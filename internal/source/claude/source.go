package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/platform"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/session/toolsem"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/jsonl"
)

const formatName = "claude/jsonl"

// Adapter 是 claude 的 Source 实现。
//
// 存储形态：$CLAUDE_CONFIG_DIR/projects/<cwd-slug>/<uuid>.jsonl
// 没有进程可探测；项目目录名是 cwd 的转义，真实 cwd 以记录里的字段为准。
type Adapter struct{ platform platform.Adapter }

// New 返回 claude 适配器。
func New(adapters ...platform.Adapter) *Adapter {
	var p platform.Adapter
	if len(adapters) > 0 {
		p = adapters[0]
	}
	if p == nil {
		p = platform.Detect()
	}
	return &Adapter{platform: p}
}

// ID 实现 source.Source。
func (a *Adapter) ID() session.SourceID { return session.Claude }

// Capabilities 实现 source.Source。
func (a *Adapter) Capabilities() source.Capabilities {
	return source.Capabilities{
		ProcessDiscovery:  false,
		WorkspaceCatalog:  false,
		ServerSideFilter:  false,
		PrefixLookup:      false,
		SessionTitles:     false,
		SubagentTree:      true, // isSidechain + parentUuid
		TurnHints:         true, // turnPosition
		Tokens:            true,
		Cost:              false,
		ReasoningText:     false, // 本机样本未见 thinking 块，按"不保证"处理
		ToolArgs:          true,
		ToolOutput:        true,
		ToolStatus:        false,
		CompactionRecords: false,
		LiveActivity:      false,
		ListCost:          source.ListHeaderScan,
		OpenCost:          source.OpenStream,
	}
}

// Locate 定位 projects 根目录。
func (a *Adapter) Locate(ctx context.Context, l source.Locator) ([]source.Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Store != "" {
		return []source.Store{probe(l.Store, "l0:--store")}, nil
	}
	home := l.Home
	rule := "l1:--home"
	if home == "" {
		home = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		rule = "l1:env CLAUDE_CONFIG_DIR"
	}
	if home == "" {
		userHome, err := a.platform.HomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".claude")
		rule = "l2:claude-default"
	}
	return []source.Store{probe(filepath.Join(home, "projects"), rule)}, nil
}

func probe(path, rule string) source.Store {
	store := source.Store{Source: session.Claude, Path: path, Kind: "jsonl-dir", Format: formatName, ReadOnly: true, Rule: rule}
	info, err := os.Stat(path)
	if err != nil {
		store.Detail = "目录不存在（已尝试）"
		return store
	}
	store.Exists = true
	store.ModTime = info.ModTime().UnixMilli()
	files, err := sessionFiles(path)
	if err != nil {
		store.Detail = "扫描项目目录失败：" + err.Error()
		return store
	}
	if len(files) == 0 {
		store.Detail = "目录里没有 <project>/<uuid>.jsonl"
		return store
	}
	store.Readable = true
	store.Version = "jsonl"
	store.Evidence = fmt.Sprintf("%d 个会话文件", len(files))
	return store
}

func sessionFiles(root string) ([]string, error) {
	out := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func (a *Adapter) Sessions(ctx context.Context, store source.Store, q source.Query) ([]session.Summary, error) {
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Claude, Store: store.Path, Reason: store.Detail}
	}
	files, err := sessionFiles(store.Path)
	if err != nil {
		return nil, err
	}
	out := make([]session.Summary, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, summarize(ctx, path, store))
	}
	return out, nil
}

const scanLines = 200

// titleTailBytes 是读文件尾部找标题线索的上限（custom-title / last-prompt 都在末尾）。
const titleTailBytes = 64 << 10

// tailTitles 从 transcript 尾部取标题线索：custom-title（自定义标题）和最近一次 prompt。
// 官方 SDK 的 summary 优先级是 custom title → 最近 prompt → 自动摘要 → 首条 prompt；
// 只扫头部拿不到最近 prompt，所以补一次尾部扫描。
func tailTitles(path string) (custom, last string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", ""
	}
	start := info.Size() - titleTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", ""
	}
	data, err := io.ReadAll(io.LimitReader(file, titleTailBytes))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), string(rune(10))) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record claudeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record.Type == "custom-title" {
			if value := record.customTitleValue(); value != "" {
				custom = value
			}
		}
		if record.LastPrompt != "" {
			last = record.LastPrompt
			continue
		}
		if record.Type != "user" || record.sidechain() || record.IsMeta || !isHuman(record) {
			continue
		}
		text := strings.TrimSpace(humanText(record))
		if text == "" || looksLikeInjectedText(text) {
			continue
		}
		last = text
	}
	return custom, last
}

func summarize(ctx context.Context, path string, store source.Store) session.Summary {
	summary := session.Summary{
		Ref:         session.Ref{Source: session.Claude, Store: store.Path, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Object: path},
		UpdatedKind: "file-mtime",
		Messages:    -1,
		TitleSource: "none",
		Flags:       []string{},
	}
	if info, err := os.Stat(path); err == nil {
		summary.UpdatedMs = session.I64(info.ModTime().UnixMilli())
	}
	title := ""
	lines := 0
	_ = jsonl.Each(ctx, path, func(number int, line []byte) error {
		lines++
		if number > scanLines {
			return errStop
		}
		var record claudeRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil
		}
		if record.SessionID != "" && record.SessionID != summary.Ref.ID {
			summary.Flags = append(summary.Flags, "session-id:"+record.SessionID)
		}
		if record.Cwd != "" && summary.Directory == "" {
			summary.Directory = record.Cwd
			summary.WorkspaceKey = record.Cwd
		}
		if record.Version != "" && summary.Agent == "" {
			summary.Agent = "claude " + record.Version
		}
		if record.IsSidechain {
			summary.IsChild = true
			summary.Flags = append(summary.Flags, "sidechain")
		}
		if record.Type == "user" && title == "" && isHuman(record) {
			if text := humanText(record); text != "" && !isCommandText(text) {
				title = firstLine(text)
			}
		}
		return nil
	})
	// 对齐 SDK 的 summary 优先级：custom title → 最近一次 prompt → 首条 prompt。
	custom, last := tailTitles(path)
	switch {
	case strings.TrimSpace(custom) != "":
		summary.Title = firstLine(custom)
		summary.TitleSource = "native"
	case strings.TrimSpace(last) != "":
		summary.Title = firstLine(last)
		summary.TitleSource = "derived"
	case title != "":
		summary.Title = title
		summary.TitleSource = "derived"
	}
	summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
		Level: "info", Code: "mtime-updated", Title: "UPDATED 取自文件修改时间",
		Detail: "claude 不在会话文件里写最后活动时间；按最后活动排序需要全扫。",
	})
	if lines > scanLines {
		summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
			Level: "info", Code: "title-scan-bounded", Title: fmt.Sprintf("标题只扫了前 %d 行", scanLines),
		})
	}
	return summary
}

// Open 流式读一个会话文件。
func (a *Adapter) Open(ctx context.Context, store source.Store, ref session.Ref, o source.OpenOptions) (*session.Session, error) {
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Claude, Store: store.Path, Reason: store.Detail}
	}
	path, err := locateSession(store.Path, ref.ID)
	if err != nil {
		return nil, err
	}
	return mapSession(ctx, path, store, o)
}

func locateSession(root, id string) (string, error) {
	files, err := sessionFiles(root)
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if strings.TrimSuffix(filepath.Base(path), ".jsonl") == id {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w：%s", source.ErrNotFound, id)
}

type claudeRecord struct {
	Type        string  `json:"type"`
	Subtype     string  `json:"subtype"`
	UUID        string  `json:"uuid"`
	ParentUUID  *string `json:"parentUuid"`
	Timestamp   string  `json:"timestamp"`
	Cwd         string  `json:"cwd"`
	SessionID   string  `json:"sessionId"`
	Version     string  `json:"version"`
	IsSidechain bool    `json:"isSidechain"`
	IsMeta      bool    `json:"isMeta"`
	// 子 agent 消息的挂载点（SDK 的 parent_tool_use_id / parent_agent_id）。
	ParentToolUseID  *string `json:"parentToolUseId"`
	ParentToolUseIDS *string `json:"parent_tool_use_id"`
	ParentAgentID    *string `json:"parentAgentId"`
	ParentAgentIDS   *string `json:"parent_agent_id"`
	// 会话标题相关记录：custom-title / last-prompt。
	CustomTitle       string `json:"customTitle"`
	Title             string `json:"title"`
	LastPrompt        string `json:"lastPrompt"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	APIErrorStatus    *int64 `json:"apiErrorStatus"`
	Error             string `json:"error"`
	DurationMs        *int64 `json:"durationMs"`
	PromptSource      string `json:"promptSource"`
	Origin            *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	TurnPosition *struct {
		PromptIndex int `json:"promptIndex"`
		TurnIndex   int `json:"turnIndex"`
	} `json:"turnPosition"`
	Message json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *struct {
		InputTokens              *int64 `json:"input_tokens"`
		OutputTokens             *int64 `json:"output_tokens"`
		CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// knownIgnored：确认与对话无关的记录类型（见 docs/sources/claude.md）。
var knownIgnored = map[string]bool{
	"mode":                  true,
	"permission-mode":       true,
	"atis-latch":            true,
	"file-history-snapshot": true,
	"attachment":            true,
	"api-request":           true,
	"api-request-blob":      true,
	"api-request-shape":     true,
	"cost-state":            true,
	"queue-operation":       true,
	"tools-used":            true,
}

var errStop = errors.New("stop")

func mapSession(ctx context.Context, path string, store source.Store, opts source.OpenOptions) (*session.Session, error) {
	if opts.MaxTextBytes <= 0 {
		opts.MaxTextBytes = 256 << 10
	}
	objectID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	out := &session.Session{
		Ref: session.Ref{Source: session.Claude, Store: store.Path, Object: path, ID: objectID},
		Meta: session.Meta{
			// 文件名就是 claude 的会话 id；Meta 与 Ref 必须一致，否则导出后按 meta 查不到会话。
			ID: session.Str(objectID),
			Provenance: session.Provenance{
				Source: session.Claude, Store: store.Path, Format: formatName, LoadedAt: time.Now().UnixMilli(), Complete: true,
			},
		},
		Diagnostics: make([]session.Diagnostic, 0, 3),
		Unknown:     map[string]int{},
	}
	m := &mapper{out: out, opts: opts, tools: map[string]toolRef{}, lastAssistant: -1}
	err := jsonl.Each(ctx, path, m.handle)
	if err != nil {
		var incomplete *jsonl.IncompleteError
		if errors.As(err, &incomplete) {
			out.Meta.Provenance.Complete = false
			out.Diagnostics = append(out.Diagnostics, session.Diagnostic{
				Level: "warn", Code: "torn-tail", Title: "文件尾部不完整", Detail: incomplete.Error(),
			})
		} else {
			return nil, err
		}
	}
	m.finalize()
	return out, nil
}

type toolRef struct {
	message int
	part    int
}

type mapper struct {
	out           *session.Session
	opts          source.OpenOptions
	tools         map[string]toolRef
	lastAssistant int
	turnIndex     int
	firstMs       int64
	truncated     bool
	derivedStatus bool
	sawThinking   bool
	injected      int
	customTitle   string
	lastPrompt    string
	lastHuman     string
	sidechain     int
}

func (m *mapper) handle(number int, line []byte) error {
	var record claudeRecord
	if err := json.Unmarshal(line, &record); err != nil {
		m.out.Unknown["line:unparsable"]++
		return nil
	}
	at, _ := parseISO(record.Timestamp)
	m.observe(record, at)
	if record.LastPrompt != "" {
		m.lastPrompt = record.LastPrompt
	}
	if record.Type == "custom-title" {
		if title := record.customTitleValue(); title != "" {
			m.customTitle = title
		}
	}
	switch record.Type {
	case "last-prompt", "custom-title":
		// 标题类记录：上面已取值，不产出消息。
		return nil
	case "user":
		m.applyUser(record, at)
	case "assistant":
		m.applyAssistant(record, at)
	case "system":
		if record.Subtype == "turn_duration" && m.lastAssistant >= 0 && record.DurationMs != nil {
			completed := at
			message := &m.out.Messages[m.lastAssistant]
			if message.CreatedMs != nil {
				completed = *message.CreatedMs + *record.DurationMs
			}
			message.CompletedMs = session.I64(completed)
			message.UpdatedMs = session.I64(completed)
			for position := range message.Parts {
				part := &message.Parts[position]
				// 文本/思考的起止在记录里是同一个写入时刻；turn_duration 给了整段耗时，
				// 用它把结束时间补到消息完成时刻，模型泳道才有真实宽度。
				if part.StartMs != nil && part.EndMs != nil && *part.EndMs == *part.StartMs {
					part.EndMs = session.I64(completed)
					part.UpdatedMs = session.I64(completed)
				}
			}
			return nil
		}
		if record.Subtype == "api_error" {
			return nil
		}
		m.out.Unknown["system/"+record.Subtype]++
	case "summary":
		// 老版本会用 summary 记录表示压缩摘要；我们没有起止时间，只标注。
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "no-compaction", Title: "压缩记录没有耗时",
			Detail: "claude 只在 summary 记录里写压缩结果，不写起止时间。",
		})
	default:
		if knownIgnored[record.Type] {
			return nil
		}
		m.out.Unknown[record.Type]++
	}
	return nil
}

// observe 收集会话级元信息：起始时间、工作目录、版本。
// 这些字段以前只在 Sessions() 的摘要里填，Open() 出来的会话是空的，导出就丢了。
func (m *mapper) observe(record claudeRecord, at int64) {
	if at > 0 && (m.firstMs == 0 || at < m.firstMs) {
		m.firstMs = at
	}
	if record.Cwd != "" && (m.out.Meta.Directory == nil || *m.out.Meta.Directory == "") {
		m.out.Meta.Directory = session.Str(record.Cwd)
		m.out.Meta.WorkspaceKey = record.Cwd
	}
	if record.Version != "" && (m.out.Meta.Version == nil || *m.out.Meta.Version == "") {
		m.out.Meta.Version = session.Str(record.Version)
		m.out.Meta.Agent = session.Str("claude " + record.Version)
	}
}

func (m *mapper) applyUser(record claudeRecord, at int64) {
	if record.sidechain() {
		// 子 agent 的对话（SDK 用 parent_tool_use_id/parent_agent_id 挂到父工具调用）：
		// 不并入主会话轮次，单独计数并给出诊断。
		m.sidechain++
		return
	}
	message, blocks, err := decodeMessage(record.Message)
	if err != nil {
		m.out.Unknown["user:unparsable"]++
		return
	}
	// 含 tool_result 的 user 记录是工具回执，不是人类输入。
	results := make([]claudeBlock, 0, 2)
	texts := make([]claudeBlock, 0, 2)
	for _, block := range blocks {
		switch block.Type {
		case "tool_result":
			results = append(results, block)
		case "text":
			texts = append(texts, block)
		}
	}
	for _, result := range results {
		m.applyToolResult(result, at)
	}
	// isMeta 提示、/exit 之外的 CLI 回显不是人类输入：保留成 system 消息，不参与轮次。
	if !isHuman(record) {
		if len(texts) > 0 {
			m.appendInjected(record, texts, at)
		}
		return
	}
	// 用户主动调用的本地命令（/exit、/model）算用户输入，文本归一成 "/exit"。
	if command, ok := commandInvocation(texts); ok {
		m.appendCommand(record, command, at)
		return
	}
	if looksLikeLocalCommand(texts) {
		if len(texts) > 0 {
			m.appendInjected(record, texts, at)
		}
		return
	}
	if len(texts) == 0 {
		return
	}
	parts := make([]session.Part, 0, len(texts))
	for index, block := range texts {
		text := m.clip(block.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, session.Part{
			ID:        record.UUID + "#" + itoa(index),
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
			Text:      &text,
		})
	}
	id := record.UUID
	if id == "" {
		id = "user-" + itoa(len(m.out.Messages))
	}
	hint := session.TurnHint{}
	if record.TurnPosition != nil {
		hint.Turn = record.TurnPosition.TurnIndex
		m.turnIndex = record.TurnPosition.TurnIndex
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleUser,
		PayloadRole: session.Str("user"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		TurnHint:    hint,
		Parts:       parts,
	})
	var builder strings.Builder
	for _, part := range parts {
		if part.Text == nil {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(*part.Text)
	}
	if text := strings.TrimSpace(builder.String()); text != "" {
		m.lastHuman = text
	}
	_ = message
}

func (m *mapper) applyAssistant(record claudeRecord, at int64) {
	if record.sidechain() {
		m.sidechain++
		return
	}
	message, blocks, err := decodeMessage(record.Message)
	if err != nil {
		m.out.Unknown["assistant:unparsable"]++
		return
	}
	parts := make([]session.Part, 0, len(blocks))
	for index, block := range blocks {
		switch block.Type {
		case "text":
			text := m.clip(block.Text)
			if strings.TrimSpace(text) == "" {
				continue
			}
			parts = append(parts, session.Part{
				ID: record.UUID + "#" + itoa(index), Kind: session.PartText, Type: "text",
				CreatedMs: session.I64(at), UpdatedMs: session.I64(at),
				StartMs: session.I64(at), EndMs: session.I64(at), Text: &text,
			})
		case "thinking":
			if block.Thinking == "" {
				continue
			}
			m.sawThinking = true
			text := m.clip(block.Thinking)
			parts = append(parts, session.Part{
				ID: record.UUID + "#" + itoa(index), Kind: session.PartReasoning, Type: "thinking",
				CreatedMs: session.I64(at), UpdatedMs: session.I64(at),
				StartMs: session.I64(at), EndMs: session.I64(at), Text: &text,
			})
		case "tool_use":
			call := &session.ToolCall{CallID: session.Str(block.ID), Name: session.Str(block.Name), Args: block.Input, StartMs: session.I64(at)}
			call.Kind, call.Target, call.Command = toolsem.Classify(session.Claude, block.Name, block.Input)
			m.tools[block.ID] = toolRef{message: len(m.out.Messages), part: len(parts)}
			parts = append(parts, session.Part{
				ID: record.UUID + "#" + itoa(index), Kind: session.PartToolCall, Type: "tool-call",
				CreatedMs: session.I64(at), UpdatedMs: session.I64(at), StartMs: session.I64(at), Tool: call,
			})
		}
	}
	id := record.UUID
	if id == "" {
		id = "assistant-" + itoa(len(m.out.Messages))
	}
	hint := session.TurnHint{Turn: m.turnIndex}
	if record.TurnPosition != nil {
		hint.Turn = record.TurnPosition.TurnIndex
	}
	assistant := session.Message{
		ID:          id,
		Role:        session.RoleAssistant,
		PayloadRole: session.Str("assistant"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		TurnHint:    hint,
		Finish:      finishOf(message.StopReason),
		Tokens:      tokensOf(message),
		Parts:       parts,
	}
	if message.Model != "" {
		assistant.Model = &session.ModelRef{ID: session.Str(message.Model)}
	}
	if record.IsAPIErrorMessage {
		assistant.Error = &session.Error{Name: session.Str("APIError"), StatusCode: record.APIErrorStatus}
		if record.Error != "" {
			assistant.Error.Code = session.Str(record.Error)
		}
	}
	if m.lastAssistant >= 0 {
		m.closeOpenAssistant(m.lastAssistant)
	}
	m.out.Messages = append(m.out.Messages, assistant)
	m.lastAssistant = len(m.out.Messages) - 1
}

// closeOpenAssistant 给上一条 assistant 补结束时间：它最后一个 part 的 end。
// claude 只在 system{turn_duration} 写步骤结束；下一条 assistant 已经出现时，
// 上一条实际上已经结束，用真实 part 时间收口，避免导出里留着"永远未完成"的步骤。
func (m *mapper) closeOpenAssistant(index int) {
	message := &m.out.Messages[index]
	if message.CompletedMs != nil {
		return
	}
	var end *int64
	for position := range message.Parts {
		part := message.Parts[position]
		if part.EndMs != nil && (end == nil || *part.EndMs > *end) {
			end = part.EndMs
		}
		if part.Tool != nil && part.Tool.EndMs != nil && (end == nil || *part.Tool.EndMs > *end) {
			end = part.Tool.EndMs
		}
	}
	if end != nil {
		message.CompletedMs = end
	}
}

// commandInvocation 识别 "/exit"、"/model" 这类用户主动调用的本地命令。
// 记录里的原始文本是 <command-name>/exit</command-name><command-message>… 的 XML，
// 页面只需要命令名本身。
func commandInvocation(blocks []claudeBlock) (string, bool) {
	const open = "<command-name>"
	const close = "</command-name>"
	for _, block := range blocks {
		text := block.Text
		start := strings.Index(text, open)
		if start < 0 {
			continue
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:end])
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		return name, true
	}
	return "", false
}

// appendCommand 把用户调用的本地命令记成一条 user 消息（会开一轮，但没有 assistant 回复）。
func (m *mapper) appendCommand(record claudeRecord, command string, at int64) {
	text := m.clip(command)
	id := record.UUID
	if id == "" {
		id = "user-" + itoa(len(m.out.Messages))
	}
	hint := session.TurnHint{}
	if record.TurnPosition != nil {
		hint.Turn = record.TurnPosition.TurnIndex
		m.turnIndex = record.TurnPosition.TurnIndex
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleUser,
		PayloadRole: session.Str("user"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		TurnHint:    hint,
		Parts: []session.Part{{
			ID:        id + "#0",
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
			Text:      &text,
		}},
	})
}

// appendInjected 把本地命令/元信息记录保留成 system 消息：不进轮次，但导出里能查到。
func (m *mapper) appendInjected(record claudeRecord, texts []claudeBlock, at int64) {
	parts := make([]session.Part, 0, len(texts))
	for index, block := range texts {
		text := m.clip(block.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, session.Part{
			ID:        record.UUID + "#" + itoa(index),
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
			Text:      &text,
			Synthetic: session.Bool(true),
		})
	}
	if len(parts) == 0 {
		return
	}
	id := record.UUID
	if id == "" {
		id = "system-" + itoa(len(m.out.Messages))
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleSystem,
		PayloadRole: session.Str("system"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		Synthetic:   true,
		Parts:       parts,
	})
	m.injected++
}

// looksLikeLocalCommand 识别 claude CLI 的本地命令回显/中断标记（不是用户提示词）。
func looksLikeLocalCommand(blocks []claudeBlock) bool {
	for _, block := range blocks {
		if looksLikeInjectedText(block.Text) {
			return true
		}
	}
	return false
}

// looksLikeInjectedText：claude 自己写的“伪 user 文本”。
//   - <local-command-*> / <command-*>：/exit 等 CLI 操作的回显；
//   - [Request interrupted by user]：用户中断时补的状态标记。
//
// isCommandText：/exit 这类命令的原始 XML（不能当标题）。
func isCommandText(text string) bool {
	return strings.Contains(text, "<command-name>")
}

func looksLikeInjectedText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<local-command-") || strings.HasPrefix(trimmed, "<command-") {
		return true
	}
	return strings.HasPrefix(trimmed, "[Request interrupted by user")
}

func (m *mapper) applyToolResult(block claudeBlock, at int64) {
	ref, ok := m.tools[block.ToolUseID]
	if !ok {
		m.out.Unknown["tool-result:unpaired"]++
		return
	}
	message := &m.out.Messages[ref.message]
	if ref.part >= len(message.Parts) {
		return
	}
	part := &message.Parts[ref.part]
	if part.Tool == nil {
		return
	}
	text := m.clip(decodeResultContent(block.Content))
	part.Tool.Output = marshalString(text)
	part.Tool.EndMs = session.I64(at)
	part.EndMs = session.I64(at)
	status := "completed"
	if block.IsError {
		status = "error"
	}
	part.Tool.Status = &status
	m.derivedStatus = true
	message.UpdatedMs = session.I64(at)
}

func (m *mapper) finalize() {
	if m.derivedStatus {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "derived-tool-status", Title: "工具状态由 tool_result 推断",
			Detail: "claude 的工具记录没有统一状态字段；这里按是否存在 tool_result（及其 is_error）推断。",
		})
	}
	if !m.sawThinking {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "no-reasoning", Title: "本会话没有思考记录",
			Detail: "claude 只有开启思考时才会写 thinking 块；没有就是不显示。",
		})
	}
	if m.injected > 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "local-command", Title: itoa(m.injected) + " 条 CLI 回显/元信息记录未计入对话",
			Detail: "caveat/stdout 回显与 isMeta 提示已按 system 保存，不参与轮次；/exit、/model 这类用户主动调用的命令仍算用户输入。",
		})
	}
	if m.sidechain > 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "subagent-messages", Title: itoa(m.sidechain) + " 条子 agent 消息未并入主会话",
			Detail: "带 isSidechain / parent_tool_use_id / parent_agent_id 的记录属于子 agent；官方 SDK 用父工具调用挂载，本模型暂未表达，先跳过。",
		})
	}
	if m.truncated {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "warn", Code: "truncated-text", Title: "文本/工具输出按上限截断",
			Detail: "只保留前 " + itoa(m.opts.MaxTextBytes) + " 字节。",
		})
	}
	if m.firstMs > 0 && m.out.Meta.CreatedMs == nil {
		m.out.Meta.CreatedMs = session.I64(m.firstMs)
	}
	if len(m.out.Messages) == 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "empty-session", Title: "这个会话文件里没有对话记录",
			Detail: "只有元信息/系统类记录；导出后消息为空，不是读取失败。",
		})
	}
	if len(m.out.Messages) > 0 {
		if last := m.out.Messages[len(m.out.Messages)-1]; last.UpdatedMs != nil {
			m.out.Meta.UpdatedMs = last.UpdatedMs
		}
	}
	// 标题优先级对齐官方 SDK 的 summary：custom title → 最近一次 prompt → 首条 prompt。
	title := ""
	switch {
	case strings.TrimSpace(m.customTitle) != "":
		title = m.customTitle
	case strings.TrimSpace(m.lastPrompt) != "":
		title = m.lastPrompt
	case strings.TrimSpace(m.lastHuman) != "":
		title = m.lastHuman
	default:
		title = firstUserText(m.out.Messages)
	}
	if title = firstLine(title); title != "" {
		m.out.Meta.Title = session.Str(title)
	}
	for index := len(m.out.Messages) - 1; index >= 0; index-- {
		if m.out.Messages[index].Model != nil {
			m.out.Meta.Model = m.out.Messages[index].Model
			break
		}
	}
}

func decodeMessage(raw json.RawMessage) (claudeMessage, []claudeBlock, error) {
	var message claudeMessage
	if len(raw) == 0 {
		return message, nil, nil
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return message, nil, err
	}
	trimmed := strings.TrimSpace(string(message.Content))
	if trimmed == "" || trimmed == "null" {
		return message, nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var blocks []claudeBlock
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return message, nil, err
		}
		return message, blocks, nil
	}
	var text string
	if err := json.Unmarshal(message.Content, &text); err != nil {
		return message, nil, err
	}
	return message, []claudeBlock{{Type: "text", Text: text}}, nil
}

// sidechain 判断这条记录属于子 agent（不是主会话的人类输入/回复）。
func (r claudeRecord) sidechain() bool {
	return r.IsSidechain || nonEmptyPtr(r.ParentToolUseID) || nonEmptyPtr(r.ParentToolUseIDS) || nonEmptyPtr(r.ParentAgentID) || nonEmptyPtr(r.ParentAgentIDS)
}

func (r claudeRecord) customTitleValue() string {
	if strings.TrimSpace(r.CustomTitle) != "" {
		return r.CustomTitle
	}
	return r.Title
}

func nonEmptyPtr(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func isHuman(record claudeRecord) bool {
	if record.IsAPIErrorMessage || record.IsSidechain || record.IsMeta {
		return false
	}
	if record.Origin != nil {
		return record.Origin.Kind == "human"
	}
	if record.PromptSource != "" {
		// typed = 终端手输；sdk = 通过 Claude Agent SDK / 外部调用发来的真实提示词。
		// 两者都是人类意图；其余（auto/notification…）才是注入内容。
		switch record.PromptSource {
		case "typed", "sdk":
			return true
		default:
			return false
		}
	}
	return true
}

func humanText(record claudeRecord) string {
	_, blocks, err := decodeMessage(record.Message)
	if err != nil {
		return ""
	}
	text := ""
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			text += block.Text
		}
	}
	return text
}

func decodeResultContent(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := ""
		for index, block := range blocks {
			if block.Type != "text" {
				continue
			}
			if index > 0 && out != "" {
				out += string(rune(10))
			}
			out += block.Text
		}
		return out
	}
	return string(raw)
}

func tokensOf(message claudeMessage) *session.Tokens {
	if message.Usage == nil {
		return nil
	}
	tokens := &session.Tokens{
		Input:      message.Usage.InputTokens,
		Output:     message.Usage.OutputTokens,
		CacheRead:  message.Usage.CacheReadInputTokens,
		CacheWrite: message.Usage.CacheCreationInputTokens,
	}
	if tokens.Input == nil && tokens.Output == nil && tokens.CacheRead == nil && tokens.CacheWrite == nil {
		return nil
	}
	return tokens
}

func finishOf(reason string) *string {
	if reason == "" {
		return nil
	}
	return session.Str(reason)
}

func firstUserText(messages []session.Message) string {
	for _, message := range messages {
		if message.Role != session.RoleUser {
			continue
		}
		text := ""
		for _, part := range message.Parts {
			if part.Text != nil {
				text += *part.Text
			}
		}
		if strings.TrimSpace(text) != "" {
			return firstLine(text)
		}
	}
	return ""
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, string(rune(10))) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		if len(runes) > 60 {
			return string(runes[:60])
		}
		return trimmed
	}
	return ""
}

func parseISO(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	if stamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return stamp.UnixMilli(), true
	}
	return 0, false
}

func (m *mapper) clip(text string) string {
	if len(text) <= m.opts.MaxTextBytes {
		return text
	}
	m.truncated = true
	return text[:m.opts.MaxTextBytes]
}

func marshalString(text string) []byte {
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
