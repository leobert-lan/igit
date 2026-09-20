package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"grscan/internal/state"
)

// mkRepo 按完整路径造一个仓库记录。
func mkRepo(p string) *state.Repo {
	r := &state.Repo{Path: p, Name: filepath.Base(p), Kind: "repo", Branch: "main"}
	state.Canonicalize(r)
	r.Status = state.StatusNew
	return r
}

func demoState() *state.State {
	mk := func(name, status string, ignored bool) *state.Repo {
		r := &state.Repo{Path: "C:/repos/" + name, Name: name, Kind: "repo", Branch: "main"}
		state.Canonicalize(r)
		r.Status = status
		r.Ignored = ignored
		return r
	}
	return &state.State{Repos: []*state.Repo{
		mk("alpha", state.StatusNew, false),
		mk("beta", state.StatusNew, false),
		mk("gamma", state.StatusDone, false),
		mk("delta", state.StatusNew, true),
	}}
}

// key 用一条按键消息推进模型，返回更新后的模型值。
func key(m model, k tea.KeyMsg) model {
	nm, _ := m.Update(k)
	return nm.(model)
}

func runeKey(m model, r rune) model {
	return key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func typeText(m model, s string) model {
	for _, r := range []rune(s) {
		m = runeKey(m, r)
	}
	return m
}

// 默认视图只显示待处理，不含已处理与已忽略
func TestDefaultScopeExcludesDoneAndIgnored(t *testing.T) {
	m := newModel(demoState(), []string{"C:/repos"}, "", state.StatusNew, func() error { return nil })
	if len(m.rows) != 2 {
		t.Fatalf("待处理视图应有 2 行，实际 %d", len(m.rows))
	}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(model)
	view := m.View()
	if !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") {
		t.Fatalf("视图缺少仓库名:\n%s", view)
	}
	if strings.Contains(view, "gamma") {
		t.Fatalf("已处理的不应出现在待处理视图:\n%s", view)
	}
}

// v 键循环切换视图
func TestScopeCycle(t *testing.T) {
	m := newModel(demoState(), []string{"C:/repos"}, "", "", func() error { return nil })
	m = runeKey(m, 'v')
	if m.sc != scopeAll {
		t.Fatalf("第一次 v 应切到全部，实际 %v", m.sc)
	}
	if len(m.rows) != 3 {
		t.Fatalf("全部视图应含 3 条（排除已忽略），实际 %d", len(m.rows))
	}
	m = runeKey(m, 'v')
	if m.sc != scopeIgnored {
		t.Fatalf("第二次 v 应切到已忽略，实际 %v", m.sc)
	}
	if len(m.rows) != 1 {
		t.Fatalf("已忽略视图应有 1 条，实际 %d", len(m.rows))
	}
}

// G 输入分组 → 生效；d 标记已处理 → 从待处理视图消失
func TestGroupAndDoneFlow(t *testing.T) {
	st := demoState()
	saved := false
	m := newModel(st, []string{"C:/repos"}, "", "", func() error { saved = true; return nil })

	m = runeKey(m, 'G')
	if m.mode != inGroup {
		t.Fatal("G 应进入分组输入模式")
	}
	m = typeText(m, "工作")
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "工作") {
		t.Fatalf("视图未显示新分组:\n%s", m.View())
	}
	if st.Repos[0].Group != "工作" && st.Repos[1].Group != "工作" {
		t.Fatal("分组未写入仓库记录")
	}

	// 空格勾选当前行，再按 d 标为已处理（作用域是勾选行）
	m = key(m, tea.KeyMsg{Type: tea.KeySpace})
	m = runeKey(m, 'd')
	grouped := st.Repos[0].Group
	if grouped != "工作" {
		t.Fatalf("被处理的仓库应带分组，实际 %q", grouped)
	}
	if st.Repos[0].Status != state.StatusDone {
		t.Fatalf("被勾选的行应变为 done，实际 %q", st.Repos[0].Status)
	}
	if st.Repos[1].Status != state.StatusNew {
		t.Fatal("未被勾选的行不应被改动")
	}
	if len(m.rows) != 1 {
		t.Fatalf("待处理视图应只剩 1 行，实际 %d", len(m.rows))
	}

	// q 退出时应触发保存
	m = runeKey(m, 'q')
	if !saved {
		t.Fatal("退出时应保存状态")
	}
}

// 忽略：退出待处理视图，并能切回来看到
func TestIgnoreFlow(t *testing.T) {
	st := demoState()
	m := newModel(st, []string{"C:/repos"}, "", "", func() error { return nil })
	target := m.rows[m.cursor].r
	m = runeKey(m, 'i')
	if !target.Ignored {
		t.Fatal("i 应把目标标记为忽略")
	}
	if len(m.rows) != 1 {
		t.Fatalf("忽略一条后待处理应剩 1 行，实际 %d", len(m.rows))
	}
	m = runeKey(m, 'v')
	m = runeKey(m, 'v')
	if len(m.rows) != 2 {
		t.Fatalf("已忽略视图应有 2 条，实际 %d", len(m.rows))
	}
}

// 过滤输入 -> Enter 生效 -> c 清除
func TestFilterFlow(t *testing.T) {
	m := newModel(demoState(), []string{"C:/repos"}, "", "", func() error { return nil })
	m = runeKey(m, 'F')
	m = typeText(m, "beta")
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.rows) != 1 || m.rows[0].r.Name != "beta" {
		t.Fatalf("过滤失败，剩余 %d 行", len(m.rows))
	}
	m = runeKey(m, 'c')
	if len(m.rows) != 2 {
		t.Fatalf("清除过滤后应恢复 2 行，实际 %d", len(m.rows))
	}
}

// View 在任何状态下都不能渲染出空字符串，也不能 panic
func TestViewNeverEmpty(t *testing.T) {
	m := newModel(demoState(), []string{"C:/repos"}, "", "", func() error { return nil })
	if m.View() == "" {
		t.Fatal("View 不应为空")
	}
	m = runeKey(m, '?')
	if m.View() == "" {
		t.Fatal("帮助页不应为空")
	}
	m = runeKey(m, '?')
	m = key(m, tea.KeyMsg{Type: tea.KeyDown})
	m = key(m, tea.KeyMsg{Type: tea.KeyPgDown})
	m = key(m, tea.KeyMsg{Type: tea.KeyUp})
	m = runeKey(m, 'A')
	if m.View() == "" {
		t.Fatal("多次按键后 View 不应为空")
	}
	if m.selectedCount() != 2 {
		t.Fatalf("A 应全选当前视图的 2 行，实际 %d", m.selectedCount())
	}
}

// 折叠：同一目录下的仓库收成一行，操作作用于组内全部仓库
func TestFoldedRow(t *testing.T) {
	st := demoState()
	st.Repos = []*state.Repo{
		mkRepo("C:/repos/ryxl/a"),
		mkRepo("C:/repos/ryxl/b"),
		mkRepo("C:/repos/ryxl/c"),
		mkRepo("C:/work/solo"),
	}
	st.Fold("C:/repos/ryxl", "")

	m := newModel(st, []string{"C:/repos", "C:/work"}, "", "", func() error { return nil })
	if len(m.rows) != 2 {
		t.Fatalf("3 个仓库应折叠成 1 行，加上 solo 共 2 行，实际 %d", len(m.rows))
	}
	idx := -1
	for i, rw := range m.rows {
		if rw.fold != nil {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("应有一行是折叠组")
	}
	if len(m.rows[idx].fold.repos) != 3 {
		t.Fatalf("折叠组应含 3 个仓库，实际 %d", len(m.rows[idx].fold.repos))
	}
	if m.View() == "" {
		t.Fatal("折叠行渲染不应为空")
	}

	// 光标移到折叠行，勾选后按 d，组内三个都应变成 done
	m.cursor = idx
	m = key(m, tea.KeyMsg{Type: tea.KeySpace})
	if n := len(m.targets()); n != 3 {
		t.Fatalf("勾选折叠组应选中 3 个仓库，实际 %d", n)
	}
	m = runeKey(m, 'd')
	done := 0
	for _, r := range st.Repos {
		if r.Status == state.StatusDone {
			done++
		}
	}
	if done != 3 {
		t.Fatalf("折叠组内 3 个仓库都应标为 done，实际 %d", done)
	}
}

// e 键：在折叠行上临时展开，不写盘
func TestToggleFold(t *testing.T) {
	st := demoState()
	st.Repos = []*state.Repo{
		mkRepo("C:/repos/ryxl/a"),
		mkRepo("C:/repos/ryxl/b"),
		mkRepo("C:/work/solo"),
	}
	st.Fold("C:/repos/ryxl", "")
	m := newModel(st, []string{"C:/repos", "C:/work"}, "", "", func() error { return nil })

	idx := -1
	for i, rw := range m.rows {
		if rw.fold != nil {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("应有折叠行")
	}
	m.cursor = idx
	m = runeKey(m, 'e')
	if len(m.rows) != 3 {
		t.Fatalf("e 展开后应有 3 行，实际 %d", len(m.rows))
	}
	if len(st.Folded) != 1 {
		t.Fatal("e 只应是会话内展开，不应改动持久规则")
	}
}

// hide：目录被隐藏后，任何视图都不出现
func TestHiddenDirExcluded(t *testing.T) {
	st := demoState()
	st.Repos = []*state.Repo{
		mkRepo("C:/repos/ryxl/a"),
		mkRepo("C:/repos/ryxl/b"),
		mkRepo("C:/work/solo"),
	}
	st.Hide("C:/repos/ryxl", "")
	m := newModel(st, []string{"C:/repos", "C:/work"}, "", "", func() error { return nil })
	if len(m.rows) != 1 {
		t.Fatalf("隐藏 ryxl 后应只剩 1 行，实际 %d", len(m.rows))
	}
	if m.rows[0].r == nil || !strings.HasSuffix(m.rows[0].r.Path, "solo") {
		t.Fatal("剩下的应是 solo")
	}
	// 切到"全部"视图也不该出现
	m = runeKey(m, 'v')
	if len(m.rows) != 1 {
		t.Fatalf("全部视图下隐藏目录仍不应出现，实际 %d 行", len(m.rows))
	}
}

// 已有分组/标签在输入时应给出提示
func TestExistingHints(t *testing.T) {
	st := demoState()
	st.Repos[1].Group = "旧分组"
	st.Repos[1].Tags = []string{"老标签"}
	m := newModel(st, []string{"C:/repos"}, "", "", func() error { return nil })
	m = runeKey(m, 'G')
	if !strings.Contains(m.View(), "已有分组") {
		t.Fatalf("输入分组时应提示已有分组:\n%s", m.View())
	}
}
