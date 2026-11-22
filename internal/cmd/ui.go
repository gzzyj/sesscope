package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/attribution"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

const (
	uiSource = iota
	uiWorkspace
	uiSessions
	uiQuit = -1
)

type uiAction int

const (
	actionStay uiAction = iota
	actionBack
	actionToSource
	actionToWorkspace
	actionQuit
)

func newUICommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var noBrowser bool
	var perPage int
	command := &cobra.Command{
		Use:     "ui",
		Aliases: []string{"browse", "pick"},
		Short:   "交互式浏览：来源 → 工作区 → 会话 → 浏览器打开",
		Long: "逐级选择来源、工作区、会话，把选中的会话渲染成自包含 HTML 并用浏览器打开。\n\n" +
			"按键：\n" +
			"  数字    选择当前页的条目\n" +
			"  a       当前层全部（工作区层）\n" +
			"  n / p   下一页 / 上一页（会话层）\n" +
			"  o       渲染并在浏览器打开（详情层）\n" +
			"  b       返回上一层\n" +
			"  s       直接回到来源选择\n" +
			"  q       退出\n\n" +
			"非交互环境（管道）也能用：把按键按行喂给 stdin 即可。",
		GroupID: "discover",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			picker := &picker{
				env:       env,
				in:        bufio.NewReader(cmd.InOrStdin()),
				out:       cmd.OutOrStdout(),
				errOut:    cmd.ErrOrStderr(),
				ctx:       cmd.Context(),
				locator:   sourceOpts.locator(),
				names:     sourceOpts.sources,
				noBrowser: noBrowser,
				perPage:   perPage,
			}
			return picker.run()
		},
	}
	sourceOpts.add(command)
	command.Flags().BoolVar(&noBrowser, "no-browser", false, "只渲染并打印路径，不自动打开浏览器")
	command.Flags().IntVar(&perPage, "per-page", 15, "会话列表每页条数")
	return command
}

type picker struct {
	env       *app.Env
	in        *bufio.Reader
	out       io.Writer
	errOut    io.Writer
	ctx       context.Context
	locator   source.Locator
	names     []string
	noBrowser bool
	perPage   int

	sourceID  session.SourceID
	workspace string
	page      int
	list      *app.SessionList
}

type wsEntry struct {
	key   string
	label string
	count int
	last  *int64
}

func (p *picker) run() error {
	state := uiSource
	for {
		switch state {
		case uiSource:
			id, action := p.chooseSource()
			if action == actionQuit {
				return nil
			}
			p.sourceID = id
			p.workspace = ""
			p.page = 0
			p.list = nil
			state = uiWorkspace
		case uiWorkspace:
			key, action := p.chooseWorkspace()
			switch action {
			case actionQuit:
				return nil
			case actionBack, actionToSource:
				state = uiSource
			default:
				p.workspace = key
				p.page = 0
				state = uiSessions
			}
		case uiSessions:
			item, action := p.chooseSession()
			switch action {
			case actionQuit:
				return nil
			case actionBack:
				state = uiWorkspace
			case actionToSource:
				state = uiSource
			case actionToWorkspace:
				state = uiWorkspace
			default:
				state = p.showDetail(item)
				if state == uiQuit {
					return nil
				}
			}
		}
	}
}

func (p *picker) clear() {
	if !isTerminal(p.out) {
		return
	}
	fmt.Fprint(p.out, "\033[2J\033[H")
}

func (p *picker) line(prompt string) (string, bool) {
	fmt.Fprint(p.out, prompt)
	text, err := p.in.ReadString('\n')
	if err != nil && strings.TrimSpace(text) == "" {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func (p *picker) chooseSource() (session.SourceID, uiAction) {
	choices, err := p.env.Registry.Select(p.names)
	if err != nil {
		fmt.Fprintln(p.errOut, "来源有误：", err)
		return "", actionQuit
	}
	for {
		p.clear()
		fmt.Fprintln(p.out, "选择来源（1/3）")
		for index, item := range choices {
			status := ""
			if selection, err := p.env.Select(p.ctx, []string{string(item.ID())}, p.locator); err == nil {
				if len(selection.Readable()) == 0 {
					status = "（没有可读存储）"
				}
			}
			fmt.Fprintf(p.out, "  %d) %-8s %s\n", index+1, item.ID(), status)
		}
		fmt.Fprintln(p.out, "  q) 退出")
		text, ok := p.line("> ")
		if !ok || text == "q" || text == "quit" {
			return "", actionQuit
		}
		index, err := strconv.Atoi(text)
		if err != nil || index < 1 || index > len(choices) {
			continue
		}
		return choices[index-1].ID(), actionStay
	}
}

func (p *picker) sessions() (*app.SessionList, error) {
	if p.list != nil {
		return p.list, nil
	}
	list, err := p.env.Sessions(p.ctx, []string{string(p.sourceID)}, p.locator, source.Query{
		IncludeChildren: true,
		Limit:           0,
		Sort:            "updated",
	})
	if err != nil {
		return nil, err
	}
	p.list = list
	return list, nil
}

func (p *picker) chooseWorkspace() (string, uiAction) {
	list, err := p.sessions()
	if err != nil {
		fmt.Fprintln(p.errOut, "读取会话失败：", err)
		return "", actionBack
	}
	groups := map[string]*wsEntry{}
	for _, item := range list.Items {
		key := workspaceKeyOf(item)
		entry, ok := groups[key]
		if !ok {
			label := list.WorkspaceLabel(item)
			if label == "" {
				label = item.Directory
			}
			if label == "" {
				label = item.WorkspaceKey
			}
			if label == "" {
				label = "（无工作区）"
			}
			entry = &wsEntry{key: key, label: label}
			groups[key] = entry
		}
		entry.count++
		if item.UpdatedMs != nil && (entry.last == nil || *item.UpdatedMs > *entry.last) {
			entry.last = item.UpdatedMs
		}
	}
	entries := make([]*wsEntry, 0, len(groups))
	for _, entry := range groups {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if (left.last == nil) != (right.last == nil) {
			return left.last != nil
		}
		if left.last != nil && *left.last != *right.last {
			return *left.last > *right.last
		}
		return left.label < right.label
	})
	for {
		p.clear()
		fmt.Fprintf(p.out, "来源 %s · 选择工作区（2/3，共 %d 个会话）\n", p.sourceID, len(list.Items))
		for index, entry := range entries {
			fmt.Fprintf(p.out, "  %d) %-42s %5d 个  最后活动 %s\n",
				index+1, cli.TruncateDisplay(entry.label, 42), entry.count, cli.FormatTimePtr(entry.last))
		}
		fmt.Fprintln(p.out, "  a) 全部会话")
		fmt.Fprintln(p.out, "  b) 返回来源    q) 退出")
		text, ok := p.line("> ")
		if !ok || text == "q" {
			return "", actionQuit
		}
		switch text {
		case "b":
			return "", actionBack
		case "a", "":
			return "", actionStay
		}
		index, err := strconv.Atoi(text)
		if err != nil || index < 1 || index > len(entries) {
			continue
		}
		return entries[index-1].key, actionStay
	}
}

func (p *picker) filtered() []session.Summary {
	list, err := p.sessions()
	if err != nil {
		return nil
	}
	if p.workspace == "" {
		return list.Items
	}
	out := make([]session.Summary, 0, len(list.Items))
	for _, item := range list.Items {
		if workspaceKeyOf(item) == p.workspace {
			out = append(out, item)
		}
	}
	return out
}

func (p *picker) chooseSession() (session.Summary, uiAction) {
	items := p.filtered()
	if len(items) == 0 {
		fmt.Fprintln(p.errOut, "这个工作区没有会话")
		return session.Summary{}, actionBack
	}
	pages := (len(items) + p.perPage - 1) / p.perPage
	if p.page >= pages {
		p.page = pages - 1
	}
	if p.page < 0 {
		p.page = 0
	}
	for {
		start := p.page * p.perPage
		end := start + p.perPage
		if end > len(items) {
			end = len(items)
		}
		p.clear()
		fmt.Fprintf(p.out, "来源 %s · %s · 会话（3/3，第 %d/%d 页，共 %d 个）\n",
			p.sourceID, p.workspaceLabel(), p.page+1, pages, len(items))
		for index := start; index < end; index++ {
			item := items[index]
			topic := item.Title
			if topic == "" {
				topic = "（无标题）"
			}
			fmt.Fprintf(p.out, "  %2d) %-16s %-18s %s\n",
				index-start+1, cli.FormatTimePtr(item.UpdatedMs), cli.TruncateDisplay(item.ID, 18), cli.TruncateDisplay(topic, 54))
		}
		fmt.Fprintln(p.out, "  n) 下一页  p) 上一页  b) 返回工作区  s) 换来源  q) 退出")
		text, ok := p.line("> ")
		if !ok || text == "q" {
			return session.Summary{}, actionQuit
		}
		switch text {
		case "b":
			return session.Summary{}, actionBack
		case "s":
			return session.Summary{}, actionToSource
		case "w":
			return session.Summary{}, actionToWorkspace
		case "n":
			if p.page < pages-1 {
				p.page++
			}
			continue
		case "p":
			if p.page > 0 {
				p.page--
			}
			continue
		}
		index, err := strconv.Atoi(text)
		if err != nil || index < 1 || index > end-start {
			continue
		}
		return items[start+index-1], actionStay
	}
}

func (p *picker) workspaceLabel() string {
	if p.list == nil || p.workspace == "" {
		return "全部工作区"
	}
	for _, item := range p.list.Items {
		if workspaceKeyOf(item) == p.workspace {
			if label := p.list.WorkspaceLabel(item); label != "" {
				return label
			}
			break
		}
	}
	return strings.TrimPrefix(p.workspace, "key:")
}

func (p *picker) showDetail(item session.Summary) int {
	for {
		p.clear()
		fmt.Fprintln(p.out, "会话详情（4/4）")
		fmt.Fprintf(p.out, "  标题： %s\n", valueOr(item.Title, "（无标题）"))
		fmt.Fprintf(p.out, "  来源： %s\n", item.Source)
		fmt.Fprintf(p.out, "  id  ： %s\n", item.ID)
		fmt.Fprintf(p.out, "  目录： %s\n", valueOr(item.Directory, "-"))
		count := "消息数未知"
		if item.Messages >= 0 {
			count = strconv.Itoa(item.Messages) + " 条消息"
		}
		fmt.Fprintf(p.out, "  更新： %s（%s）\n", cli.FormatTimePtr(item.UpdatedMs), count)
		fmt.Fprintln(p.out, "  o) 渲染并在浏览器打开")
		fmt.Fprintln(p.out, "  b) 返回会话列表  w) 返回工作区  s) 换来源  q) 退出")
		text, ok := p.line("> ")
		if !ok || text == "q" {
			return uiQuit
		}
		switch text {
		case "o", "":
			if p.renderAndOpen(item) {
				return uiSessions
			}
		case "b":
			return uiSessions
		case "w":
			return uiWorkspace
		case "s":
			return uiSource
		}
	}
}

func (p *picker) renderAndOpen(item session.Summary) bool {
	selection, err := p.env.Select(p.ctx, []string{string(p.sourceID)}, p.locator)
	if err != nil {
		fmt.Fprintln(p.errOut, "定位存储失败：", err)
		return false
	}
	opened, _, err := p.env.OpenSession(p.ctx, selection, string(p.sourceID)+":"+item.ID, source.OpenOptions{})
	if err != nil {
		fmt.Fprintln(p.errOut, "打开会话失败：", err)
		return false
	}
	if len(opened.Messages) == 0 {
		fmt.Fprintln(p.errOut, "这个会话没有消息（空会话），无法渲染；换一个吧")
		_, _ = p.in.ReadString('\n')
		return false
	}
	path := filepath.Join(os.TempDir(), "sesscope-"+safeName(string(p.sourceID))+"-"+safeName(item.ID)+".html")
	view, err := p.env.View(opened, path, p.env.Clock(), attribution.DefaultChainGap)
	if err != nil {
		fmt.Fprintln(p.errOut, "归一化失败：", err)
		return false
	}
	html, err := p.env.RenderHTML(view)
	if err != nil {
		fmt.Fprintln(p.errOut, "渲染失败：", err)
		return false
	}
	if err := os.WriteFile(path, html, 0o644); err != nil {
		fmt.Fprintln(p.errOut, "写文件失败：", err)
		return false
	}
	fmt.Fprintf(p.out, "\n已渲染：%s（%d 字节）\n", path, len(html))
	if p.noBrowser {
		fmt.Fprintln(p.out, "（--no-browser：未自动打开）按 Enter 回到会话列表")
	} else if err := openInBrowser(path); err != nil {
		fmt.Fprintf(p.out, "没能自动打开浏览器（%v），请手动打开：%s\n按 Enter 返回会话列表", err, path)
	} else {
		fmt.Fprintln(p.out, "浏览器已打开；关掉它之后按 Enter 回到会话列表")
	}
	_, _ = p.in.ReadString('\n')
	return true
}

func workspaceKeyOf(item session.Summary) string {
	if item.WorkspaceKey != "" {
		return "key:" + item.WorkspaceKey
	}
	directory := strings.ToLower(strings.TrimRight(strings.ReplaceAll(item.Directory, "\\", "/"), "/"))
	if directory == "" {
		return "dir:"
	}
	return "dir:" + directory
}

func safeName(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "session"
	}
	return builder.String()
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func openInBrowser(path string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{path}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", path}
	default:
		name, args = "xdg-open", []string{path}
	}
	command := exec.Command(name, args...)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
