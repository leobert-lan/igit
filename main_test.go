package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grscan/internal/scope"
	"grscan/internal/state"
	"grscan/internal/util"
)

// completed 还原 Tab 会补出什么：已输入的部分 + readline 返回的剩余。
func completed(c replCompleter, line string) []string {
	rs := []rune(line)
	newLines, n := c.Do(rs, len(rs))
	if len(newLines) == 0 {
		return nil
	}
	head := string(rs[:n])
	out := make([]string, 0, len(newLines))
	for _, nl := range newLines {
		out = append(out, head+string(nl))
	}
	return out
}

func mkCtx(t *testing.T, paths ...string) *appCtx {
	t.Helper()
	st := &state.State{}
	for _, p := range paths {
		st.Repos = append(st.Repos, &state.Repo{
			ID:   util.NormalizePath(p),
			Path: p,
			Name: filepath.Base(p),
			Kind: "repo",
		})
	}
	res, err := scope.Resolve(scope.Scope{Source: scope.SourceRoot}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	return &appCtx{state: st, res: res}
}

// 第一个词补的是"这个界面能敲什么"
func TestCompleteCommands(t *testing.T) {
	ctx := mkCtx(t, `D:\work\a`, `D:\work\b`)
	c := replCompleter{ctx: ctx}
	got := strings.Join(completed(c, "st"), " ")
	if !strings.Contains(got, "stats") {
		t.Fatalf("st 应补出 stats，实际: %q", got)
	}
	if strings.Contains(got, "scanx") {
		t.Fatalf("补出了不存在的命令: %q", got)
	}
}

// 根下 cd 补维度入口，把三个维度都摆出来
func TestCompleteCdRootGivesDimensions(t *testing.T) {
	ctx := mkCtx(t, `D:\work\a`, `D:\work\b`)
	c := replCompleter{ctx: ctx}
	got := strings.Join(completed(c, "cd "), " ")
	for _, want := range []string{"file:", "group:", "tag:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cd 后应能补出 %s，实际: %q", want, got)
		}
	}
}

// file: 后面的路径直接读磁盘
func TestCompleteFilePathReadsDisk(t *testing.T) {
	root := t.TempDir()
	// TempDir 在 Windows 上可能是短路径，统一成绝对路径再拼
	base := filepath.Join(root, "ASWorkSpace")
	if err := os.MkdirAll(filepath.Join(base, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 只补前半截，不该带出文件
	got := filePaths("file:", filepath.Join(root, "AS"))
	if len(got) != 1 || filepath.Base(got[0]) != "ASWorkSpace" {
		t.Fatalf("路径补全不对: %v", got)
	}

	// 末尾带分隔符 = 列这层的子目录，只给目录不给文件
	got = filePaths("file:", base+string(filepath.Separator))
	if len(got) != 2 {
		t.Fatalf("子目录应只有两个（tmp/other），实际 %v", got)
	}
	for _, g := range got {
		if strings.HasSuffix(g, "note.txt") {
			t.Fatalf("不该把文件补出来: %v", got)
		}
	}
}

// hide / fold 的相对路径必须以作用域为基准，不能拿进程 cwd 冒充
func TestRequireDirArgUsesScopeBase(t *testing.T) {
	st := &state.State{}
	for _, p := range []string{
		`D:\repos\ryxl_rd\algorithm\a.git`,
		`D:\repos\ryxl_rd\algorithm\b.git`,
		`D:\repos\other\c.git`,
	} {
		st.Repos = append(st.Repos, &state.Repo{
			ID: util.NormalizePath(p), Path: p, Name: filepath.Base(p), Kind: "repo",
		})
	}
	res, err := scope.Resolve(scope.Scope{
		Segs: []scope.Seg{{Dim: scope.DimFile, Ident: `D:\repos`}},
	}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	ctx := &appCtx{state: st, res: res}

	got, ok := requireDirArg(ctx, parseArgs([]string{"ryxl_rd"}), "hide")
	if !ok {
		t.Fatal("解析失败")
	}
	if want := filepath.Join(`D:\repos`, "ryxl_rd"); got != want {
		t.Fatalf("应解析成 %s，实际 %s", want, got)
	}
}

// 作用域里没有 file 段时无从推断，退回进程 cwd —— 但必须把基准说出来
func TestRequireDirArgFallsBackToCWD(t *testing.T) {
	root := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	st := &state.State{Repos: []*state.Repo{
		{ID: util.NormalizePath(`D:\repos\a`), Path: `D:\repos\a`, Name: "a", Kind: "repo"},
	}}
	res, err := scope.Resolve(scope.Scope{Source: scope.SourceRoot}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	ctx := &appCtx{state: st, res: res}

	got, ok := requireDirArg(ctx, parseArgs([]string{"sub"}), "hide")
	if !ok {
		t.Fatal("解析失败")
	}
	if want := filepath.Join(root, "sub"); got != want {
		t.Fatalf("应退回进程目录解析成 %s，实际 %s", want, got)
	}
}

// 盖住 0 个仓库的规则不许落盘：静默写一条永远不生效的规则比报错危险
func TestGuardEmptyRuleRejects(t *testing.T) {
	st := &state.State{Repos: []*state.Repo{
		{ID: util.NormalizePath(`D:\repos\ryxl_rd\algorithm\a.git`), Path: `D:\repos\ryxl_rd\algorithm\a.git`, Name: "a", Kind: "repo"},
	}}
	res, err := scope.Resolve(scope.Scope{Source: scope.SourceRoot}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	ctx := &appCtx{state: st, res: res}

	// 打字打错：repos 下没有 algoritm 这一层
	if guardEmptyRule(ctx, filepath.Join(`D:\repos`, "ryxl_rd", "algoritm"), "hide") {
		t.Fatal("盖住 0 个仓库却放行了")
	}
	// 真的有仓库的那条要放行
	if !guardEmptyRule(ctx, `D:\repos\ryxl_rd`, "hide") {
		t.Fatal("盖住 1 个仓库却被拦下")
	}
}

// 嵌套节点上"看它自己"的入口也要能补出来
func TestCompleteCdNestedOffersRepoPrefix(t *testing.T) {
	self := `D:\repos\opensource`
	st := &state.State{Repos: []*state.Repo{
		{ID: util.NormalizePath(self), Path: self, Name: "opensource", Kind: "repo"},
		{ID: util.NormalizePath(`D:\repos\opensource\a`), Path: `D:\repos\opensource\a`, Name: "a", Kind: "repo"},
	}}
	res, err := scope.Resolve(scope.Scope{Segs: []scope.Seg{{Dim: scope.DimFile, Ident: self, Explicit: true}}}, st)
	if err != nil {
		t.Fatalf("Resolve 出错: %v", err)
	}
	if res.Self == nil {
		t.Fatal("Self 没认出来")
	}
	c := replCompleter{ctx: &appCtx{state: st, res: res}}
	got := strings.Join(completed(c, "cd "), " ")
	if !strings.Contains(got, "repo:"+self) {
		t.Fatalf("嵌套节点应补出 repo:%s，实际: %q", self, got)
	}
	// 自身不该作为普通子项出现两次
	if n := strings.Count(got, "opensource"); n != 1 {
		t.Fatalf("opensource 出现了 %d 次: %q", n, got)
	}
}
