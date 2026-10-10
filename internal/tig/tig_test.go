package tig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grscan/internal/state"
)

// TestMsys2Terminal 只在 tig 旁边真有 mintty 时才用它。
// 探测过宽会把 conhost 能跑的情况也换成 mintty，反而多一层不确定。
func TestMsys2Terminal(t *testing.T) {
	dir := t.TempDir()
	if got := msys2Terminal(filepath.Join(dir, "tig.exe")); got != "" {
		t.Errorf("旁边没有 mintty 时不该返回: %q", got)
	}
	want := filepath.Join(dir, "mintty.exe")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := msys2Terminal(filepath.Join(dir, "tig.exe")); got != want {
		t.Errorf("探测到 %q，要 %q", got, want)
	}
}

// TestDecide 锁住"开屏位置"的判断顺序。
// 顺序一变，用户按 g 看到的就不是他以为会看到的那一屏。
func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		repo *state.Repo
		want string
		args string
	}{
		{"有改动优先于未推送", &state.Repo{Dirty: true, Unstaged: 3, Ahead: 2, HasUpstream: true}, "status", ""},
		{"领先远端只看没推的", &state.Repo{Ahead: 2, HasUpstream: true}, "log", "@{u}..HEAD"},
		{"领先但没上游不算领先", &state.Repo{Ahead: 2}, "refs", ""},
		{"没配上游看分支远端", &state.Repo{}, "refs", ""},
		{"干净且同步看历史", &state.Repo{HasUpstream: true}, "main", ""},
		{"落后远端不算待办", &state.Repo{Behind: 3, HasUpstream: true}, "main", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := Decide(c.repo, "auto")
			if err != nil {
				t.Fatalf("Decide 出错: %v", err)
			}
			if v.String() != c.want {
				t.Errorf("视图 = %s，要 %s（理由: %s）", v.String(), c.want, v.Why)
			}
			got := strings.Join(v.Args, " ")
			if got != c.args {
				t.Errorf("参数 = %q，要 %q", got, c.args)
			}
		})
	}
}

func TestDecideForced(t *testing.T) {
	r := &state.Repo{Dirty: true}
	for _, want := range []string{"status", "log", "refs", "main"} {
		v, err := Decide(r, want)
		if err != nil {
			t.Fatalf("强制 %s 出错: %v", want, err)
		}
		if v.String() != want {
			t.Errorf("强制 %s 得到 %s", want, v.String())
		}
	}
	if _, err := Decide(r, "nonsense"); err == nil {
		t.Error("不认识的视图应该报错")
	}
}

func TestArgsAndLine(t *testing.T) {
	v, _ := Decide(&state.Repo{Ahead: 1, HasUpstream: true}, "auto")
	argv := Args("tig", `D:\work\my repo`, v)
	want := []string{"tig", "-C", `D:\work\my repo`, "log", "@{u}..HEAD"}
	if len(argv) != len(want) {
		t.Fatalf("参数个数 = %d，要 %d: %v", len(argv), len(want), argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q，要 %q", i, argv[i], want[i])
		}
	}

	// 含空格的路径必须加引号，否则命令行会被切碎
	line := Line(argv)
	if !strings.Contains(line, `"D:\work\my repo"`) {
		t.Errorf("Line 没给带空格的路径加引号: %s", line)
	}
}

func TestLineNoNeedlessQuotes(t *testing.T) {
	got := Line([]string{"tig", "-C", `D:\work\repo`})
	if strings.Contains(got, `"D:`) {
		t.Errorf("不含空格不该加引号: %s", got)
	}
	if got != `tig -C D:\work\repo` {
		t.Errorf("Line = %q", got)
	}
}
