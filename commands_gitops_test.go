package main

import (
	"bufio"
	"os/exec"
	"strings"
	"testing"

	"grscan/internal/config"
	"grscan/internal/scope"
	"grscan/internal/state"
	"grscan/internal/util"
)

func TestCompleteGitOpsCommands(t *testing.T) {
	ctx := mkCtx(t, `D:\work\repoA`)
	// 进入单仓作用域
	sc, err := scope.Parse(`file:D:\work\repoA`)
	if err != nil {
		t.Fatalf("parse scope error: %v", err)
	}
	repoRes, err := scope.Resolve(sc, ctx.state)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	ctx.res = repoRes
	ctx.cfg = config.Default()

	c := replCompleter{ctx: ctx}

	// 1. 验证命令补全
	cmds := completed(c, "ig")
	if !strings.Contains(strings.Join(cmds, " "), "ignore") {
		t.Errorf("ig 应补全 ignore，实际: %v", cmds)
	}

	remotes := completed(c, "rem")
	if !strings.Contains(strings.Join(remotes, " "), "remote") {
		t.Errorf("rem 应补全 remote，实际: %v", remotes)
	}

	// 2. 验证 ignore 子命令补全
	subIgnore := completed(c, "ignore ")
	joined := strings.Join(subIgnore, " ")
	if !strings.Contains(joined, "add") || !strings.Contains(joined, "show") || !strings.Contains(joined, "untracked") {
		t.Errorf("ignore 应补全 [add, show, untracked]，实际: %v", subIgnore)
	}

	// 3. 验证 remote 子命令补全
	subRemote := completed(c, "remote ")
	joinedRem := strings.Join(subRemote, " ")
	if !strings.Contains(joinedRem, "user") || !strings.Contains(joinedRem, "list") {
		t.Errorf("remote 应补全 [user, list...]，实际: %v", subRemote)
	}
}

func TestCmdPullSafetyChecks(t *testing.T) {
	dir := t.TempDir()
	r := &state.Repo{
		ID:        util.NormalizePath(dir),
		Path:      dir,
		Name:      "test_repo",
		Dirty:     true,
		Untracked: 3,
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	ctx := &appCtx{
		state: st,
		res:   res,
		cfg:   config.Default(),
	}

	// 脏工作区应拦截 pull，返回 1
	code := cmdPull(ctx, args{kv: map[string]string{}})
	if code != 1 {
		t.Errorf("dirty repo cmdPull should return 1, got %d", code)
	}

	// 分叉工作区应拦截 pull，返回 1
	r.Dirty = false
	r.Ahead = 2
	r.Behind = 3
	code = cmdPull(ctx, args{kv: map[string]string{}})
	if code != 1 {
		t.Errorf("diverged repo cmdPull should return 1, got %d", code)
	}
}

func TestCmdCommitCleanCheck(t *testing.T) {
	dir := t.TempDir()
	r := &state.Repo{
		ID:    util.NormalizePath(dir),
		Path:  dir,
		Name:  "test_clean",
		Dirty: false,
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	ctx := &appCtx{
		state: st,
		res:   res,
		cfg:   config.Default(),
	}

	// 干净工作区无需提交，返回 0
	code := cmdCommit(ctx, args{pos: []string{"test commit"}})
	if code != 0 {
		t.Errorf("clean repo cmdCommit should return 0, got %d", code)
	}
}

func TestRemoteTokenAESEncryption(t *testing.T) {
	dir := t.TempDir()
	_ = exec.Command("git", "init", dir).Run()
	r := &state.Repo{
		ID:   util.NormalizePath(dir),
		Path: dir,
		Name: "test_token_repo",
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	cfg := config.Default()
	cfgPath := t.TempDir() + "/config.json"
	ctx := &appCtx{
		state:   st,
		res:     res,
		cfg:     cfg,
		cfgPath: cfgPath,
	}

	// 1. 设置仓库级 Token
	plainToken := "ghp_secureToken1234567890"
	code := cmdRemote(ctx, args{pos: []string{"token", "origin", plainToken}})
	if code != 0 {
		t.Fatalf("cmdRemote token origin failed: code %d", code)
	}

	// 验证 config 中存的是密文，而不是明文
	key := r.ID + "#origin"
	encVal := ctx.cfg.RepoTokens[key]
	if encVal == "" {
		t.Fatalf("RepoTokens[%s] is empty", key)
	}
	if encVal == plainToken {
		t.Fatalf("RepoTokens stored in plain text!")
	}

	// 验证解密获取
	recovered := ctx.cfg.ResolveToken(r.ID, "origin", "github.com")
	if recovered != plainToken {
		t.Fatalf("expected recovered token %s, got %s", plainToken, recovered)
	}

	// 2. 设置 Host 级 Token
	hostToken := "glpat-secretGitlabToken"
	code = cmdRemote(ctx, args{pos: []string{"host-token", "gitlab.com", hostToken}})
	if code != 0 {
		t.Fatalf("cmdRemote host-token failed: code %d", code)
	}

	encHost := ctx.cfg.HostTokens["gitlab.com"]
	if encHost == "" || encHost == hostToken {
		t.Fatalf("HostTokens not properly encrypted: %s", encHost)
	}
	recoveredHost := ctx.cfg.GetHostToken("gitlab.com")
	if recoveredHost != hostToken {
		t.Fatalf("expected %s, got %s", hostToken, recoveredHost)
	}

	// 3. 清理 Token
	code = cmdRemote(ctx, args{pos: []string{"clear-token", "origin"}})
	if code != 0 {
		t.Fatalf("clear-token failed: code %d", code)
	}
	if ctx.cfg.GetRepoToken(r.ID, "origin") != "" {
		t.Fatalf("repo token should be cleared")
	}
}

func TestZeroRemoteHandling(t *testing.T) {
	dir := t.TempDir()
	_ = exec.Command("git", "init", dir).Run()
	r := &state.Repo{
		ID:   util.NormalizePath(dir),
		Path: dir,
		Name: "test_zero_remotes",
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	ctx := &appCtx{
		state: st,
		res:   res,
		cfg:   config.Default(),
	}

	// 0 个远端时，fetch / pull / push 应优雅拦截提示并返回 0，不崩溃
	if c := cmdFetch(ctx, args{}); c != 0 {
		t.Errorf("zero remote cmdFetch expected 0, got %d", c)
	}
	if c := cmdPull(ctx, args{}); c != 0 {
		t.Errorf("zero remote cmdPull expected 0, got %d", c)
	}
	if c := cmdPush(ctx, args{}); c != 0 {
		t.Errorf("zero remote cmdPush expected 0, got %d", c)
	}
}

func TestRemoteInteractiveCenterAndPrompts(t *testing.T) {
	dir := t.TempDir()
	_ = exec.Command("git", "init", dir).Run()
	_ = exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/foo/bar.git").Run()

	r := &state.Repo{
		ID:   util.NormalizePath(dir),
		Path: dir,
		Name: "test_interactive_repo",
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	cfg := config.Default()
	cfgPath := t.TempDir() + "/config.json"
	ctx := &appCtx{
		state:   st,
		res:     res,
		cfg:     cfg,
		cfgPath: cfgPath,
		repl:    true, // 模拟 REPL 交互环境
	}

	// 1. 测试裸敲 remote 进入交互控制面板，输入 0 安全退出
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("0\n"))
	code := cmdRemote(ctx, args{})
	if code != 0 {
		t.Fatalf("cmdRemote interactive exit expected 0, got %d", code)
	}

	// 2. 测试 remote token 省略参数时，交互提示并读取输入
	testToken := "ghp_interactive_secret_123"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader(testToken + "\n"))
	code = cmdRemote(ctx, args{pos: []string{"token"}})
	if code != 0 {
		t.Fatalf("cmdRemote interactive token expected 0, got %d", code)
	}
	if recovered := ctx.cfg.ResolveToken(r.ID, "origin", "github.com"); recovered != testToken {
		t.Fatalf("expected token %s, got %s", testToken, recovered)
	}

	// 3. 测试 remote host-token 省略参数时，交互提示域名和 token
	hostDomain := "gitlab.example.com"
	hostSecret := "glpat_interactive_456"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader(hostDomain + "\n" + hostSecret + "\n"))
	code = cmdRemote(ctx, args{pos: []string{"host-token"}})
	if code != 0 {
		t.Fatalf("cmdRemote interactive host-token expected 0, got %d", code)
	}
	if recoveredHost := ctx.cfg.GetHostToken(hostDomain); recoveredHost != hostSecret {
		t.Fatalf("expected host token %s, got %s", hostSecret, recoveredHost)
	}
}

func TestMultiRemoteInteractiveSelection(t *testing.T) {
	dir := t.TempDir()
	_ = exec.Command("git", "init", dir).Run()
	_ = exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/foo/bar.git").Run()
	_ = exec.Command("git", "-C", dir, "remote", "add", "backup", "https://gitlab.com/foo/bar.git").Run()

	r := &state.Repo{
		ID:   util.NormalizePath(dir),
		Path: dir,
		Name: "test_multi_remote_repo",
	}
	st := &state.State{Repos: []*state.Repo{r}}
	sc, _ := scope.Parse("file:" + dir)
	res, _ := scope.Resolve(sc, st)

	cfg := config.Default()
	cfgPath := t.TempDir() + "/config.json"
	ctx := &appCtx{
		state:   st,
		res:     res,
		cfg:     cfg,
		cfgPath: cfgPath,
		repl:    true,
	}

	// 1. 测试在有多个远端时（git 按字母排序: 1 为 backup, 2 为 origin），
	// remote token 通过 selectMenu 选择远端 1 (backup) 并输入 token
	backupSecret := "glpat_backup_secret_999"
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("1\n" + backupSecret + "\n"))
	code := cmdRemote(ctx, args{pos: []string{"token"}})
	if code != 0 {
		t.Fatalf("cmdRemote interactive multi-remote token expected 0, got %d", code)
	}
	if tok := ctx.cfg.ResolveToken(r.ID, "backup", "gitlab.com"); tok != backupSecret {
		t.Fatalf("expected backup token %s, got %s", backupSecret, tok)
	}
	// origin 应该没有 token
	if origTok := ctx.cfg.ResolveToken(r.ID, "origin", "github.com"); origTok != "" {
		t.Fatalf("expected origin token empty, got %s", origTok)
	}

	// 2. 测试 remote clear-token 通过 selectMenu 选择远端 1 (backup)
	ctx.pipeScanner = bufio.NewScanner(strings.NewReader("1\n"))
	code = cmdRemote(ctx, args{pos: []string{"clear-token"}})
	if code != 0 {
		t.Fatalf("cmdRemote interactive multi-remote clear-token expected 0, got %d", code)
	}
	if tok := ctx.cfg.GetRepoToken(r.ID, "backup"); tok != "" {
		t.Fatalf("expected backup token to be cleared, got %s", tok)
	}
}
