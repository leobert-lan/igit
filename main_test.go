package main

import (
	"bufio"
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
	newLines, _ := c.Do(rs, len(rs))
	if len(newLines) == 0 {
		return nil
	}
	head := string(rs)
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

// help 后能补出各个专题和命令
func TestCompleteHelpTopics(t *testing.T) {
	ctx := mkCtx(t, `D:\work\a`)
	c := replCompleter{ctx: ctx}
	got := strings.Join(completed(c, "help "), " ")
	for _, want := range []string{"scope", "filter", "tig", "rules", "config", "all", "scan", "list"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help 后应能补出 %s，实际: %q", want, got)
		}
	}

	gotSc := strings.Join(completed(c, "help sc"), " ")
	if !strings.Contains(gotSc, "scope") || !strings.Contains(gotSc, "scan") {
		t.Fatalf("help sc 应补出 scope 和 scan，实际: %q", gotSc)
	}
}

// 帮助主题规范化：别名与前缀支持
func TestNormalizeHelpTopic(t *testing.T) {
	cases := map[string]string{
		"scope":    "scope",
		"--scope":  "scope",
		"-s":       "scope",
		"--filter": "filter",
		"-f":       "filter",
		"--tig":    "tig",
		"-t":       "tig",
		"--rules":  "rules",
		"--config": "config",
		"-c":       "config",
		"--all":    "all",
		"-a":       "all",
	}
	for in, want := range cases {
		if got := normalizeHelpTopic(in); got != want {
			t.Errorf("normalizeHelpTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCmdGroupNewAndList(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st}

	// 1. 新建分组 study
	code := cmdGroup(ctx, args{pos: []string{"new", "study"}})
	if code != 0 {
		t.Fatalf("group new study 失败: exit code %d", code)
	}
	if !st.HasGroup("study") {
		t.Fatal("state 未包含 study 分组")
	}

	// 2. 重新从磁盘加载 state，验证持久化生效
	st2, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.HasGroup("study") {
		t.Fatal("持久化文件中未包含 study 分组")
	}

	// 3. 删除分组
	ctx2 := &appCtx{state: st2}
	code = cmdGroup(ctx2, args{pos: []string{"rm", "study"}})
	if code != 0 {
		t.Fatalf("group rm study 失败: exit code %d", code)
	}
	if st2.HasGroup("study") {
		t.Fatal("删除后 state 仍包含 study 分组")
	}
}

func TestRequireDirArgDotInRepoAndDir(t *testing.T) {
	st := &state.State{Repos: []*state.Repo{
		{ID: util.NormalizePath(`D:\work\repo1`), Path: `D:\work\repo1`, Name: "repo1", Kind: "repo"},
		{ID: util.NormalizePath(`D:\work\repo2`), Path: `D:\work\repo2`, Name: "repo2", Kind: "repo"},
	}}

	// 1. 处于单仓作用域
	scRepo, err := scope.Parse(`file:D:\work\repo1`)
	if err != nil {
		t.Fatal(err)
	}
	resRepo, err := scope.Resolve(scRepo, st)
	if err != nil {
		t.Fatal(err)
	}
	ctxRepo := &appCtx{state: st, res: resRepo}
	got, ok := requireDirArg(ctxRepo, parseArgs([]string{"."}), "hide")
	if !ok {
		t.Fatal("requireDirArg '.' 应成功解析")
	}
	if want := filepath.Clean(`D:\work\repo1`); got != want {
		t.Fatalf("单仓下 '.' 应解析为 %s，实际 %s", want, got)
	}

	// 2. 处于物理目录作用域
	scDir, err := scope.Parse(`file:D:\work`)
	if err != nil {
		t.Fatal(err)
	}
	resDir, err := scope.Resolve(scDir, st)
	if err != nil {
		t.Fatal(err)
	}
	ctxDir := &appCtx{state: st, res: resDir}
	got, ok = requireDirArg(ctxDir, parseArgs([]string{"."}), "fold")
	if !ok {
		t.Fatal("requireDirArg '.' 应成功解析")
	}
	if want := filepath.Clean(`D:\work`); got != want {
		t.Fatalf("目录下 '.' 应解析为 %s，实际 %s", want, got)
	}
}

func TestRequireDirArgDotAtRootFails(t *testing.T) {
	st := &state.State{Repos: []*state.Repo{
		{ID: util.NormalizePath(`D:\work\repo1`), Path: `D:\work\repo1`, Name: "repo1", Kind: "repo"},
	}}
	res, err := scope.Resolve(scope.Scope{Source: scope.SourceRoot}, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res}
	_, ok := requireDirArg(ctx, parseArgs([]string{"."}), "hide")
	if ok {
		t.Fatal("根目录下 '.' 应被拦截报错")
	}
}

func TestCmdHideAndFoldDotConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(tmpDir, "work")
	repoPath := filepath.Join(workDir, "myrepo")
	otherPath := filepath.Join(workDir, "otherrepo")
	st.Repos = append(st.Repos,
		&state.Repo{ID: util.NormalizePath(repoPath), Path: repoPath, Name: "myrepo", Kind: "repo"},
		&state.Repo{ID: util.NormalizePath(otherPath), Path: otherPath, Name: "otherrepo", Kind: "repo"},
	)
	_ = st.Save()

	sc, err := scope.Parse("file:" + repoPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := scope.Resolve(sc, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 1. 用户输入 n 取消
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("n\n"))
	code := cmdHide(ctx, parseArgs([]string{"."}), true)
	if code != 0 {
		t.Fatalf("cmdHide 取消返回码错误: %d", code)
	}
	if st.IsHidden(repoPath) {
		t.Fatal("取消后不应该隐藏仓库")
	}

	// 2. 用户输入 y 确认
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("y\n"))
	code = cmdHide(ctx, parseArgs([]string{"."}), true)
	if code != 0 {
		t.Fatalf("cmdHide 确认返回码错误: %d", code)
	}
	if !st.IsHidden(repoPath) {
		t.Fatal("确认后仓库应被隐藏")
	}
	// 验证退回了上一级作用域（workDir 包含 otherrepo）
	if ctx.res.TargetRepo() != nil {
		t.Fatalf("隐藏当前仓后应该步进到上层作用域，实际仍停留在: %v", ctx.res.TargetRepo().Path)
	}
	if gotBase := ctx.res.Scope.BaseDir(); gotBase != workDir {
		t.Fatalf("步进后的作用域应为 %s，实际 %s", workDir, gotBase)
	}

	// 3. 在当前目录作用域下执行 fold . -y
	code = cmdFold(ctx, parseArgs([]string{".", "-y"}), true)
	if code != 0 {
		t.Fatalf("cmdFold -y 返回码错误: %d", code)
	}
	folded := st.FoldedPaths()
	if len(folded) == 0 || folded[0] != workDir {
		t.Fatalf("fold . 应将 %s 记录进 FoldedPaths，实际: %v", workDir, folded)
	}
}

func TestCmdGroupInteractiveAndConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}
	repoPath := filepath.Join(tmpDir, "repo-a")
	repo := &state.Repo{ID: util.NormalizePath(repoPath), Path: repoPath, Name: "repo-a", Kind: "repo"}
	st.Repos = append(st.Repos, repo)
	st.AddGroup("study")
	st.AddGroup("job")
	_ = st.Save()

	sc, err := scope.Parse("file:" + repoPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := scope.Resolve(sc, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 1. 在单仓下输入 group，交互式选择 1 (job，按字母序排第一)
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("1\n"))
	code := cmdGroup(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdGroup 交互选择返回码错误: %d", code)
	}
	if repo.Group != "job" {
		t.Fatalf("期望 repo.Group = job, 实际: %s", repo.Group)
	}

	// 2. 在单仓下输入 group，交互式选择 2 (study)，但确认时输入 n
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("2\nn\n"))
	code = cmdGroup(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdGroup 取消返回码错误: %d", code)
	}
	if repo.Group != "job" {
		t.Fatalf("取消替换后分组应保持 job, 实际: %s", repo.Group)
	}

	// 3. 在单仓下输入 group，交互式选择 2 (study)，确认时输入 y
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("2\ny\n"))
	code = cmdGroup(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdGroup 确认替换返回码错误: %d", code)
	}
	if repo.Group != "study" {
		t.Fatalf("确认替换后分组应为 study, 实际: %s", repo.Group)
	}

	// 4. 在单仓下输入 group clear 清除分组
	code = cmdGroup(ctx, parseArgs([]string{"clear"}))
	if code != 0 {
		t.Fatalf("group clear 返回码错误: %d", code)
	}
	if repo.Group != "" {
		t.Fatalf("clear 后分组应为空, 实际: %s", repo.Group)
	}

	// 5. 快速快捷设置 group study，再快捷设置 group job (测试带 -y 与不带 -y)
	code = cmdGroup(ctx, parseArgs([]string{"study"}))
	if code != 0 || repo.Group != "study" {
		t.Fatalf("快捷分配 study 失败: code %d, group %s", code, repo.Group)
	}
	// 不带 -y 且输入 n
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("n\n"))
	code = cmdGroup(ctx, parseArgs([]string{"job"}))
	if code != 0 || repo.Group != "study" {
		t.Fatalf("快捷替换取消失败: code %d, group %s", code, repo.Group)
	}
	// 带 -y 自动放行
	code = cmdGroup(ctx, parseArgs([]string{"job", "-y"}))
	if code != 0 || repo.Group != "job" {
		t.Fatalf("带 -y 快捷替换失败: code %d, group %s", code, repo.Group)
	}
}

func TestCmdGroupInGroupScope(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}
	repoPath1 := filepath.Join(tmpDir, "repo-1")
	repoPath2 := filepath.Join(tmpDir, "repo-2")
	r1 := &state.Repo{ID: util.NormalizePath(repoPath1), Path: repoPath1, Name: "repo-1", Group: "study", Kind: "repo"}
	r2 := &state.Repo{ID: util.NormalizePath(repoPath2), Path: repoPath2, Name: "repo-2", Group: "study", Kind: "repo"}
	st.Repos = append(st.Repos, r1, r2)
	st.AddGroup("study")
	_ = st.Save()

	sc, err := scope.Parse("group:study")
	if err != nil {
		t.Fatal(err)
	}
	res, err := scope.Resolve(sc, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 在 group:study 作用域下，输入 group rm，通过交互式选项选 1 移出 repo-1
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("1\n"))
	code := cmdGroup(ctx, parseArgs([]string{"rm"}))
	if code != 0 {
		t.Fatalf("group rm 移出失败: exit code %d", code)
	}
	if r1.Group != "" {
		t.Fatalf("repo-1 应被移出分组, 实际 group=%s", r1.Group)
	}
	if r2.Group != "study" {
		t.Fatalf("repo-2 应保留在分组 study, 实际 group=%s", r2.Group)
	}

	// 在 group:study 作用域下，输入 group add repo-1 把 repo-1 拉回 study
	code = cmdGroup(ctx, parseArgs([]string{"add", "repo-1"}))
	if code != 0 {
		t.Fatalf("group add 失败: exit code %d", code)
	}
	if r1.Group != "study" {
		t.Fatalf("repo-1 应被拉入 study, 实际 group=%s", r1.Group)
	}
}

func TestAggregateByTags(t *testing.T) {
	repos := []*state.Repo{
		{Name: "r1", Tags: []string{"client-bsi", "rust", "sdk"}},
		{Name: "r2", Tags: []string{"client-bsi", "rust", "cli"}},
		{Name: "r3", Tags: []string{"client-bsi", "flutter"}},
		{Name: "r4", Tags: []string{"tools", "python"}},
	}

	// 1. 无标签过滤时，包含全部 4 个仓库，统计全部伴生标签
	matched, facets := aggregateByTags(repos, nil)
	if len(matched) != 4 {
		t.Fatalf("全量命中应为 4, 实际: %d", len(matched))
	}
	// client-bsi 出现 3 次，应排第一
	if len(facets) == 0 || facets[0].Name != "client-bsi" || facets[0].Count != 3 {
		t.Fatalf("第一个伴生标签应为 client-bsi (3), 实际: %+v", facets)
	}

	// 2. 选择 client-bsi 后，命中 3 个，伴生标签排除 client-bsi
	matched, facets = aggregateByTags(repos, []string{"client-bsi"})
	if len(matched) != 3 {
		t.Fatalf("client-bsi 命中应为 3, 实际: %d", len(matched))
	}
	// rust 出现 2 次，排第一
	if facets[0].Name != "rust" || facets[0].Count != 2 {
		t.Fatalf("伴生标签应为 rust (2), 实际: %+v", facets[0])
	}

	// 3. 选择 client-bsi + rust 后，命中 2 个 (r1, r2)
	matched, facets = aggregateByTags(repos, []string{"client-bsi", "rust"})
	if len(matched) != 2 {
		t.Fatalf("client-bsi + rust 命中应为 2, 实际: %d", len(matched))
	}
}

func TestCmdLsByTagInteractiveFacetedFlow(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(tmpDir, "repo-algo")
	p2 := filepath.Join(tmpDir, "repo-core")
	p3 := filepath.Join(tmpDir, "repo-ui")
	r1 := &state.Repo{ID: util.NormalizePath(p1), Path: p1, Name: "repo-algo", Tags: []string{"client-bsi", "rust", "sdk"}, Kind: "repo"}
	r2 := &state.Repo{ID: util.NormalizePath(p2), Path: p2, Name: "repo-core", Tags: []string{"client-bsi", "rust", "cli"}, Kind: "repo"}
	r3 := &state.Repo{ID: util.NormalizePath(p3), Path: p3, Name: "repo-ui", Tags: []string{"client-bsi", "flutter"}, Kind: "repo"}
	st.Repos = append(st.Repos, r1, r2, r3)
	_ = st.Save()

	res, err := scope.Resolve(scope.Scope{Source: scope.SourceRoot}, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 模拟交互输入流：
	// 1. 输入 "1" (选择第一个推荐伴生标签 client-bsi，当前全集 3 个仓都有)
	// 2. 输入 "1" (在 client-bsi 的伴生推荐中选择 rust，剩下 2 个仓 repo-algo 和 repo-core)
	// 3. 输入 "#2" (直达进入第二个命中仓库 repo-core)
	inputSequence := "1\n1\n#2\n"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader(inputSequence))

	code := cmdLsByTag(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdLsByTag 退出码错误: %d", code)
	}

	// 验证成功进入目标仓库 repo-core
	if ctx.res == nil || ctx.res.TargetRepo() == nil {
		t.Fatal("未成功切换至目标单仓作用域")
	}
	if ctx.res.TargetRepo().Name != "repo-core" {
		t.Fatalf("期望进入 repo-core, 实际进入: %s", ctx.res.TargetRepo().Name)
	}
}

func TestCleanTagInput(t *testing.T) {
	// 1. ANSI 方向键与删除键转义序列过滤
	dirtyArrow := "tag\x1b[D\x1b[Cname"
	if got := cleanTagInput(dirtyArrow); got != "tagname" {
		t.Fatalf("ANSI 方向键清理失败: %q", got)
	}

	dirtyDelete := "foo\x1b[3~bar"
	if got := cleanTagInput(dirtyDelete); got != "foobar" {
		t.Fatalf("ANSI 删除键清理失败: %q", got)
	}

	// 2. 模拟终端退格与 DEL 键
	dirtyBS := "helpp\b"
	if got := cleanTagInput(dirtyBS); got != "help" {
		t.Fatalf("退格键模拟失败: %q", got)
	}

	// 3. 不可见控制字符过滤
	dirtyNull := "  hello\x00world  "
	if got := cleanTagInput(dirtyNull); got != "helloworld" {
		t.Fatalf("NULL 字符过滤失败: %q", got)
	}
}

func TestCmdTagInRepoScope(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}

	repoPath := filepath.Join(tmpDir, "AgyArchiveHelper")
	repo := &state.Repo{ID: util.NormalizePath(repoPath), Path: repoPath, Name: "AgyArchiveHelper", Kind: "repo"}
	st.Repos = append(st.Repos, repo)
	_ = st.Save()

	sc, err := scope.Parse("file:" + repoPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := scope.Resolve(sc, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 1. 系统中无任何标签时，单仓下输入 tag，提示并输入 "+" 新建 "backup, helper"
	inputSeq1 := "+\nbackup, helper\n"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader(inputSeq1))
	code := cmdTag(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdTag 无标签下创建失败: code %d", code)
	}
	if len(repo.Tags) != 2 || repo.Tags[0] != "backup" || repo.Tags[1] != "helper" {
		t.Fatalf("标签创建未生效: %+v", repo.Tags)
	}

	// 2. 系统中已有标签时，输入 tag 弹出复选列表，输入 "1" 取消 backup，再输入 "q" 退出
	inputSeq2 := "1\nq\n"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader(inputSeq2))
	code = cmdTag(ctx, parseArgs([]string{}))
	if code != 0 {
		t.Fatalf("cmdTag 复选切换失败: code %d", code)
	}
	if len(repo.Tags) != 1 || repo.Tags[0] != "helper" {
		t.Fatalf("取消 backup 勾选失败: %+v", repo.Tags)
	}

	// 3. 单仓下快捷追加标签 tag rust
	code = cmdTag(ctx, parseArgs([]string{"rust"}))
	if code != 0 {
		t.Fatalf("快捷打标失败: code %d", code)
	}
	if !hasTag(repo, "rust") || !hasTag(repo, "helper") {
		t.Fatalf("快捷打标未保留原有标签: %+v", repo.Tags)
	}

	// 4. 单仓下清空标签 tag clear
	code = cmdTag(ctx, parseArgs([]string{"clear"}))
	if code != 0 {
		t.Fatalf("清空标签失败: code %d", code)
	}
	if len(repo.Tags) != 0 {
		t.Fatalf("清空标签未生效: %+v", repo.Tags)
	}
}

func TestCmdTagInTagScope(t *testing.T) {
	tmpDir := t.TempDir()
	stPath := filepath.Join(tmpDir, "state.json")
	st, err := state.Load(stPath)
	if err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(tmpDir, "r1")
	p2 := filepath.Join(tmpDir, "r2")
	r1 := &state.Repo{ID: util.NormalizePath(p1), Path: p1, Name: "r1", Tags: []string{"rust"}, Kind: "repo"}
	r2 := &state.Repo{ID: util.NormalizePath(p2), Path: p2, Name: "r2", Tags: []string{"rust"}, Kind: "repo"}
	st.Repos = append(st.Repos, r1, r2)
	_ = st.Save()

	sc, err := scope.Parse("tag:rust")
	if err != nil {
		t.Fatal(err)
	}
	res, err := scope.Resolve(sc, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &appCtx{state: st, res: res, repl: true}

	// 在 tag:rust 作用域下输入 tag rm，弹出当前拥有 rust 的仓库列表，输入 1 移除 r1 的 rust 标签
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("1\n"))
	code := cmdTag(ctx, parseArgs([]string{"rm"}))
	if code != 0 {
		t.Fatalf("tag rm 在标签作用域执行失败: %d", code)
	}
	if hasTag(r1, "rust") {
		t.Fatal("r1 的 rust 标签未被移除")
	}
	if !hasTag(r2, "rust") {
		t.Fatal("r2 的 rust 标签不应被移除")
	}
}




