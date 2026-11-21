package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gzzyj/atrace/internal/session"
	"github.com/gzzyj/atrace/internal/source"
	"github.com/gzzyj/atrace/internal/source/jsonl"
)

const formatName = "codex/rollout"

// Adapter 是 codex 的 Source 实现。
//
// 存储形态：$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl.zst
// 无进程可探测；会话标题、工作区都从文件内容派生。
type Adapter struct{}

// New 返回 codex 适配器。
func New() *Adapter { return &Adapter{} }

// ID 实现 source.Source。
func (a *Adapter) ID() session.SourceID { return session.Codex }

// Capabilities 实现 source.Source。
func (a *Adapter) Capabilities() source.Capabilities {
	return source.Capabilities{
		ProcessDiscovery:  false,
		WorkspaceCatalog:  false,
		ServerSideFilter:  false,
		PrefixLookup:      false,
		SessionTitles:     false, // 由首条人类输入派生
		SubagentTree:      false,
		TurnHints:         true,
		Tokens:            true,
		Cost:              false,
		ReasoningText:     false, // 多数 rollout 只存加密的 reasoning
		ToolArgs:          true,
		ToolOutput:        true,
		ToolStatus:        false, // 由"是否出现输出"推断
		CompactionRecords: true,
		LiveActivity:      false,
		ListCost:          source.ListHeaderScan,
		OpenCost:          source.OpenStream,
	}
}

// Locate 定位 sessions 根目录。
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
		home = strings.TrimSpace(os.Getenv("CODEX_HOME"))
		rule = "l1:env CODEX_HOME"
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("取 HOME：%w", err)
		}
		home = filepath.Join(userHome, ".codex")
		rule = "l2:codex-default"
	}
	return []source.Store{probe(filepath.Join(home, "sessions"), rule)}, nil
}

func probe(path, rule string) source.Store {
	store := source.Store{Source: session.Codex, Path: path, Kind: "jsonl-dir", Format: formatName, ReadOnly: true, Rule: rule}
	info, err := os.Stat(path)
	if err != nil {
		store.Detail = "目录不存在（已尝试）"
		return store
	}
	store.Exists = true
	store.ModTime = info.ModTime().UnixMilli()
	files, err := rolloutFiles(path)
	if err != nil {
		store.Detail = "扫描 rollout 失败：" + err.Error()
		return store
	}
	if len(files) == 0 {
		store.Detail = "目录里没有 rollout-*.jsonl.zst"
		return store
	}
	store.Readable = true
	store.Version = "rollout"
	store.Evidence = fmt.Sprintf("%d 个 rollout 文件", len(files))
	return store
}

func rolloutFiles(root string) ([]string, error) {
	out := make([]string, 0, 128)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个目录读不了不影响其它
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, ".jsonl.zst") || strings.HasSuffix(name, ".jsonl")) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// Sessions 对每个 rollout 只做"有限行扫描"：拿 session_meta + 首条人类输入。
func (a *Adapter) Sessions(ctx context.Context, store source.Store, q source.Query) ([]session.Summary, error) {
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Codex, Store: store.Path, Reason: store.Detail}
	}
	files, err := rolloutFiles(store.Path)
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

const titleScanLines = 400

func summarize(ctx context.Context, path string, store source.Store) session.Summary {
	summary := session.Summary{
		Ref:         session.Ref{Source: session.Codex, Store: store.Path, ID: sessionIDFromName(path), Object: path},
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
		if number > titleScanLines {
			return errStop
		}
		var record rolloutRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil
		}
		switch record.Type {
		case "session_meta":
			var meta sessionMeta
			if err := json.Unmarshal(record.Payload, &meta); err == nil {
				if meta.SessionID != "" {
					summary.Ref.ID = meta.SessionID
				}
				summary.Directory = meta.Cwd
				summary.WorkspaceKey = meta.Cwd
				summary.Agent = meta.Originator
				if meta.CLIVersion != "" {
					summary.Flags = append(summary.Flags, "cli:"+meta.CLIVersion)
				}
				if meta.Timestamp != "" {
					if ms, ok := parseISO(meta.Timestamp); ok {
						summary.CreatedMs = session.I64(ms)
					}
				}
			}
		case "event_msg":
			var event eventPayload
			if err := json.Unmarshal(record.Payload, &event); err == nil && event.Type == "item_completed" && title == "" {
				if text := itemText(event.Item); text != "" {
					title = firstLine(text)
				}
			}
		}
		return nil
	})
	if title != "" {
		summary.Title = title
		summary.TitleSource = "derived"
	}
	summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
		Level: "info", Code: "mtime-updated", Title: "UPDATED 取自文件修改时间",
		Detail: "codex 不在会话文件里写最后活动时间；按最后活动排序需要全扫。",
	})
	if lines > titleScanLines {
		summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
			Level: "info", Code: "title-scan-bounded", Title: fmt.Sprintf("标题只扫了前 %d 行", titleScanLines),
			Detail: "为避免列表变慢，标题从首条人类输入派生；没有就是没有，不编造。",
		})
	}
	return summary
}

// Open 流式读 rollout 并映射成领域模型。
func (a *Adapter) Open(ctx context.Context, store source.Store, ref session.Ref, o source.OpenOptions) (*session.Session, error) {
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Codex, Store: store.Path, Reason: store.Detail}
	}
	path, err := locateRollout(store.Path, ref.ID)
	if err != nil {
		return nil, err
	}
	return mapRollout(ctx, path, store, o)
}

func locateRollout(root, id string) (string, error) {
	files, err := rolloutFiles(root)
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if strings.Contains(filepath.Base(path), id) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w：%s", source.ErrNotFound, id)
}

func sessionIDFromName(path string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	// rollout-2026-09-22T18-17-53-01a0c89f-3c06-73c3-aae8-e6de46100531
	if index := strings.LastIndex(base, "-"); index < 0 {
		return base
	}
	parts := strings.Split(base, "-")
	if len(parts) < 5 {
		return base
	}
	return strings.Join(parts[len(parts)-5:], "-")
}

// itemText 取出 item_completed{UserMessage}.content 里的文本（标题派生用）。
func itemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var completed item
	if err := json.Unmarshal(raw, &completed); err != nil {
		return ""
	}
	text := ""
	for _, block := range completed.Content {
		if block.Text == "" {
			continue
		}
		if text != "" {
			text += string(rune(10))
		}
		text += block.Text
	}
	return text
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, string(rune(10))) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			runes := []rune(trimmed)
			if len(runes) > 60 {
				return string(runes[:60])
			}
			return trimmed
		}
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

var errStop = errors.New("stop")
