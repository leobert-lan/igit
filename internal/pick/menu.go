package pick

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// MenuItem 表示菜单中的一个可选项。
type MenuItem struct {
	Key      string // 快捷键或编号，如 "1", "2", "+", "0", "q"
	Label    string // 选项主标题 / 名称
	Badge    string // 徽标/状态，如 "[当前目录]", "[超出范围]"
	Detail   string // 补充信息，如 "(本目录: 1)" 或绝对路径
	Selected bool   // 用于复选模式
	Value    any    // 绑定的业务数据对象
}

// MenuOpt 菜单配置参数。
type MenuOpt struct {
	Title       string
	Prompt      string
	Items       []MenuItem
	Interactive bool      // 是否启用终端 TTY 交互（Bubble Tea 方向键操作）
	In          io.Reader // 非交互模式或管道模式下的输入流
	Out         io.Writer // 文本输出流（默认为 os.Stdout）
}

// Menu 启动单选菜单：
//  1. 若处于 TTY 交互模式且未指定自定义 In，使用 Bubble Tea 呈现全交互菜单，
//     支持方向键 (↑/↓/j/k) 移动光标、数值选项直接命中、Enter 确认以及 q/Esc 取消；
//  2. 若在管道模式、单元测试或非 TTY 环境下，回退为标准编号文本列表与命令行输入。
func Menu(opt MenuOpt) (selected *MenuItem, cancelled bool) {
	out := opt.Out
	if out == nil {
		out = os.Stdout
	}

	// 管道模式、测试注入流或非交互终端：走标准文本菜单
	if !opt.Interactive || opt.In != nil {
		return textMenu(opt, out)
	}

	// 终端交互环境：启动 Bubble Tea TUI
	m := initialMenuModel(opt)
	p := tea.NewProgram(m)
	finalM, err := p.Run()
	if err != nil {
		// Bubble Tea 启动失败，安全降级到文本模式
		return textMenu(opt, out)
	}

	res := finalM.(menuModel)
	if res.cancelled || res.chosen == nil {
		return nil, true
	}
	return res.chosen, false
}

func textMenu(opt MenuOpt, out io.Writer) (*MenuItem, bool) {
	if opt.Title != "" {
		fmt.Fprintln(out, opt.Title)
	}
	for _, it := range opt.Items {
		line := formatItemLine(it)
		fmt.Fprintln(out, "  "+line)
	}
	fmt.Fprintln(out)

	promptStr := opt.Prompt
	if promptStr == "" {
		promptStr = "请选择 (编号 / q 取消): "
	}
	fmt.Fprint(out, promptStr)

	in := opt.In
	if in == nil {
		in = os.Stdin
	}
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		fmt.Fprintln(out)
		return nil, true
	}
	raw := strings.TrimSpace(sc.Text())
	if raw == "" || strings.EqualFold(raw, "q") || strings.EqualFold(raw, "quit") || strings.EqualFold(raw, "esc") {
		return nil, true
	}

	// 1. 尝试匹配 Key
	for i := range opt.Items {
		if strings.EqualFold(opt.Items[i].Key, raw) {
			return &opt.Items[i], false
		}
	}

	// 2. 尝试解析为序号 (1-based)
	if idx, err := strconv.Atoi(raw); err == nil && idx >= 1 && idx <= len(opt.Items) {
		return &opt.Items[idx-1], false
	}

	// 3. 尝试匹配 Label
	for i := range opt.Items {
		if strings.EqualFold(opt.Items[i].Label, raw) {
			return &opt.Items[i], false
		}
	}

	return nil, true
}

func formatItemLine(it MenuItem) string {
	keyPart := fmt.Sprintf("%2s) ", it.Key)
	badgePart := ""
	if it.Badge != "" {
		badgePart = it.Badge + " "
	}
	detailPart := ""
	if it.Detail != "" {
		detailPart = " " + it.Detail
	}
	return keyPart + badgePart + it.Label + detailPart
}

type menuModel struct {
	title     string
	prompt    string
	items     []MenuItem
	cursor    int
	chosen    *MenuItem
	cancelled bool
	quitting  bool
}

func initialMenuModel(opt MenuOpt) menuModel {
	items := make([]MenuItem, len(opt.Items))
	copy(items, opt.Items)
	return menuModel{
		title:  opt.Title,
		prompt: opt.Prompt,
		items:  items,
		cursor: 0,
	}
}

func (m menuModel) Init() tea.Cmd {
	return nil
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q", "Q":
			m.cancelled = true
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(m.items) - 1
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			} else {
				m.cursor = 0
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.items) {
				m.chosen = &m.items[m.cursor]
			}
			m.quitting = true
			return m, tea.Quit
		default:
			k := msg.String()
			for i := range m.items {
				if strings.EqualFold(m.items[i].Key, k) {
					m.cursor = i
					m.chosen = &m.items[i]
					m.quitting = true
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m menuModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	if m.title != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.title) + "\n")
	}

	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")) // cyan
	normalStyle := lipgloss.NewStyle()
	badgeInStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))   // green
	badgeOutStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // orange/amber
	detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))   // dim

	for i, it := range m.items {
		isCursor := (i == m.cursor)
		cursor := "   "
		if isCursor {
			cursor = cursorStyle.Render(" > ")
		}

		keyPart := fmt.Sprintf("%2s) ", it.Key)
		if isCursor {
			keyPart = cursorStyle.Render(keyPart)
		} else {
			keyPart = normalStyle.Render(keyPart)
		}

		badgePart := ""
		if it.Badge != "" {
			if strings.Contains(it.Badge, "当前") {
				badgePart = badgeInStyle.Render(it.Badge) + " "
			} else {
				badgePart = badgeOutStyle.Render(it.Badge) + " "
			}
		}

		labelPart := it.Label
		if isCursor {
			labelPart = cursorStyle.Render(labelPart)
		}

		detailPart := ""
		if it.Detail != "" {
			detailPart = " " + detailStyle.Render(it.Detail)
		}

		b.WriteString(cursor + keyPart + badgePart + labelPart + detailPart + "\n")
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	b.WriteString("\n" + helpStyle.Render("  [↑/↓ 或 j/k] 移动光标  [编号/快捷键] 直接选择  [Enter] 确认  [q] 取消") + "\n")

	return b.String()
}

// ---------------------------------------------------------------- 复选菜单 (MultiMenu)

// MultiMenu 启动复选菜单，支持：
// 1. 方向键 (↑/↓/j/k) 移动光标、空格键翻转勾选、数字键直接翻转对应项勾选、a 全选/全不选、Enter 确认选择；
// 2. 管道模式/文本回退模式下，支持输入多个编号（空格或逗号分隔，如 1 2 3）或 a 全选。
func MultiMenu(opt MenuOpt) (selected []*MenuItem, cancelled bool) {
	out := opt.Out
	if out == nil {
		out = os.Stdout
	}

	// 管道模式、测试注入流或非交互终端：走标准文本多选菜单
	if !opt.Interactive || opt.In != nil {
		return textMultiMenu(opt, out)
	}

	// 终端交互环境：启动 Bubble Tea TUI
	m := initialMultiMenuModel(opt)
	p := tea.NewProgram(m)
	finalM, err := p.Run()
	if err != nil {
		return textMultiMenu(opt, out)
	}

	res := finalM.(multiMenuModel)
	if res.cancelled || !res.confirmed {
		return nil, true
	}

	var resList []*MenuItem
	for i := range res.items {
		if res.items[i].Selected {
			resList = append(resList, &res.items[i])
		}
	}
	return resList, false
}

func textMultiMenu(opt MenuOpt, out io.Writer) ([]*MenuItem, bool) {
	if opt.Title != "" {
		fmt.Fprintln(out, opt.Title)
	}
	for _, it := range opt.Items {
		mark := "[ ]"
		if it.Selected {
			mark = "[√]"
		}
		badgePart := ""
		if it.Badge != "" {
			badgePart = it.Badge + " "
		}
		detailPart := ""
		if it.Detail != "" {
			detailPart = " " + it.Detail
		}
		line := fmt.Sprintf("%2s) %s %s%s%s", it.Key, mark, badgePart, it.Label, detailPart)
		fmt.Fprintln(out, "  "+line)
	}
	fmt.Fprintln(out)

	promptStr := opt.Prompt
	if promptStr == "" {
		promptStr = "请选择编号 (支持多个用空格分隔，输入 a 全选，q 取消): "
	}
	fmt.Fprint(out, promptStr)

	in := opt.In
	if in == nil {
		in = os.Stdin
	}
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		fmt.Fprintln(out)
		return nil, true
	}
	raw := strings.TrimSpace(sc.Text())
	if raw == "" || strings.EqualFold(raw, "q") || strings.EqualFold(raw, "quit") || strings.EqualFold(raw, "esc") {
		return nil, true
	}

	// 全选支持
	if strings.EqualFold(raw, "a") || strings.EqualFold(raw, "all") || raw == "*" {
		res := make([]*MenuItem, len(opt.Items))
		for i := range opt.Items {
			res[i] = &opt.Items[i]
		}
		return res, false
	}

	// 解析逗号或空格分隔的 token
	cleaner := strings.ReplaceAll(raw, ",", " ")
	tokens := strings.Fields(cleaner)
	if len(tokens) == 0 {
		return nil, true
	}

	pickedMap := make(map[int]bool)
	for _, tok := range tokens {
		if idx, err := strconv.Atoi(tok); err == nil && idx >= 1 && idx <= len(opt.Items) {
			pickedMap[idx-1] = true
			continue
		}
		matched := false
		for i := range opt.Items {
			if strings.EqualFold(opt.Items[i].Key, tok) {
				pickedMap[i] = true
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for i := range opt.Items {
			if strings.EqualFold(opt.Items[i].Label, tok) {
				pickedMap[i] = true
				break
			}
		}
	}

	if len(pickedMap) == 0 {
		return nil, true
	}

	var res []*MenuItem
	for i := range opt.Items {
		if pickedMap[i] {
			res = append(res, &opt.Items[i])
		}
	}
	return res, false
}

type multiMenuModel struct {
	title     string
	prompt    string
	items     []MenuItem
	cursor    int
	confirmed bool
	cancelled bool
	quitting  bool
}

func initialMultiMenuModel(opt MenuOpt) multiMenuModel {
	items := make([]MenuItem, len(opt.Items))
	copy(items, opt.Items)
	return multiMenuModel{
		title:  opt.Title,
		prompt: opt.Prompt,
		items:  items,
		cursor: 0,
	}
}

func (m multiMenuModel) Init() tea.Cmd {
	return nil
}

func (m multiMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q", "Q":
			m.cancelled = true
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(m.items) - 1
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			} else {
				m.cursor = 0
			}
		case " ", "x":
			if m.cursor >= 0 && m.cursor < len(m.items) {
				m.items[m.cursor].Selected = !m.items[m.cursor].Selected
			}
		case "a", "A":
			allSelected := true
			for _, it := range m.items {
				if !it.Selected {
					allSelected = false
					break
				}
			}
			for i := range m.items {
				m.items[i].Selected = !allSelected
			}
		case "enter":
			// 若用户未显式空格勾选，但直接在某项上按 Enter，默认勾选当前光标项
			hasSelected := false
			for _, it := range m.items {
				if it.Selected {
					hasSelected = true
					break
				}
			}
			if !hasSelected && m.cursor >= 0 && m.cursor < len(m.items) {
				m.items[m.cursor].Selected = true
			}
			m.confirmed = true
			m.quitting = true
			return m, tea.Quit
		default:
			k := msg.String()
			for i := range m.items {
				if strings.EqualFold(m.items[i].Key, k) {
					m.items[i].Selected = !m.items[i].Selected
					m.cursor = i
					break
				}
			}
		}
	}
	return m, nil
}

func (m multiMenuModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	if m.title != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.title) + "\n")
	}

	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")) // cyan
	normalStyle := lipgloss.NewStyle()
	badgeInStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))            // green
	badgeOutStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))          // orange/amber
	detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))            // dim
	checkOnStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")) // [√] green
	checkOffStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))          // [ ] dim

	selectedCount := 0
	for i, it := range m.items {
		if it.Selected {
			selectedCount++
		}
		isCursor := (i == m.cursor)
		cursor := "   "
		if isCursor {
			cursor = cursorStyle.Render(" > ")
		}

		keyPart := fmt.Sprintf("%2s) ", it.Key)
		if isCursor {
			keyPart = cursorStyle.Render(keyPart)
		} else {
			keyPart = normalStyle.Render(keyPart)
		}

		checkPart := checkOffStyle.Render("[ ] ")
		if it.Selected {
			checkPart = checkOnStyle.Render("[√] ")
		}

		badgePart := ""
		if it.Badge != "" {
			if strings.Contains(it.Badge, "当前") {
				badgePart = badgeInStyle.Render(it.Badge) + " "
			} else {
				badgePart = badgeOutStyle.Render(it.Badge) + " "
			}
		}

		labelPart := it.Label
		if isCursor {
			labelPart = cursorStyle.Render(labelPart)
		}

		detailPart := ""
		if it.Detail != "" {
			detailPart = " " + detailStyle.Render(it.Detail)
		}

		b.WriteString(cursor + keyPart + checkPart + badgePart + labelPart + detailPart + "\n")
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	helpText := fmt.Sprintf("  [↑/↓ 或 j/k] 移动  [空格/编号] 勾选/取消  [a] 全选  [Enter] 确认 (%d 个已选)  [q] 取消", selectedCount)
	b.WriteString("\n" + helpStyle.Render(helpText) + "\n")

	return b.String()
}
