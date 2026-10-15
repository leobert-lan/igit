package main

import (
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
