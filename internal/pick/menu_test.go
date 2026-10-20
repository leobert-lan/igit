package pick

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMenuTextFallback(t *testing.T) {
	items := []MenuItem{
		{Key: "1", Label: "Alpha", Badge: "[当前目录]", Detail: "(1 个)"},
		{Key: "2", Label: "Beta", Badge: "[超出范围]", Detail: "(2 个)"},
		{Key: "+", Label: "新建分组..."},
	}

	// 1. 测试按编号 1 选择
	var outBuf bytes.Buffer
	in1 := strings.NewReader("1\n")
	chosen, cancelled := Menu(MenuOpt{
		Title:       "测试菜单",
		Items:       items,
		Interactive: false,
		In:          in1,
		Out:         &outBuf,
	})
	if cancelled || chosen == nil || chosen.Label != "Alpha" {
		t.Fatalf("选择 1 失败: cancelled=%v, chosen=%+v", cancelled, chosen)
	}

	// 2. 测试按快捷键 + 选择
	in2 := strings.NewReader("+\n")
	chosen, cancelled = Menu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in2,
		Out:         &bytes.Buffer{},
	})
	if cancelled || chosen == nil || chosen.Label != "新建分组..." {
		t.Fatalf("选择 + 失败: cancelled=%v, chosen=%+v", cancelled, chosen)
	}

	// 3. 测试按名称选择
	in3 := strings.NewReader("Beta\n")
	chosen, cancelled = Menu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in3,
		Out:         &bytes.Buffer{},
	})
	if cancelled || chosen == nil || chosen.Label != "Beta" {
		t.Fatalf("选择 Beta 失败: cancelled=%v, chosen=%+v", cancelled, chosen)
	}

	// 4. 测试 q 取消
	in4 := strings.NewReader("q\n")
	chosen, cancelled = Menu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in4,
		Out:         &bytes.Buffer{},
	})
	if !cancelled || chosen != nil {
		t.Fatalf("q 取消失败: cancelled=%v, chosen=%+v", cancelled, chosen)
	}
}

func TestMenuModelBubbleTea(t *testing.T) {
	items := []MenuItem{
		{Key: "1", Label: "Item-1"},
		{Key: "2", Label: "Item-2"},
		{Key: "+", Label: "New"},
	}
	m := initialMenuModel(MenuOpt{
		Title: "测试TUI",
		Items: items,
	})

	// 初始状态
	if m.cursor != 0 {
		t.Fatalf("期望初始 cursor=0, 实际: %d", m.cursor)
	}

	// 按下箭头移动到第二个
	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mUpdated.(menuModel)
	if m.cursor != 1 {
		t.Fatalf("向下移动后 cursor 错误: %d", m.cursor)
	}

	// 按 Enter 确认选择 Item-2
	mUpdated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mUpdated.(menuModel)
	if cmd == nil || !m.quitting || m.chosen == nil || m.chosen.Label != "Item-2" {
		t.Fatalf("Enter 确认失败: %+v", m)
	}

	// 测试直接按数字快捷键
	m2 := initialMenuModel(MenuOpt{Items: items})
	m2Updated, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	m2 = m2Updated.(menuModel)
	if m2.chosen == nil || m2.chosen.Label != "New" {
		t.Fatalf("快捷键 + 触发失败: %+v", m2)
	}

	// 测试 Esc/q 取消
	m3 := initialMenuModel(MenuOpt{Items: items})
	m3Updated, _ := m3.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m3 = m3Updated.(menuModel)
	if !m3.cancelled || m3.chosen != nil {
		t.Fatalf("Esc 取消失败: %+v", m3)
	}
}

func TestMultiMenuTextFallback(t *testing.T) {
	items := []MenuItem{
		{Key: "1", Label: "Repo-A"},
		{Key: "2", Label: "Repo-B"},
		{Key: "3", Label: "Repo-C"},
	}

	// 1. 测试多选 "1 3"
	in1 := strings.NewReader("1 3\n")
	chosen, cancelled := MultiMenu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in1,
		Out:         &bytes.Buffer{},
	})
	if cancelled || len(chosen) != 2 {
		t.Fatalf("多选 1 3 失败: cancelled=%v, len=%d", cancelled, len(chosen))
	}
	if chosen[0].Label != "Repo-A" || chosen[1].Label != "Repo-C" {
		t.Fatalf("多选结果不匹配: %+v", chosen)
	}

	// 2. 测试逗号分隔 "2, 3"
	in2 := strings.NewReader("2, 3\n")
	chosen, cancelled = MultiMenu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in2,
		Out:         &bytes.Buffer{},
	})
	if cancelled || len(chosen) != 2 || chosen[0].Label != "Repo-B" || chosen[1].Label != "Repo-C" {
		t.Fatalf("多选逗号分隔失败: %+v", chosen)
	}

	// 3. 测试全选 "a"
	in3 := strings.NewReader("a\n")
	chosen, cancelled = MultiMenu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in3,
		Out:         &bytes.Buffer{},
	})
	if cancelled || len(chosen) != 3 {
		t.Fatalf("全选 a 失败: len=%d", len(chosen))
	}

	// 4. 测试取消 "q"
	in4 := strings.NewReader("q\n")
	chosen, cancelled = MultiMenu(MenuOpt{
		Items:       items,
		Interactive: false,
		In:          in4,
		Out:         &bytes.Buffer{},
	})
	if !cancelled || len(chosen) != 0 {
		t.Fatalf("取消 q 失败: cancelled=%v, len=%d", cancelled, len(chosen))
	}
}

func TestMultiMenuBubbleTea(t *testing.T) {
	items := []MenuItem{
		{Key: "1", Label: "Repo-A"},
		{Key: "2", Label: "Repo-B"},
		{Key: "3", Label: "Repo-C"},
	}
	m := initialMultiMenuModel(MenuOpt{
		Title: "测试多选TUI",
		Items: items,
	})

	// 1. 空格勾选第 1 个
	mUpdated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = mUpdated.(multiMenuModel)
	if !m.items[0].Selected {
		t.Fatalf("空格勾选失败")
	}

	// 2. 移动到第 2 个并空格勾选
	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mUpdated.(multiMenuModel)
	mUpdated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = mUpdated.(multiMenuModel)
	if !m.items[1].Selected {
		t.Fatalf("第二个项勾选失败")
	}

	// 3. Enter 确认选择
	mUpdated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mUpdated.(multiMenuModel)
	if cmd == nil || !m.confirmed || !m.quitting {
		t.Fatalf("Enter 确认多选失败")
	}

	// 4. 测试直接按数字键翻转
	m2 := initialMultiMenuModel(MenuOpt{Items: items})
	m2Updated, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m2 = m2Updated.(multiMenuModel)
	if !m2.items[2].Selected {
		t.Fatalf("数字键 3 翻转勾选失败")
	}

	// 5. 测试按 a 全选
	m3 := initialMultiMenuModel(MenuOpt{Items: items})
	m3Updated, _ := m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m3 = m3Updated.(multiMenuModel)
	for i, it := range m3.items {
		if !it.Selected {
			t.Fatalf("项 %d 未被全选", i)
		}
	}
}
