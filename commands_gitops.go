package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"grscan/internal/config"
	"grscan/internal/gitinfo"
	"grscan/internal/scope"
	"grscan/internal/state"
)

// targetRepoForOps 获取当前命令要作用的具体仓库。
// 如果已在单仓节点上，直接使用当前仓；否则通过 kw 或 --repo 查找。
func targetRepoForOps(ctx *appCtx, cmd string, a args) *state.Repo {
	if ctx != nil && ctx.res != nil && ctx.res.TargetRepo() != nil {
		return ctx.res.TargetRepo()
	}
	repoKw := a.str("", "repo")
	if repoKw != "" {
		return pickTarget(ctx, cmd, repoKw)
	}
	kw := ""
	if len(a.pos) > 0 {
		kw = a.pos[0]
	}
	return pickTarget(ctx, cmd, kw)
}

// refreshRepoState 原位刷新仓库客观状态并持久化。
func refreshRepoState(ctx *appCtx, r *state.Repo) {
	if ctx == nil || r == nil {
		return
	}
	updated := gitinfo.Inspect(r.Path, r.Kind, ctx.cfg.GitExe, true)
	if updated != nil {
		r.Branch = updated.Branch
		r.Detached = updated.Detached
		r.Commit = updated.Commit
		r.CommitAt = updated.CommitAt
		r.Author = updated.Author
		r.Subject = updated.Subject
		r.Dirty = updated.Dirty
		r.Staged = updated.Staged
		r.Unstaged = updated.Unstaged
		r.Untracked = updated.Untracked
		r.Ahead = updated.Ahead
		r.Behind = updated.Behind
		r.HasUpstream = updated.HasUpstream
		r.Remotes = updated.Remotes
		r.PrimaryURL = updated.PrimaryURL
		r.LastSeen = time.Now().Format(time.RFC3339)
		if ctx.state != nil {
			_ = ctx.state.Save()
		}
	}
}

// ---------------------------------------------------------------- ignore

func cmdIgnore(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "ignore", a)
	if r == nil {
		return 2
	}

	sub := ""
	var rest []string
	if len(a.pos) > 0 {
		sub = strings.ToLower(a.pos[0])
		rest = a.pos[1:]
	}

	switch sub {
	case "", "show", "list":
		lines, exists, err := gitinfo.ReadIgnore(r.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 .gitignore 失败: %v\n", err)
			return 1
		}
		if !exists {
			fmt.Printf("仓库 %s 尚无 .gitignore 文件。\n  敲 ignore add <规则> 可自动创建并追加规则 (支持 Tab 补全)。\n", r.Name)
			return 0
		}
		fmt.Printf(".gitignore 规则清单 (%s):\n", r.Name)
		count := 0
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			fmt.Printf("  %s\n", trimmed)
			count++
		}
		fmt.Printf("(共 %d 条生效规则，敲 ignore add 追加，敲 ignore untracked 看未跟踪项)\n", count)
		return 0

	case "untracked":
		items, err := gitinfo.GetUntrackedItems(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取未跟踪项失败: %v\n", err)
			return 1
		}
		if len(items) == 0 {
			fmt.Printf("仓库 %s 工作区没有未跟踪的文件或目录 (干净)。\n", r.Name)
			return 0
		}
		fmt.Printf("未跟踪项清单 (%d 个):\n", len(items))
		for _, it := range items {
			kind := "文件"
			if it.IsDir {
				kind = "目录"
			}
			fmt.Printf("  [%s] %s\n", kind, it.Path)
		}
		fmt.Println("提示: 敲 ignore add <Tab> 可快速将上述项加入 .gitignore")
		return 0

	case "add":
		if len(rest) == 0 {
			fmt.Println("用法: igit ignore add <模式1> [模式2...]")
			fmt.Println("  例: igit ignore add __pycache__/ *.pyc .env")
			fmt.Println("  (支持在 REPL 中敲 Tab 自动补全未跟踪项)")
			return 2
		}
		added, err := gitinfo.AppendIgnore(r.Path, rest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "追加规则失败: %v\n", err)
			return 1
		}
		if len(added) == 0 {
			fmt.Println("所给规则已存在于 .gitignore 中，无需重复添加。")
		} else {
			fmt.Printf("成功追加 %d 条规则到 .gitignore: %s\n", len(added), strings.Join(added, " "))
			refreshRepoState(ctx, r)
		}
		return 0

	default:
		// 容错：如果用户直接敲 ignore __pycache__/ 而没敲 add，自动视为 add
		patterns := a.pos
		added, err := gitinfo.AppendIgnore(r.Path, patterns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "追加规则失败: %v\n", err)
			return 1
		}
		if len(added) == 0 {
			fmt.Println("所给规则已存在于 .gitignore 中，无需重复添加。")
		} else {
			fmt.Printf("成功追加 %d 条规则到 .gitignore: %s\n", len(added), strings.Join(added, " "))
			refreshRepoState(ctx, r)
		}
		return 0
	}
}

// ---------------------------------------------------------------- remote

func cmdRemote(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "remote", a)
	if r == nil {
		return 2
	}

	sub := ""
	if len(a.pos) > 0 {
		sub = strings.ToLower(a.pos[0])
	}

	switch sub {
	case "user", "set-user":
		// remote user [remote_name] [username]
		// 如果只给了一个参数，则视为 username（remote 默认为 origin）
		remoteName := "origin"
		userName := ""
		argsList := a.pos[1:]
		if len(argsList) == 1 {
			userName = argsList[0]
		} else if len(argsList) >= 2 {
			remoteName = argsList[0]
			userName = argsList[1]
		} else {
			// 未给任何参数：尝试从配置中寻找已记住的 Host 账号
			remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
			if err == nil && len(remotes) > 0 {
				h := remotes[0].Host
				if remembered, ok := ctx.cfg.HostUsers[h]; ok && remembered != "" {
					userName = remembered
					remoteName = remotes[0].Name
					fmt.Printf("采用域名 %s 已记忆的账号 [%s]\n", h, remembered)
				}
			}
		}
		if userName == "" {
			fmt.Println("用法: remote user [远端名] <用户名>")
			fmt.Println("  例: remote user zhangsan")
			fmt.Println("  例: remote user origin zhangsan")
			return 2
		}
		oldURL, newURL, err := gitinfo.SetRemoteUser(r.Path, ctx.cfg.GitExe, remoteName, userName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "设置失败: %v\n", err)
			return 1
		}
		// 提取 host 并记录到全局配置
		_, host, _ := gitinfo.ParseRemoteURL(newURL)
		if host != "" {
			if ctx.cfg.HostUsers == nil {
				ctx.cfg.HostUsers = map[string]string{}
			}
			ctx.cfg.HostUsers[host] = userName
			_ = config.Save(ctx.cfgPath, ctx.cfg)
		}
		fmt.Printf("已更新远端 %s:\n", remoteName)
		fmt.Printf("  旧地址: %s\n", oldURL)
		fmt.Printf("  新地址: %s\n", newURL)
		if host != "" {
			fmt.Printf("💡 已记住域名 %s 常用账号为 [%s]，后续同域名仓库 push 仅需输密码。\n", host, userName)
		}
		refreshRepoState(ctx, r)
		return 0

	case "remember":
		// remote remember <host> <user>
		if len(a.pos) < 3 {
			fmt.Println("用法: remote remember <域名> <用户名>")
			fmt.Println("  例: remote remember gitlab.mycompany.com zhangsan")
			return 2
		}
		host := a.pos[1]
		user := a.pos[2]
		if ctx.cfg.HostUsers == nil {
			ctx.cfg.HostUsers = map[string]string{}
		}
		ctx.cfg.HostUsers[host] = user
		_ = config.Save(ctx.cfgPath, ctx.cfg)
		fmt.Printf("已记住域名 %s 对应常用账号为 [%s]\n", host, user)
		return 0

	case "clear-user":
		remoteName := "origin"
		if len(a.pos) >= 2 {
			remoteName = a.pos[1]
		}
		_, newURL, err := gitinfo.SetRemoteUser(r.Path, ctx.cfg.GitExe, remoteName, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "清除用户名失败: %v\n", err)
			return 1
		}
		fmt.Printf("已清除远端 %s 的嵌入用户名，当前地址: %s\n", remoteName, newURL)
		refreshRepoState(ctx, r)
		return 0

	default:
		// 默认 list 远端详情
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取远端失败: %v\n", err)
			return 1
		}
		if len(remotes) == 0 {
			fmt.Printf("仓库 %s 尚未配置任何远端仓库。\n", r.Name)
			return 0
		}
		fmt.Printf("远端配置清单 (%s · %d 个远端):\n", r.Name, len(remotes))
		var suggestHost, suggestUser, suggestRemote string
		for _, rd := range remotes {
			userTag := "[未绑定用户]"
			if rd.User != "" {
				userTag = fmt.Sprintf("[已绑定用户: %s · Push免输用户]", rd.User)
			} else if rd.Host != "" && ctx.cfg.HostUsers != nil {
				if remembered, ok := ctx.cfg.HostUsers[rd.Host]; ok && remembered != "" {
					suggestHost = rd.Host
					suggestUser = remembered
					suggestRemote = rd.Name
				}
			}
			fmt.Printf("  %-10s %s\n", rd.Name, rd.FetchURL)
			if rd.PushURL != "" && rd.PushURL != rd.FetchURL {
				fmt.Printf("             push -> %s\n", rd.PushURL)
			}
			fmt.Printf("             %s\n", userTag)
		}
		if suggestUser != "" {
			fmt.Printf("\n💡 提示: 域名 %s 记忆的常用账号为 [%s]，可敲  remote user %s %s  一键绑定\n",
				suggestHost, suggestUser, suggestRemote, suggestUser)
		}
		return 0
	}
}

// ---------------------------------------------------------------- fetch

func cmdFetch(ctx *appCtx, a args) int {
	// 如果是集合作用域且有多个仓库，支持批量抓取
	if ctx.res != nil && ctx.res.Kind != scope.KindRepo && len(ctx.res.Repos) > 1 && !a.has("repo") && len(a.pos) == 0 {
		return batchFetch(ctx, a)
	}

	r := targetRepoForOps(ctx, "fetch", a)
	if r == nil {
		return 2
	}

	remote := ""
	if len(a.pos) > 0 {
		remote = a.pos[0]
	}

	fmt.Printf("正在拉取 %s 远端更新...\n", r.Name)
	var err error
	if isTerminal(os.Stdin) {
		err = gitinfo.GitFetchInteractive(r.Path, ctx.cfg.GitExe, remote)
	} else {
		var msg string
		msg, err = gitinfo.GitFetch(r.Path, ctx.cfg.GitExe, remote)
		if msg != "" {
			fmt.Println(msg)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch 失败: %v\n", err)
		return 1
	}

	refreshRepoState(ctx, r)

	// 打印直观拓扑结论
	fmt.Printf("拉取完成。当前状态: ")
	switch {
	case r.Ahead == 0 && r.Behind == 0:
		fmt.Println("与远端保持最新。")
	case r.Behind > 0 && r.Ahead == 0:
		fmt.Printf("落后远端 %d 个提交，本地干净 (可安全敲 pull 快进拉取)。\n", r.Behind)
	case r.Ahead > 0 && r.Behind == 0:
		fmt.Printf("领先远端 %d 个提交 (可敲 push 推送)。\n", r.Ahead)
	case r.Ahead > 0 && r.Behind > 0:
		fmt.Printf("⚠️ 本地领先 %d 且落后 %d 个提交，两端已分叉！\n  已阻止自动 pull，请使用可视化工具 (如 JetBrains / tig) 进行合并。\n", r.Ahead, r.Behind)
	default:
		fmt.Printf("分支 %s\n", r.Branch)
	}
	return 0
}

func batchFetch(ctx *appCtx, a args) int {
	pool := scopeRepos(ctx)
	if len(pool) == 0 {
		fmt.Println("当前作用域内没有仓库。")
		return 0
	}
	fmt.Printf("开始批量拉取当前作用域内的 %d 个仓库...\n", len(pool))
	success, failed := 0, 0
	for _, r := range pool {
		if r.PrimaryURL == "" {
			continue
		}
		_, err := gitinfo.GitFetch(r.Path, ctx.cfg.GitExe, "")
		if err != nil {
			failed++
			continue
		}
		refreshRepoState(ctx, r)
		success++
	}
	fmt.Printf("批量拉取完毕: 成功 %d，失败/无远端跳过 %d\n", success, failed)
	return 0
}

// ---------------------------------------------------------------- pull

func cmdPull(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "pull", a)
	if r == nil {
		return 2
	}

	// 1. 脏工作区检查（绝不污染未提交代码）
	if r.Dirty {
		fmt.Fprintf(os.Stderr, "工作区有未提交的改动 (暂存:%d, 未暂存:%d, 未跟踪:%d)。\n已拦截 pull，请先 commit 提交或 ignore 干净，避免冲突覆盖。\n",
			r.Staged, r.Unstaged, r.Untracked)
		return 1
	}

	// 2. 分叉检查（绝不命令行隐式 Merge）
	if r.Ahead > 0 && r.Behind > 0 {
		fmt.Fprintf(os.Stderr, "⚠️ 本地与远端已分叉 (领先 %d, 落后 %d)。\n命令行已拦截合并，请使用 JetBrains 或其它可视化工具处理 (可敲 open 打开目录)。\n",
			r.Ahead, r.Behind)
		return 1
	}

	if r.Behind == 0 && r.HasUpstream {
		fmt.Println("已经是最新版本，无需拉取。")
		return 0
	}

	fmt.Printf("正在安全快进拉取 (%s)...\n", r.Name)
	var pullErr error
	if isTerminal(os.Stdin) {
		pullErr = gitinfo.GitPullFFOnlyInteractive(r.Path, ctx.cfg.GitExe)
	} else {
		var out string
		out, pullErr = gitinfo.GitPullFFOnly(r.Path, ctx.cfg.GitExe)
		if out != "" {
			fmt.Println(out)
		}
	}
	if pullErr != nil {
		fmt.Fprintf(os.Stderr, "%v\n", pullErr)
		return 1
	}
	refreshRepoState(ctx, r)
	fmt.Println("快进同步完成。")
	return 0
}

// ---------------------------------------------------------------- commit

func cmdCommit(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "commit", a)
	if r == nil {
		return 2
	}

	if !r.Dirty {
		fmt.Printf("仓库 %s 工作区是干净的，没有需要提交的改动。\n", r.Name)
		return 0
	}

	msg := a.str("", "m", "message")
	if msg == "" && len(a.pos) > 0 {
		msg = strings.Join(a.pos, " ")
	}

	if msg == "" {
		if ctx.rl != nil {
			ctx.rl.SetPrompt("请输入提交说明: ")
			line, err := ctx.rl.Readline()
			ctx.rl.SetPrompt(prompt(ctx))
			if err != nil || strings.TrimSpace(line) == "" {
				fmt.Println("已取消提交。")
				return 0
			}
			msg = strings.TrimSpace(line)
		} else if isTerminal(os.Stdin) {
			fmt.Print("请输入提交说明: ")
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil || strings.TrimSpace(line) == "" {
				fmt.Println("已取消提交。")
				return 0
			}
			msg = strings.TrimSpace(line)
		} else {
			fmt.Fprintln(os.Stderr, "缺少提交说明。用法: igit commit \"提交说明\"")
			return 2
		}
	}

	fmt.Printf("正在全量提交改动 (add -A & commit)...\n")
	out, err := gitinfo.GitCommitAll(r.Path, ctx.cfg.GitExe, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if out != "" {
		fmt.Println(out)
	}
	refreshRepoState(ctx, r)
	cShort := r.Commit
	if len(cShort) > 7 {
		cShort = cShort[:7]
	}
	fmt.Printf("提交成功: [%s] %s\n", cShort, r.Subject)
	return 0
}

// ---------------------------------------------------------------- push

func cmdPush(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "push", a)
	if r == nil {
		return 2
	}

	// 1. 检查跟踪分支
	info, err := gitinfo.GetUpstreamInfo(r.Path, ctx.cfg.GitExe)
	if err == nil && !info.HasUpstream {
		branch := info.Branch
		if branch == "" {
			branch = r.Branch
		}
		fmt.Println("远端尚未建立追踪分支，请执行:")
		fmt.Printf("  git push --set-upstream origin %s\n", branch)
		return 0
	}

	// 2. 检查远端用户名绑定
	remotes, _ := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
	if len(remotes) > 0 && remotes[0].IsHTTPS && remotes[0].User == "" {
		h := remotes[0].Host
		if remembered, ok := ctx.cfg.HostUsers[h]; ok && remembered != "" {
			fmt.Printf("💡 提示: 远端尚未绑定用户名，后续可敲  remote user %s  绑定已记忆账号 [%s]，免输用户名。\n", remotes[0].Name, remembered)
		}
	}

	fmt.Printf("正在推送到远端 (%s)... 如有密码提示请输入:\n", r.Name)
	exe := gitinfo.ResolveGit(ctx.cfg.GitExe)
	cmdArgs := []string{"-C", r.Path, "push"}
	if len(a.pos) > 0 {
		cmdArgs = append(cmdArgs, a.pos...)
	}
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "推送失败: %v\n", err)
		return 1
	}
	refreshRepoState(ctx, r)
	fmt.Println("推送成功！")
	return 0
}

// ---------------------------------------------------------------- 补全辅助

func untrackedCandidates(ctx *appCtx) []string {
	if ctx == nil {
		return nil
	}
	r := ctx.res.TargetRepo()
	if r == nil {
		return nil
	}
	items, err := gitinfo.GetUntrackedItems(r.Path, ctx.cfg.GitExe)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

func remoteUserCandidates(ctx *appCtx) []string {
	if ctx == nil || ctx.cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, u := range ctx.cfg.HostUsers {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}
