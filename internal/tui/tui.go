package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"grscan/internal/render"
	"grscan/internal/state"
	"grscan/internal/util"
)

type scope int

const (
	scopePending scope = iota
	scopeAll
	scopeIgnored
)

func (s scope) String() string {
	switch s {
	case scopeAll:
		return "全部"
	case scopeIgnored:
		return "已忽略"
	default:
		return "待处理"
	}
}

type inputKind int

const (
	inNone inputKind = iota
	inGroup
	inTag
	inNote
	inFilter
)

func (k inputKind) prompt() string {
	switch k {
	case inGroup:
		return "分组名"
	case inTag:
		return "标签（逗号分隔）"
	case inNote:
		return "备注"
	case inFilter:
		return "过滤路径/名字"
	default:
		return ""
	}
}

// foldGroup 一个被折叠起来的目录：只占一行，操作会作用于组内全部仓库。
type foldGroup struct {
	dir   string
	repos []*state.Repo
}

type row struct {
	r    *state.Repo
	fold *foldGroup
	sel  bool
}

// OpenTig 把光标下的仓库交给 tig：返回一句回显，或一条错误。
// 由调用方注入，面板本身不知道 tig 装在哪、终端怎么起。
type OpenTig func(r *state.Repo) (string, error)

type model struct {
	st    *state.State
	roots []string
	save  func() error
	// openTig 为 nil 时按 g 会提示没接上 —— 面板不该假装能用。
	openTig OpenTig

	// subset 限定这次面板能碰到的仓库（按 Repo.ID 索引）。nil = 全集。
	// 绝不为限定范围去改 st.Repos —— 那会污染状态并被 save() 写回磁盘。
	subset map[string]bool
	// scopeLabel 当前作用域的名字，钉在标题栏，让人随时知道自己在哪一层。
	scopeLabel string

	// 会话内的临时展开/折叠，不写盘
	expanded map[string]bool
	tempFold map[string]bool

	rows    []row
	cursor  int
	offset  int
	width   int
	height  int
	sc      scope
	query   string
	mode    inputKind
	buf     string
	msg     string
	changed bool
	help    bool
}

// Run 启动交互面板。statusPreset 决定初始视图。
// subset 为空表示全集；非空时面板只在Scope这批仓库里工作，但读写的仍是 st 里的同一批对象，
// 所以标注结果照常落盘，只是别的仓库不出现在屏幕上。
func Run(st *state.State, roots []string, query, statusPreset string, subset []*state.Repo, scopeLabel string, save func() error, openTig OpenTig) error {
	m := newModel(st, roots, query, statusPreset, subset, scopeLabel, save)
	m.openTig = openTig
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

func newModel(st *state.State, roots []string, query, statusPreset string, subset []*state.Repo, scopeLabel string, save func() error) model {
	m := model{
		st: st, roots: roots, save: save, query: strings.ToLower(query),
		width: 120, height: 30,
		expanded:   map[string]bool{},
		tempFold:   map[string]bool{},
		scopeLabel: scopeLabel,
	}
	if len(subset) > 0 {
		m.subset = make(map[string]bool, len(subset))
		for _, r := range subset {
			m.subset[r.ID] = true
		}
	}
	switch statusPreset {
	case "ignored":
		m.sc = scopeIgnored
	case state.StatusDone, state.StatusArchived, state.StatusActive, "all":
		m.sc = scopeAll
	default:
		m.sc = scopePending
	}
	m.refresh()
	// 进面板第一句话就把最常用的动作交代清楚；
	// 任何一次操作都会覆盖它，不会长期占着屏幕
	if len(m.rows) == 0 {
		m.msg = "（当前视图没有仓库，按 v 切换视图）"
	} else {
		m.msg = "没勾选时操作只作用于当前行 · 空格可勾选多个一起改 · ? 看全部快捷键"
	}
	return m
}

// ---------------------------------------------------------------- 数据

func (m *model) inScope(r *state.Repo) bool {
	// 被 hide 的目录，任何视图都不出现
	if m.st.IsHidden(r.Path) {
		return false
	}
	// 限定了作用域就不越界：这次只处理这一批，别的仓库当不存在
	if m.subset != nil && !m.subset[r.ID] {
		return false
	}
	switch m.sc {
	case scopeAll:
		return !r.Ignored
	case scopeIgnored:
		return r.Ignored
	default:
		return !r.Ignored && (r.Status == "" || r.Status == state.StatusNew) && !r.Missing
	}
}

// foldedPaths 当前生效的折叠目录：持久规则 + 本次会话临时折叠 - 临时展开。
func (m *model) foldedPaths() []string {
	out := []string{}
	for _, p := range m.st.FoldedPaths() {
		if !m.expanded[p] {
			out = append(out, p)
		}
	}
	for p := range m.tempFold {
		if !m.expanded[p] {
			out = append(out, p)
		}
	}
	return out
}

// rowKey 行的稳定标识：仓库用 ID，折叠组用目录路径。
func rowKey(rw row) string {
	if rw.fold != nil {
		return "dir:" + rw.fold.dir
	}
	return rw.r.ID
}

// keyTime 参与排序的时间：折叠组取组内最近一次提交。
func keyTime(rw row) time.Time {
	if rw.fold == nil {
		return render.CommitTime(rw.r)
	}
	latest := time.Time{}
	for _, r := range rw.fold.repos {
		if t := render.CommitTime(r); t.After(latest) {
			latest = t
		}
	}
	return latest
}

func (m *model) refresh() {
	sel := map[string]bool{}
	for _, rw := range m.rows {
		if rw.sel {
			sel[rowKey(rw)] = true
		}
	}
	curKey := ""
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		curKey = rowKey(m.rows[m.cursor])
	}

	folded := m.foldedPaths()
	rows := []row{}
	at := map[string]int{}
	for _, r := range m.st.Repos {
		if !m.inScope(r) {
			continue
		}
		if m.query != "" {
			hay := strings.ToLower(r.Path + " " + r.Name + " " + r.Group + " " + strings.Join(r.Tags, " "))
			if !strings.Contains(hay, m.query) {
				continue
			}
		}
		if dir := util.HasAnyPrefixDir(r.Path, folded); dir != "" {
			if i, ok := at[dir]; ok {
				rows[i].fold.repos = append(rows[i].fold.repos, r)
				continue
			}
			at[dir] = len(rows)
			rows = append(rows, row{fold: &foldGroup{dir: dir, repos: []*state.Repo{r}}, sel: sel["dir:"+dir]})
			continue
		}
		rows = append(rows, row{r: r, sel: sel[r.ID]})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		mi := rows[i].fold != nil && isAllMissing(rows[i].fold)
		mj := rows[j].fold != nil && isAllMissing(rows[j].fold)
		if mi != mj {
			return mj // 已删除的沉到最后
		}
		if rows[i].fold == nil && rows[j].fold == nil {
			if rows[i].r.Missing != rows[j].r.Missing {
				return rows[j].r.Missing
			}
		}
		ta, tb := keyTime(rows[i]), keyTime(rows[j])
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		return rowKey(rows[i]) < rowKey(rows[j])
	})
	m.rows = rows

	// 尽量保持原来的光标位置
	if curKey != "" {
		for i, rw := range m.rows {
			if rowKey(rw) == curKey {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
}

func isAllMissing(g *foldGroup) bool {
	for _, r := range g.repos {
		if !r.Missing {
			return false
		}
	}
	return true
}

func (m *model) clamp() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	visible := m.visibleRows()
	if visible < 1 {
		visible = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
}

func (m *model) targets() []*state.Repo {
	var out []*state.Repo
	for _, rw := range m.rows {
		if !rw.sel {
			continue
		}
		// 勾选一个折叠目录 = 勾选它底下所有仓库
		if rw.fold != nil {
			out = append(out, rw.fold.repos...)
			continue
		}
		out = append(out, rw.r)
	}
	if len(out) == 0 && len(m.rows) > 0 {
		cur := m.rows[m.cursor]
		if cur.fold != nil {
			out = append(out, cur.fold.repos...)
		} else {
			out = append(out, cur.r)
		}
	}
	return out
}

func (m *model) selectedCount() int {
	n := 0
	for _, rw := range m.rows {
		if rw.sel {
			n++
		}
	}
	return n
}

func (m *model) apply(fn func(r *state.Repo)) {
	list := m.targets()
	if len(list) == 0 {
		return
	}
	now := time.Now().Format(time.RFC3339)
	for _, r := range list {
		fn(r)
		r.ReviewedAt = now
	}
	m.changed = true
	m.msg = fmt.Sprintf("已更新 %d 个仓库", len(list))
	for i := range m.rows {
		m.rows[i].sel = false
	}
	m.refresh()
}

func (m *model) doSave() {
	if err := m.save(); err != nil {
		m.msg = "保存失败: " + err.Error()
		return
	}
	m.changed = false
	m.msg = "已写入 " + m.st.Path()
}

// ---------------------------------------------------------------- bubbletea

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch theme := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = theme.Width, theme.Height
		m.clamp()
		return m, nil
	case tea.KeyMsg:
		return m.onKey(theme)
	}
	return m, nil
}

func (m model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 输入模式优先
	if m.mode != inNone {
		switch k.Type {
		case tea.KeyEsc:
			m.mode, m.buf = inNone, ""
			return m, nil
		case tea.KeyEnter:
			return m.commitInput()
		case tea.KeyBackspace:
			if len(m.buf) > 0 {
				runes := []rune(m.buf)
				m.buf = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyRunes:
			m.buf += string(k.Runes)
			return m, nil
		case tea.KeySpace:
			m.buf += " "
			return m, nil
		case tea.KeyCtrlU:
			m.buf = ""
			return m, nil
		}
		return m, nil
	}

	switch {
	case k.Type == tea.KeyCtrlC:
		return m, tea.Quit
	case k.Type == tea.KeyCtrlS:
		m.doSave()
		return m, nil
	case k.Type == tea.KeyEsc:
		for i := range m.rows {
			m.rows[i].sel = false
		}
		m.msg = "已清空选择"
		return m, nil
	case k.Type == tea.KeyUp, k.String() == "k":
		m.cursor--
		m.clamp()
		return m, nil
	case k.Type == tea.KeyDown, k.String() == "j":
		m.cursor++
		m.clamp()
		return m, nil
	case k.Type == tea.KeyPgUp:
		m.cursor -= m.visibleRows()
		m.clamp()
		return m, nil
	case k.Type == tea.KeyPgDown:
		m.cursor += m.visibleRows()
		m.clamp()
		return m, nil
	case k.Type == tea.KeyHome:
		m.cursor = 0
		m.clamp()
		return m, nil
	case k.Type == tea.KeyEnd:
		m.cursor = len(m.rows) - 1
		m.clamp()
		return m, nil
	}

	switch k.String() {
	case "q":
		if m.changed {
			m.doSave()
		}
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case " ":
		if len(m.rows) > 0 {
			m.rows[m.cursor].sel = !m.rows[m.cursor].sel
			m.cursor++
			m.clamp()
		}
		return m, nil
	case "A":
		for i := range m.rows {
			m.rows[i].sel = true
		}
		m.msg = fmt.Sprintf("已全选 %d 个", len(m.rows))
		return m, nil
	case "a":
		for i := range m.rows {
			m.rows[i].sel = false
		}
		m.msg = "已取消选择"
		return m, nil
	case "G":
		m.mode, m.buf = inGroup, ""
		return m, nil
	case "T", "t":
		m.mode, m.buf = inTag, ""
		return m, nil
	case "N", "n":
		m.mode, m.buf = inNote, ""
		return m, nil
	case "F", "/":
		m.mode, m.buf = inFilter, m.query
		return m, nil
	case "c":
		m.query = ""
		m.mode = inNone
		m.msg = "已清除过滤"
		m.refresh()
		return m, nil
	case "S":
		m.apply(func(r *state.Repo) { r.Status = state.NextStatus(r.Status) })
		return m, nil
	case "d":
		m.apply(func(r *state.Repo) { r.Status = state.StatusDone })
		return m, nil
	case "w":
		m.apply(func(r *state.Repo) { r.Status = state.StatusActive })
		return m, nil
	case "z":
		m.apply(func(r *state.Repo) { r.Status = state.StatusArchived })
		return m, nil
	case "u":
		m.apply(func(r *state.Repo) {
			r.Status = state.StatusNew
			r.Ignored = false
		})
		return m, nil
	case "I", "i":
		m.apply(func(r *state.Repo) {
			r.Ignored = !r.Ignored
			if r.Ignored {
				m.msg = "已忽略，后续扫描不再打扰"
			}
		})
		return m, nil
	case "x":
		m.apply(func(r *state.Repo) { r.Group = "" })
		return m, nil
	case "v":
		m.sc = scope((int(m.sc) + 1) % 3)
		m.cursor, m.offset = 0, 0
		m.refresh()
		m.msg = "视图: " + m.sc.String()
		return m, nil
	case "o":
		if cur := m.currentRow(); cur != nil && cur.fold != nil {
			openInExplorer(cur.fold.dir)
			m.msg = "已打开 " + cur.fold.dir
		} else if cur != nil {
			openInExplorer(cur.r.Path)
			m.msg = "已打开 " + cur.r.Path
		}
		return m, nil
	case "p":
		if cur := m.currentRow(); cur != nil && cur.fold != nil {
			m.msg = fmt.Sprintf("%s  （%d 个仓库）", cur.fold.dir, len(cur.fold.repos))
		} else if cur != nil {
			m.msg = cur.r.Path
		}
		return m, nil
	case "r":
		if cur := m.currentRow(); cur != nil && cur.fold != nil {
			m.msg = foldDetail(cur.fold)
		} else if cur != nil {
			m.msg = detailLine(cur.r)
		}
		return m, nil
	case "e":
		return m.toggleFold()
	case "g":
		return m.openInTig()
	}
	return m, nil
}

// openInTig 把光标下的仓库丢给 tig，只作用于光标所在行。
//
// 勾选多行时不批量开：一次弹五个 tig 窗口只会更乱，而且看不完还得一个个关。
// 真有勾选就把这句话说出来，免得你以为"勾了 5 个所以会开 5 个"。
func (m model) openInTig() (tea.Model, tea.Cmd) {
	cur := m.currentRow()
	if cur == nil {
		return m, nil
	}
	if cur.fold != nil {
		m.msg = "折叠起来的是一个目录，不是仓库 · e 展开后选中具体那个"
		return m, nil
	}
	if m.openTig == nil {
		m.msg = "没接上 tig · 退出后用 grscan tig <关键字> 看是什么挡住了"
		return m, nil
	}
	info, err := m.openTig(cur.r)
	if err != nil {
		m.msg = err.Error()
		return m, nil
	}
	m.msg = fmt.Sprintf("已打开 %s · %s", cur.r.Name, info)
	if n := m.selectedCount(); n > 0 {
		m.msg += fmt.Sprintf(" （g 只看光标这一行，不是已勾选的 %d 项）", n)
	}
	return m, nil
}

// toggleFold 在折叠行上按 e 展开，在普通行上按 e 折叠它所在的目录。
// 都是会话内的临时动作，不写盘——要持久用 grscan fold / unfold。
func (m model) toggleFold() (tea.Model, tea.Cmd) {
	cur := m.currentRow()
	if cur == nil {
		return m, nil
	}
	if cur.fold != nil {
		dir := cur.fold.dir
		m.expanded[dir] = true
		delete(m.tempFold, dir)
		m.refresh()
		m.msg = "已临时展开 " + dir
		return m, nil
	}
	dir := filepath.Dir(cur.r.Path)
	if m.st.IsFolded(dir) != "" {
		// 目录本身有持久折叠规则，临时展开它
		m.expanded[dir] = true
		m.refresh()
		m.msg = "已临时展开 " + dir
		return m, nil
	}
	m.tempFold[dir] = true
	delete(m.expanded, dir)
	m.refresh()
	m.msg = fmt.Sprintf("已临时折叠 %s（%d 个仓库）", dir, countUnder(m.st, dir))
	return m, nil
}

func countUnder(st *state.State, dir string) int {
	n := 0
	for _, r := range st.Repos {
		if util.HasPrefixPath(r.Path, dir) {
			n++
		}
	}
	return n
}

func (m model) commitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.buf)
	switch m.mode {
	case inFilter:
		m.query = strings.ToLower(text)
		m.cursor, m.offset = 0, 0
		m.refresh()
		m.msg = "过滤: " + text
	case inGroup:
		if text != "" {
			m.apply(func(r *state.Repo) { r.Group = text })
		}
	case inTag:
		if text != "" {
			tags := splitList(text)
			m.apply(func(r *state.Repo) {
				for _, t := range tags {
					dup := false
					for _, ex := range r.Tags {
						if strings.EqualFold(ex, t) {
							dup = true
						}
					}
					if !dup {
						r.Tags = append(r.Tags, t)
					}
				}
			})
		}
	case inNote:
		if text != "" {
			note := text
			m.apply(func(r *state.Repo) { r.Note = note })
		}
	}
	m.mode, m.buf = inNone, ""
	return m, nil
}

// ---------------------------------------------------------------- 渲染

func (m model) visibleRows() int {
	// 标题 + 列头 + 列表 + 空行 + 详情行 + 键位条 + 消息行
	h := m.height - 9
	if h < 3 {
		h = 3
	}
	return h
}

var (
	stTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	stCursor  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	stSelMark = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	stName    = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	stBranch  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	stWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	stOk      = lipgloss.NewStyle().Foreground(lipgloss.Color("41"))
	stDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	stMsg     = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	stInput   = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	stKey     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("222"))
	stKeySep  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	stKeyHead = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226"))
)

// tailTrunc 从左侧截断：路径的尾巴信息量最大，D:\work\a\b\opensource 要留住 opensource。
func tailTrunc(s string, w int) string {
	if w <= 1 || util.DisplayWidth(s) <= w {
		return s
	}
	runes := []rune(s)
	out := []rune{}
	width := 1 // 开头的省略号占一位
	for i := len(runes) - 1; i >= 0; i-- {
		cw := util.DisplayWidth(string(runes[i]))
		if width+cw > w {
			break
		}
		width += cw
		out = append([]rune{runes[i]}, out...)
	}
	return "…" + string(out)
}

func (m model) View() string {
	if m.help {
		return m.helpView()
	}
	var b strings.Builder

	verb := "grscan triage"
	if m.scopeLabel != "" {
		verb += " · " + tailTrunc(m.scopeLabel, maxInt(18, m.width-50))
	}
	head := fmt.Sprintf(" %s   视图[%s]  共 %d  已选 %d  %s",
		verb, m.sc.String(), len(m.rows), m.selectedCount(), dirtyMark(m.changed))
	b.WriteString(stTitle.Render(head) + "\n")
	b.WriteString(stDim.Render(m.columnHeader()) + "\n")

	visible := m.visibleRows()
	end := m.offset + visible
	if end > len(m.rows) {
		end = len(m.rows)
	}
	if len(m.rows) == 0 {
		b.WriteString("\n   " + stDim.Render("（列表为空）") + "\n")
	} else {
		for i := m.offset; i < end; i++ {
			b.WriteString(m.rowLine(i) + "\n")
		}
	}

	b.WriteString("\n")
	if m.mode != inNone {
		hint := existingHint(m.st, m.mode)
		b.WriteString(stInput.Render(" "+m.mode.prompt()+": "+m.buf+"_") + "\n")
		if hint != "" {
			b.WriteString(stDim.Render("   "+hint) + "\n")
		} else {
			b.WriteString(stDim.Render("   Enter 确认 · Esc 取消 —— 此刻字母键都是输入内容") + "\n")
		}
	} else if cur := m.currentRow(); cur != nil && cur.fold != nil {
		b.WriteString(stDim.Render("   "+util.Truncate(foldDetail(cur.fold)+"   e 展开", m.width-4)) + "\n")
	} else if cur != nil {
		b.WriteString(stDim.Render("   "+util.Truncate(detailLine(cur.r), m.width-4)) + "\n")
	} else {
		b.WriteString(stDim.Render("   "+emptyHint(m)) + "\n")
	}
	// 键位条常驻：有行没行、有没有勾选都在，不用去翻帮助页
	b.WriteString(m.hintBar() + "\n")
	if m.msg != "" {
		b.WriteString(stMsg.Render("   "+util.Truncate(m.msg, m.width-4)) + "\n")
	}
	return b.String()
}

// keyHint 底部键位条里的一项。
type keyHint struct {
	key   string
	label string
}

// emptyHint 列表为空时说明"为什么空"以及"下一步按什么"。
func emptyHint(m model) string {
	switch {
	case m.query != "":
		return "没有匹配「" + m.query + "」的仓库 · c 清除过滤 · v 切换视图"
	case m.sc == scopePending:
		return "待处理已经清空 · v 切到「全部」查看已处理的仓库"
	case m.sc == scopeIgnored:
		return "还没有忽略任何仓库 · v 切回「待处理」"
	default:
		return "这个视图暂时没有仓库 · v 切换视图 · F 按路径过滤"
	}
}

// hintBar 常驻底部的一行按键提示。宽度不够就自动丢掉靠后的键，
// 保证不被撑出换行把列表挤乱。
// 一旦有勾选，整条换成"作用于已选项"——这是面板里最容易误解的一条规则。
func (m model) hintBar() string {
	limit := m.width - 6
	if limit < 24 {
		limit = 24
	}
	if n := m.selectedCount(); n > 0 {
		head := fmt.Sprintf(" 已选 %d 项 →", n)
		// 越靠前越不会被窄屏丢掉：先给最常用和最容易找不到出口的键
		return stKeyHead.Render(head) + joinHints([]keyHint{
			{"d", "已处理"}, {"Esc", "取消选择"}, {"G", "分组"}, {"w", "跟进"},
			{"z", "归档"}, {"u", "退回"}, {"T", "标签"}, {"I", "忽略"},
		}, limit-displayWidth(head))
	}
	// 空列表时给的是脱身动作，不是处理动作
	if len(m.rows) == 0 {
		return joinHints([]keyHint{
			{"v", "切换视图"}, {"c", "清除过滤"}, {"?", "帮助"}, {"q", "退出"},
		}, limit)
	}
	return joinHints([]keyHint{
		{"↑↓", "移动"}, {"空格", "勾选"}, {"d", "已处理"}, {"g", "tig"},
		{"q", "退出保存"}, {"?", "帮助"}, {"G", "分组"}, {"v", "视图"}, {"F", "过滤"}, {"o", "打开"},
	}, limit)
}

// joinHints 把若干按键提示拼成一行，超宽的项直接丢弃。
func joinHints(ps []keyHint, limit int) string {
	out := ""
	used := 0
	for i, p := range ps {
		step := displayWidth(p.key) + displayWidth(p.label) + 2
		if i > 0 {
			step += 3 // " · "
		}
		if used+step > limit {
			break
		}
		if i > 0 {
			out += stKeySep.Render(" · ")
		}
		out += stKey.Render(" "+p.key) + stDim.Render(" "+p.label)
		used += step
	}
	return out
}

// displayWidth 按终端列宽算：中日韩字符占两列。
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if r > 0x2E7F {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func dirtyMark(changed bool) string {
	if changed {
		return stWarn.Render("● 未保存 (Ctrl+S 保存)")
	}
	return stOk.Render("✓ 已同步")
}

func (m model) currentRepo() *state.Repo {
	if cur := m.currentRow(); cur != nil {
		return cur.r
	}
	return nil
}

func (m model) currentRow() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return &m.rows[m.cursor]
	}
	return nil
}

func foldDetail(g *foldGroup) string {
	dirty, pending := 0, 0
	for _, r := range g.repos {
		if r.Dirty {
			dirty++
		}
		if !r.Ignored && (r.Status == "" || r.Status == state.StatusNew) {
			pending++
		}
	}
	out := fmt.Sprintf("%s  · %d 个仓库", g.Dir(), len(g.repos))
	if dirty > 0 {
		out += fmt.Sprintf(" · %d 有改动", dirty)
	}
	out += fmt.Sprintf(" · %d 待处理", pending)
	return out
}

func (g *foldGroup) Dir() string { return g.dir }

// foldLine 折叠目录只占一行：目录名 + 数量 + 有改动/待处理数。
func (m model) foldLine(i int) string {
	rw := m.rows[i]
	g := rw.fold

	selMark := "   "
	if rw.sel {
		selMark = stSelMark.Render("[x]")
	}
	curMark := "  "
	if i == m.cursor {
		curMark = stCursor.Render("> ")
	}

	dirty, pending := 0, 0
	for _, r := range g.repos {
		if r.Dirty {
			dirty++
		}
		if !r.Ignored && (r.Status == "" || r.Status == state.StatusNew) {
			pending++
		}
	}
	summary := g.dir
	if !strings.HasSuffix(summary, `\`) && !strings.HasSuffix(summary, "/") {
		summary += string(filepath.Separator)
	}
	summary += fmt.Sprintf("  · %d 个仓库", len(g.repos))
	if dirty > 0 {
		summary += fmt.Sprintf(" · %d 有改动", dirty)
	}
	if pending > 0 {
		summary += fmt.Sprintf(" · %d 待处理", pending)
	}
	line := selMark + curMark + stCursor.Render(util.PadRight("[已折叠]", 26)) + stDim.Render(summary)
	return line
}

func (m model) columnHeader() string {
	return util.PadRight("", 3) +
		util.PadRight("NAME", 26) +
		util.PadRight("BRANCH", 20) +
		util.PadRight("CHG", 7) +
		util.PadRight("SYNC", 8) +
		util.PadRight("LAST", 10) +
		util.PadRight("GROUP", 14) +
		"PATH"
}

func (m model) rowLine(i int) string {
	rw := m.rows[i]
	if rw.fold != nil {
		return m.foldLine(i)
	}
	r := rw.r

	selMark := "   "
	if rw.sel {
		selMark = "[x]"
	}
	curMark := "  "
	if i == m.cursor {
		curMark = "> "
	}

	name := util.Truncate(r.Name, 26)
	branch := util.Truncate(renderBranch(r), 20)
	chg := render.ChangeLabel(r)
	sync := render.SyncLabel(r)
	last := render.CommitAge(r)
	group := r.Group
	if group == "" {
		group = "-"
	}
	group = util.Truncate(group, 14)

	// 先补空格再上色，避免 ANSI 转义序列被算进宽度
	cells := []string{
		util.PadRight(name, 26),
		util.PadRight(branch, 20),
		util.PadRight(chg, 7),
		util.PadRight(sync, 8),
		util.PadRight(last, 10),
		util.PadRight(group, 14),
	}
	prefix := selMark + curMark
	if i == m.cursor {
		cells[0] = stName.Render(cells[0])
		cells[1] = stBranch.Render(cells[1])
		prefix = stSelMark.Render(selMark) + stCursor.Render(curMark)
	}
	cells[2] = colorChg(cells[2])

	path := util.Truncate(render.ShortPath(r.Path, m.roots), maxInt(24, m.width-104))
	line := prefix + strings.Join(cells, "") + stDim.Render(path)
	if r.Missing {
		line = stDim.Render(line)
	}
	return line
}

func colorChg(s string) string {
	if s == "-" {
		return stOk.Render(s)
	}
	return stWarn.Render(s)
}

func renderBranch(r *state.Repo) string {
	b := r.Branch
	if b == "" {
		return "-"
	}
	if r.Detached {
		b += "(分离)"
	}
	if r.Dirty {
		b += " *"
	}
	return b
}

func detailLine(r *state.Repo) string {
	out := r.Path
	if r.PrimaryURL != "" {
		out += "  |  " + r.PrimaryURL
	}
	if r.Subject != "" {
		out += "  |  " + strings.TrimSpace(r.Author+" "+r.Subject)
	}
	extra := []string{render.StatusCN(r)}
	if r.Group != "" {
		extra = append(extra, "组:"+r.Group)
	}
	if len(r.Tags) > 0 {
		extra = append(extra, "标签:"+strings.Join(r.Tags, ","))
	}
	if r.Note != "" {
		extra = append(extra, "备注:"+r.Note)
	}
	if r.ScanIssue != "" {
		extra = append(extra, "扫描告警:"+r.ScanIssue)
	}
	return out + "  |  " + strings.Join(extra, " / ")
}

func existingHint(st *state.State, k inputKind) string {
	switch k {
	case inGroup:
		seen := map[string]bool{}
		var names []string
		for _, r := range st.Repos {
			if r.Group != "" && !seen[r.Group] {
				seen[r.Group] = true
				names = append(names, r.Group)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ""
		}
		if len(names) > 8 {
			names = names[:8]
		}
		return "已有分组: " + strings.Join(names, " / ")
	case inTag:
		seen := map[string]bool{}
		var names []string
		for _, r := range st.Repos {
			for _, t := range r.Tags {
				if !seen[t] {
					seen[t] = true
					names = append(names, t)
				}
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ""
		}
		if len(names) > 8 {
			names = names[:8]
		}
		return "已有标签: " + strings.Join(names, " / ")
	}
	return ""
}

func (m model) helpView() string {
	return "\n grscan triage 快捷键\n\n" +
		"  对象    有勾选的行 = 作用于全部勾选行；没勾选 = 只作用于光标所在行\n" +
		"  移动    ↑/k  ↓/j   PgUp/PgDn   Home/End\n" +
		"  多选   空格 选择/取消        A 全选     a 取消选择   Esc 清空选择\n" +
		"  处理    S 循环状态           d 已处理   w 跟进中   z 已归档   u 退回待处理\n" +
		"  归类    G 设置分组           T 加标签   N 写备注   x 清除分组\n" +
		"  忽略    I 忽略/取消忽略（忽略后下次 scan 会在父目录整棵跳过）\n" +
		"  折叠    e 临时折叠/展开光标所在目录（仅本次会话，要持久用 grscan fold）\n" +
		"  视图    v 待处理/全部/已忽略  F 过滤路径 c 清除过滤\n" +
		"  看进去  g 用 tig 打开（新窗口，按这个仓库的状态选开屏位置）\n" +
		"  其它    o 资源管理器打开   r 查看详情   p 显示完整路径\n" +
		"  保存    Ctrl+S 立即保存      q 退出并保存        Ctrl+C 放弃改动退出\n\n" +
		"  提示:  按 d 的行会直接从“待处理”视图消失，适合从头往下快速清场。\n" +
		"         G / T / N / F 会进入输入状态，Enter 确认、Esc 取消。\n" +
		"         底部始终显示当前可用的按键，不用记这张表。\n\n" +
		"  按 ? 返回\n"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func openInExplorer(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
