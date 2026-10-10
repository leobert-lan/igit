package pick

import (
	"strings"
	"testing"

	"grscan/internal/state"
)

func repo(path, name string) *state.Repo {
	return &state.Repo{ID: strings.ToLower(path), Path: path, Name: name}
}

// 池外的仓库：全清单里有，作用域里没有。解析绝不能摸到它。
func TestRepoNeverFallsBackToGlobal(t *testing.T) {
	outside := repo(`D:\elsewhere\log`, "log")
	inside := repo(`C:\work\log`, "log")

	got := Repo(Opt{
		Cmd: "tig", Label: "file:C:\\work", Pool: []*state.Repo{inside}, All: []*state.Repo{inside, outside},
	}, "log")
	if got != inside {
		t.Fatalf("应在池内命中，got=%v", got)
	}

	// 池内没有、全集有 —— 必须定不出来，而不是退回全集。
	got = Repo(Opt{
		Cmd: "tig", Label: "file:C:\\work",
		Pool: []*state.Repo{repo(`C:\work\other`, "other")},
		All:  []*state.Repo{outside},
	}, "log")
	if got != nil {
		t.Fatalf("池内没有就不该命中，却拿到了 %v", got.Path)
	}
}

func TestRepoSingleCandidateNeedsNoKeyword(t *testing.T) {
	only := repo(`C:\work\only`, "only")
	got := Repo(Opt{Cmd: "tig", Label: "file:C:\\work", Pool: []*state.Repo{only}}, "")
	if got != only {
		t.Fatalf("池里就一个，不该再问，got=%v", got)
	}

	// 多个又不给关键字：列候选，不动手。
	var buf strings.Builder
	got = Repo(Opt{
		Cmd: "tig", Label: "file:C:\\work", Out: &buf,
		Pool: []*state.Repo{only, repo(`C:\work\two`, "two")},
	}, "")
	if got != nil {
		t.Fatalf("多个候选时不该自动选，got=%v", got.Path)
	}
	if !strings.Contains(buf.String(), "得给一个关键字") {
		t.Fatalf("没说清楚为什么要关键字:\n%s", buf.String())
	}
}

func TestRepoAmbiguousAsksByNumber(t *testing.T) {
	a := repo(`C:\work\aaa`, "log")
	b := repo(`C:\work\bbbb`, "log")
	opt := Opt{Cmd: "tig", Label: "file:C:\\work", Pool: []*state.Repo{a, b}, All: []*state.Repo{a, b}}

	var buf strings.Builder
	opt.Out = &buf
	opt.In = strings.NewReader("2\n")
	// 编号按路径从短到长排，所以 1 是 aaa、2 是 bbbb。
	if got := Repo(opt, "log"); got != b {
		t.Fatalf("选 2 应拿到 bbbb，got=%v", got.Path)
	}
	if !strings.Contains(buf.String(), "1)") || !strings.Contains(buf.String(), "2)") {
		t.Fatalf("没有列出编号候选:\n%s", buf.String())
	}

	// 不能交互时（管道模式）只列候选，绝不替人选。
	buf.Reset()
	opt.In = nil
	if got := Repo(opt, "log"); got != nil {
		t.Fatalf("不能交互时不该自动选，got=%v", got.Path)
	}

	// 回车 = 取消。
	buf.Reset()
	opt.In = strings.NewReader("\n")
	if got := Repo(opt, "log"); got != nil {
		t.Fatalf("回车应取消，got=%v", got.Path)
	}
}
