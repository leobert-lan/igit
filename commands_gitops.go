package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"grscan/internal/config"
	"grscan/internal/gitinfo"
	"grscan/internal/pick"
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

func resolveRepoAuth(ctx *appCtx, r *state.Repo, rd gitinfo.RemoteDetail) gitinfo.GitAuthContext {
	user := rd.User
	if user == "" && rd.Host != "" && ctx.cfg.HostUsers != nil {
		user = ctx.cfg.HostUsers[rd.Host]
	}
	token := ctx.cfg.ResolveToken(r.ID, rd.Name, rd.Host)
	return gitinfo.GitAuthContext{
		User:  user,
		Token: token,
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

func isInteractiveMode(ctx *appCtx, a args) bool {
	if a.has("batch", "quiet") {
		return false
	}
	if ctx != nil && (ctx.repl || ctx.rl != nil) {
		return true
	}
	return isTerminal(os.Stdin)
}

func cmdRemote(ctx *appCtx, a args) int {
	r := targetRepoForOps(ctx, "remote", a)
	if r == nil {
		return 2
	}

	sub := ""
	if len(a.pos) > 0 {
		sub = strings.ToLower(a.pos[0])
	}

	interactive := isInteractiveMode(ctx, a)

	switch sub {
	case "user", "set-user":
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取远端失败: %v\n", err)
			return 1
		}
		if len(remotes) == 0 {
			fmt.Printf("仓库 %s 尚未配置任何远端，请先添加远端。\n", r.Name)
			return 1
		}

		remoteName := "origin"
		userName := ""
		argsList := a.pos[1:]
		if len(argsList) == 1 {
			userName = argsList[0]
		} else if len(argsList) >= 2 {
			remoteName = argsList[0]
			userName = argsList[1]
		}

		// 缺少用户名且处于交互模式：主动交互式询问
		if userName == "" && interactive {
			if len(remotes) > 1 {
				fmt.Println("当前可用远端:")
				for i, rd := range remotes {
					fmt.Printf("  %d) %s (%s)\n", i+1, rd.Name, rd.PushURL)
				}
				idxStr := readPrompt(ctx, fmt.Sprintf("请选择远端编号 (默认 1 / %s): ", remotes[0].Name))
				if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
					remoteName = remotes[idx-1].Name
				} else {
					remoteName = remotes[0].Name
				}
			} else {
				remoteName = remotes[0].Name
			}

			// 检查是否有 Host 记忆账号
			defaultUser := ""
			for _, rd := range remotes {
				if rd.Name == remoteName && rd.Host != "" && ctx.cfg.HostUsers != nil {
					defaultUser = ctx.cfg.HostUsers[rd.Host]
					break
				}
			}
			promptMsg := fmt.Sprintf("请输入远端 [%s] 的用户名", remoteName)
			if defaultUser != "" {
				promptMsg += fmt.Sprintf(" (回车默认使用 [%s])", defaultUser)
			}
			promptMsg += ": "
			inputUser := readPrompt(ctx, promptMsg)
			if inputUser == "" {
				userName = defaultUser
			} else {
				userName = inputUser
			}
		}

		if userName == "" {
			fmt.Println("用法: remote user [远端名] <用户名>")
			return 2
		}

		oldURL, newURL, err := gitinfo.SetRemoteUser(r.Path, ctx.cfg.GitExe, remoteName, userName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "设置失败: %v\n", err)
			return 1
		}
		_, host, _ := gitinfo.ParseRemoteURL(newURL)
		if host != "" {
			if ctx.cfg.HostUsers == nil {
				ctx.cfg.HostUsers = map[string]string{}
			}
			ctx.cfg.HostUsers[host] = userName
			_ = config.Save(ctx.cfgPath, ctx.cfg)
		}
		fmt.Printf("✅ 已更新远端 [%s]:\n  旧地址: %s\n  新地址: %s\n", remoteName, oldURL, newURL)
		if host != "" {
			fmt.Printf("💡 已记住域名 %s 常用账号为 [%s]，后续同域名仓库 push 仅需输密码。\n", host, userName)
		}
		refreshRepoState(ctx, r)
		return 0

	case "token", "set-token":
		remoteName := "origin"
		tokenVal := ""
		argsList := a.pos[1:]
		if len(argsList) == 1 {
			tokenVal = argsList[0]
		} else if len(argsList) >= 2 {
			remoteName = argsList[0]
			tokenVal = argsList[1]
		}

		if tokenVal == "" && interactive {
			remotes, _ := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
			if len(remotes) > 1 {
				fmt.Println("当前可用远端:")
				for i, rd := range remotes {
					fmt.Printf("  %d) %s (%s)\n", i+1, rd.Name, rd.PushURL)
				}
				idxStr := readPrompt(ctx, fmt.Sprintf("请选择远端编号 (默认 1 / %s): ", remotes[0].Name))
				if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
					remoteName = remotes[idx-1].Name
				} else {
					remoteName = remotes[0].Name
				}
			} else if len(remotes) == 1 {
				remoteName = remotes[0].Name
			}
			tokenVal = readPrompt(ctx, fmt.Sprintf("请输入远端 [%s] 的访问 Token (AES-256-GCM 加密存储): ", remoteName))
		}

		if tokenVal == "" {
			fmt.Println("用法: remote token [远端名] <Token>")
			return 2
		}

		if err := ctx.cfg.SetRepoToken(r.ID, remoteName, tokenVal); err != nil {
			fmt.Fprintf(os.Stderr, "保存 Token 失败: %v\n", err)
			return 1
		}
		_ = config.Save(ctx.cfgPath, ctx.cfg)
		fmt.Printf("✅ 已加密保存仓库 %s 远端 [%s] 的访问 Token (AES-256-GCM 保护)。\n后续 push/fetch/pull 将自动鉴权。\n", r.Name, remoteName)
		return 0

	case "host-token":
		host := ""
		tokenVal := ""
		if len(a.pos) >= 3 {
			host = a.pos[1]
			tokenVal = a.pos[2]
		} else if len(a.pos) == 2 {
			host = a.pos[1]
		}

		if (host == "" || tokenVal == "") && interactive {
			if host == "" {
				host = readPrompt(ctx, "请输入平台域名 (如 github.com 或 gitlab.mycorp.com): ")
			}
			if host != "" && tokenVal == "" {
				tokenVal = readPrompt(ctx, fmt.Sprintf("请输入域名 [%s] 的全局默认访问 Token: ", host))
			}
		}

		if host == "" || tokenVal == "" {
			fmt.Println("用法: remote host-token <域名> <Token>")
			fmt.Println("  例: remote host-token github.com ghp_xxxx")
			return 2
		}

		if err := ctx.cfg.SetHostToken(host, tokenVal); err != nil {
			fmt.Fprintf(os.Stderr, "保存 Host Token 失败: %v\n", err)
			return 1
		}
		_ = config.Save(ctx.cfgPath, ctx.cfg)
		fmt.Printf("✅ 已加密保存域名 [%s] 全局访问 Token (AES-256-GCM 保护)。\n", host)
		return 0

	case "clear-token":
		remoteName := "origin"
		if len(a.pos) >= 2 {
			remoteName = a.pos[1]
		} else if interactive {
			remotes, _ := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
			if len(remotes) > 1 {
				fmt.Println("选择要清除 Token 的远端:")
				for i, rd := range remotes {
					fmt.Printf("  %d) %s\n", i+1, rd.Name)
				}
				idxStr := readPrompt(ctx, "请选择编号: ")
				if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
					remoteName = remotes[idx-1].Name
				}
			}
		}
		_ = ctx.cfg.SetRepoToken(r.ID, remoteName, "")
		_ = config.Save(ctx.cfgPath, ctx.cfg)
		fmt.Printf("已清除仓库 %s 远端 [%s] 的 Token 配置。\n", r.Name, remoteName)
		return 0

	case "remember":
		host := ""
		user := ""
		if len(a.pos) >= 3 {
			host = a.pos[1]
			user = a.pos[2]
		} else if interactive {
			host = readPrompt(ctx, "请输入域名 (如 github.com): ")
			user = readPrompt(ctx, fmt.Sprintf("请输入域名 [%s] 对应的常用用户名: ", host))
		}
		if host == "" || user == "" {
			fmt.Println("用法: remote remember <域名> <用户名>")
			return 2
		}
		if ctx.cfg.HostUsers == nil {
			ctx.cfg.HostUsers = map[string]string{}
		}
		ctx.cfg.HostUsers[host] = user
		_ = config.Save(ctx.cfgPath, ctx.cfg)
		fmt.Printf("✅ 已记住域名 %s 对应常用账号为 [%s]\n", host, user)
		return 0

	case "clear-user":
		remoteName := "origin"
		if len(a.pos) >= 2 {
			remoteName = a.pos[1]
		} else if interactive {
			remotes, _ := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
			if len(remotes) > 1 {
				fmt.Println("选择要清除用户名的远端:")
				for i, rd := range remotes {
					fmt.Printf("  %d) %s\n", i+1, rd.Name)
				}
				idxStr := readPrompt(ctx, "请选择编号: ")
				if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
					remoteName = remotes[idx-1].Name
				}
			}
		}
		_, newURL, err := gitinfo.SetRemoteUser(r.Path, ctx.cfg.GitExe, remoteName, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "清除用户名失败: %v\n", err)
			return 1
		}
		fmt.Printf("已清除远端 [%s] 的嵌入用户名，当前地址: %s\n", remoteName, newURL)
		refreshRepoState(ctx, r)
		return 0

	case "add":
		name := ""
		urlStr := ""
		if len(a.pos) >= 3 {
			name = a.pos[1]
			urlStr = a.pos[2]
		} else if interactive {
			name = readPrompt(ctx, "请输入新远端名称 (默认 origin): ")
			if name == "" {
				name = "origin"
			}
			urlStr = readPrompt(ctx, "请输入远端地址 (URL): ")
		}
		if name == "" || urlStr == "" {
			fmt.Println("用法: remote add <远端名> <URL>")
			return 2
		}
		if err := gitinfo.AddRemote(r.Path, ctx.cfg.GitExe, name, urlStr); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Printf("✅ 已为仓库 %s 添加远端 [%s] -> %s\n", r.Name, name, urlStr)
		refreshRepoState(ctx, r)
		return 0

	case "rm", "remove":
		name := ""
		if len(a.pos) >= 2 {
			name = a.pos[1]
		} else if interactive {
			remotes, _ := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
			if len(remotes) == 0 {
				fmt.Println("当前仓库没有可删除的远端。")
				return 0
			}
			fmt.Println("选择要删除的远端:")
			for i, rd := range remotes {
				fmt.Printf("  %d) %s (%s)\n", i+1, rd.Name, rd.PushURL)
			}
			idxStr := readPrompt(ctx, "请选择编号: ")
			if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
				name = remotes[idx-1].Name
			}
		}
		if name == "" {
			fmt.Println("用法: remote rm <远端名>")
			return 2
		}
		if interactive && !confirmPrompt(ctx, fmt.Sprintf("确认删除远端 [%s] 吗? [y/N]: ", name)) {
			fmt.Println("已取消。")
			return 0
		}
		if err := gitinfo.RemoveRemote(r.Path, ctx.cfg.GitExe, name); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Printf("✅ 已从仓库 %s 删除远端 [%s]\n", r.Name, name)
		refreshRepoState(ctx, r)
		return 0

	case "list", "show":
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取远端失败: %v\n", err)
			return 1
		}
		printRemoteDetails(ctx, r, remotes)
		return 0

	default:
		// 只要在交互环境中敲 remote（无论是否有其他未知子参数），直接进入交互控制中心
		if interactive {
			return remoteInteractive(ctx, r)
		}
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取远端失败: %v\n", err)
			return 1
		}
		printRemoteDetails(ctx, r, remotes)
		return 0
	}
}

func printRemoteDetails(ctx *appCtx, r *state.Repo, remotes []gitinfo.RemoteDetail) {
	if len(remotes) == 0 {
		fmt.Printf("仓库 %s 尚未配置任何远端仓库。\n", r.Name)
		return
	}
	fmt.Printf("远端配置清单 (%s · %d 个远端):\n", r.Name, len(remotes))
	for _, rd := range remotes {
		userTag := "[未绑定用户]"
		if rd.User != "" {
			userTag = fmt.Sprintf("[已绑定用户: %s]", rd.User)
		} else if rd.Host != "" && ctx.cfg.HostUsers != nil {
			if remembered, ok := ctx.cfg.HostUsers[rd.Host]; ok && remembered != "" {
				userTag = fmt.Sprintf("[Host记忆账号: %s]", remembered)
			}
		}

		tokenTag := "[未配置Token]"
		if repoTok := ctx.cfg.GetRepoToken(r.ID, rd.Name); repoTok != "" {
			tokenTag = "[已配仓库专有Token(AES保护)]"
		} else if hostTok := ctx.cfg.GetHostToken(rd.Host); hostTok != "" {
			tokenTag = "[已配Host全局Token(AES保护)]"
		}

		fmt.Printf("  %-10s %s\n", rd.Name, rd.FetchURL)
		if rd.PushURL != "" && rd.PushURL != rd.FetchURL {
			fmt.Printf("             push -> %s\n", rd.PushURL)
		}
		fmt.Printf("             %s  %s\n", userTag, tokenTag)
	}
}

func remoteInteractive(ctx *appCtx, r *state.Repo) int {
	for {
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取远端失败: %v\n", err)
			return 1
		}

		fmt.Println()
		fmt.Println("╔═══════════════════════════════════════════════════════════════════╗")
		fmt.Printf("║               远端管理控制中心: %-33s ║\n", r.Name)
		fmt.Println("╚═══════════════════════════════════════════════════════════════════╝")
		printRemoteDetails(ctx, r, remotes)
		fmt.Println()
		fmt.Println("操作选项:")
		fmt.Println("  [1] 👤 绑定远端用户名 (Username)      - 写入 URL，免输用户名")
		fmt.Println("  [2] 🔑 配置远端访问令牌 (Token)        - AES-256-GCM 加密，Push/Pull 自动鉴权")
		fmt.Println("  [3] 🌐 配置平台域名默认账号与 Token  - 如 github.com 全局默认凭据")
		fmt.Println("  [4] ➕ 添加新远端 (git remote add)     - 添加镜像/备份远端")
		fmt.Println("  [5] ➖ 删除远端 (git remote remove)  - 移除指定远端")
		fmt.Println("  [6] 🧹 清除指定远端用户或 Token")
		fmt.Println("  [0] 返回 / 退出管理中心")
		fmt.Println()

		choice := readPrompt(ctx, "请选择操作 (1-6 / 0 退出): ")
		switch choice {
		case "1":
			cmdRemote(ctx, args{pos: []string{"user"}})
		case "2":
			cmdRemote(ctx, args{pos: []string{"token"}})
		case "3":
			cmdRemote(ctx, args{pos: []string{"host-token"}})
		case "4":
			cmdRemote(ctx, args{pos: []string{"add"}})
		case "5":
			cmdRemote(ctx, args{pos: []string{"rm"}})
		case "6":
			interactiveClearAuth(ctx, r, remotes)
		case "0", "q", "quit", "exit", "":
			return 0
		default:
			fmt.Println("无效选项，请输入 1-6 或 0。")
		}
	}
}

func interactiveClearAuth(ctx *appCtx, r *state.Repo, remotes []gitinfo.RemoteDetail) {
	if len(remotes) == 0 {
		fmt.Println("当前仓库无远端配置。")
		return
	}
	fmt.Println("清除认证信息:")
	fmt.Println("  1) 清除远端 URL 中的用户名")
	fmt.Println("  2) 清除远端专属加密 Token")
	fmt.Println("  0) 取消返回")
	sub := readPrompt(ctx, "请选择 (1-2 / 0 取消): ")
	if sub != "1" && sub != "2" {
		return
	}
	targetName := remotes[0].Name
	if len(remotes) > 1 {
		fmt.Println("选择目标远端:")
		for i, rd := range remotes {
			fmt.Printf("  %d) %s\n", i+1, rd.Name)
		}
		idxStr := readPrompt(ctx, "请选择编号: ")
		if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 1 && idx <= len(remotes) {
			targetName = remotes[idx-1].Name
		}
	}
	if sub == "1" {
		cmdRemote(ctx, args{pos: []string{"clear-user", targetName}})
	} else if sub == "2" {
		cmdRemote(ctx, args{pos: []string{"clear-token", targetName}})
	}
}

// ---------------------------------------------------------------- fetch

func cmdFetch(ctx *appCtx, a args) int {
	// 集合作用域且有多个仓库，走批量抓取
	if ctx.res != nil && ctx.res.Kind != scope.KindRepo && len(ctx.res.Repos) > 1 && !a.has("repo") && len(a.pos) == 0 {
		return batchFetch(ctx, a)
	}

	r := targetRepoForOps(ctx, "fetch", a)
	if r == nil {
		return 2
	}

	remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取远端列表失败: %v\n", err)
		return 1
	}
	if len(remotes) == 0 {
		fmt.Printf("⚠️ 仓库 %s 尚未配置任何远端仓库 (remote)。\n", r.Name)
		fmt.Println("提示: 可输入 'remote' 进行交互式配置，或执行 'git remote add <名称> <URL>'。")
		return 0
	}

	// 1. 命令行显式指定了远端，如 fetch origin
	if len(a.pos) > 0 {
		targetName := a.pos[0]
		var targetRD *gitinfo.RemoteDetail
		for i := range remotes {
			if strings.EqualFold(remotes[i].Name, targetName) {
				targetRD = &remotes[i]
				break
			}
		}
		if targetRD == nil {
			fmt.Fprintf(os.Stderr, "未找到名为 %s 的远端配置。\n", targetName)
			return 1
		}
		return fetchSingleRemote(ctx, r, *targetRD)
	}

	// 2. 单远端直通
	if len(remotes) == 1 {
		return fetchSingleRemote(ctx, r, remotes[0])
	}

	// 3. 多远端全量抓取
	fmt.Printf("仓库 %s 检测到 %d 个远端，开始全量抓取更新...\n", r.Name, len(remotes))
	success, failed := 0, 0
	for _, rd := range remotes {
		auth := resolveRepoAuth(ctx, r, rd)
		tokenHint := ""
		if auth.Token != "" {
			tokenHint = " [已注入Token]"
		}
		fmt.Printf("  -> 正在抓取 [%s] (%s)%s...\n", rd.Name, rd.FetchURL, tokenHint)
		msg, err := gitinfo.GitFetchWithAuth(r.Path, ctx.cfg.GitExe, rd.Name, auth)
		if err != nil {
			fmt.Fprintf(os.Stderr, "     [%s] 抓取失败: %v\n", rd.Name, err)
			failed++
		} else {
			if msg != "" {
				fmt.Printf("     %s\n", msg)
			}
			success++
		}
	}
	refreshRepoState(ctx, r)
	fmt.Printf("全量拉取完成: 成功 %d 个远端，失败 %d 个。\n", success, failed)
	printTopologySummary(r)
	return 0
}

func fetchSingleRemote(ctx *appCtx, r *state.Repo, rd gitinfo.RemoteDetail) int {
	auth := resolveRepoAuth(ctx, r, rd)
	tokenHint := ""
	if auth.Token != "" {
		tokenHint = " (采用配置 Token 免密)"
	}
	fmt.Printf("正在拉取 %s 远端 [%s] 更新%s...\n", r.Name, rd.Name, tokenHint)

	var err error
	if isTerminal(os.Stdin) {
		err = gitinfo.GitFetchInteractiveWithAuth(r.Path, ctx.cfg.GitExe, rd.Name, auth)
	} else {
		var msg string
		msg, err = gitinfo.GitFetchWithAuth(r.Path, ctx.cfg.GitExe, rd.Name, auth)
		if msg != "" {
			fmt.Println(msg)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch 失败: %v\n", err)
		return 1
	}
	refreshRepoState(ctx, r)
	printTopologySummary(r)
	return 0
}

func printTopologySummary(r *state.Repo) {
	fmt.Printf("当前状态: ")
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
		remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
		if err != nil || len(remotes) == 0 {
			continue
		}
		auth := resolveRepoAuth(ctx, r, remotes[0])
		_, err = gitinfo.GitFetchWithAuth(r.Path, ctx.cfg.GitExe, "", auth)
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

	// 2. 状态检查（在单元测试或无真实 git 时，通过已记录的 Ahead/Behind 快速预检）
	if r.Ahead > 0 && r.Behind > 0 {
		fmt.Fprintf(os.Stderr, "⚠️ 快进原则拦截: 本地与远端已分叉 (领先 %d, 落后 %d)。\n已拦截合并，请使用可视化工具 (如 tig / IDE) 处理。\n",
			r.Ahead, r.Behind)
		return 1
	}

	remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取远端列表失败: %v\n", err)
		return 1
	}
	if len(remotes) == 0 {
		fmt.Printf("⚠️ 仓库 %s 尚未配置任何远端仓库 (remote)，无法拉取。\n", r.Name)
		return 0
	}

	info, _ := gitinfo.GetUpstreamInfo(r.Path, ctx.cfg.GitExe)
	var targetRD gitinfo.RemoteDetail

	// 3. 确定拉取源远端 (单选)
	if len(a.pos) > 0 {
		targetName := a.pos[0]
		found := false
		for _, rd := range remotes {
			if strings.EqualFold(rd.Name, targetName) {
				targetRD = rd
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "未找到名为 %s 的远端配置。\n", targetName)
			return 1
		}
	} else if len(remotes) == 1 {
		targetRD = remotes[0]
	} else {
		// 多远端交互式单选
		var menuItems []pick.MenuItem
		for i, rd := range remotes {
			badge := ""
			if info.HasUpstream && strings.HasPrefix(info.Upstream, rd.Name+"/") {
				badge = "[当前追踪]"
			}
			menuItems = append(menuItems, pick.MenuItem{
				Key:    strconv.Itoa(i + 1),
				Label:  rd.Name,
				Badge:  badge,
				Detail: rd.FetchURL,
				Value:  rd,
			})
		}
		chosen, cancelled := pick.Menu(pick.MenuOpt{
			Title:       fmt.Sprintf("仓库 %s 包含 %d 个远端，请选择拉取源 (单选 · 方向键移动，回车确认):", r.Name, len(remotes)),
			Items:       menuItems,
			Interactive: isTerminal(os.Stdin),
		})
		if cancelled || chosen == nil {
			fmt.Println("已取消拉取。")
			return 0
		}
		targetRD = chosen.Value.(gitinfo.RemoteDetail)
	}

	// 4. 快进原则与分叉提前预检
	branch := r.Branch
	if branch == "" {
		branch = info.Branch
	}
	if branch != "" {
		ahead, behind, hasRemoteBranch, err := gitinfo.CheckFastForwardStatus(r.Path, ctx.cfg.GitExe, targetRD.Name, branch)
		if err == nil && hasRemoteBranch {
			if ahead > 0 && behind > 0 {
				fmt.Fprintf(os.Stderr, "⚠️ 快进原则拦截: 本地分支 [%s] 与远端 [%s/%s] 已分叉 (本地领先 %d, 落后 %d 个提交)！\n已阻止执行 merge，请使用 JetBrains 或其它可视化工具处理。\n",
					branch, targetRD.Name, branch, ahead, behind)
				return 1
			}
			if behind == 0 && ahead == 0 {
				fmt.Printf("当前分支 [%s] 与远端 [%s] 保持最新，无需拉取。\n", branch, targetRD.Name)
				return 0
			}
		}
	}

	// 5. 执行安全快进拉取
	auth := resolveRepoAuth(ctx, r, targetRD)
	tokenHint := ""
	if auth.Token != "" {
		tokenHint = " (采用配置 Token 免密)"
	}
	fmt.Printf("正在安全快进拉取 [%s] (%s)%s...\n", targetRD.Name, r.Name, tokenHint)
	pullErr := gitinfo.GitPullFFOnlyWithAuth(r.Path, ctx.cfg.GitExe, targetRD.Name, branch, auth)
	if pullErr != nil {
		fmt.Fprintf(os.Stderr, "拉取失败 (非快进或冲突): %v\n", pullErr)
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

	remotes, err := gitinfo.GetRemotesDetailed(r.Path, ctx.cfg.GitExe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取远端列表失败: %v\n", err)
		return 1
	}
	if len(remotes) == 0 {
		fmt.Printf("⚠️ 仓库 %s 尚未配置任何远端仓库 (remote)，无法推送。\n", r.Name)
		fmt.Println("提示: 可输入 'remote' 进行交互式配置，或执行 'git remote add <名称> <URL>'。")
		return 0
	}

	info, _ := gitinfo.GetUpstreamInfo(r.Path, ctx.cfg.GitExe)
	var targets []gitinfo.RemoteDetail

	// 1. 命令行显式指定了远端，如 push origin
	if len(a.pos) > 0 {
		targetName := a.pos[0]
		for _, rd := range remotes {
			if strings.EqualFold(rd.Name, targetName) {
				targets = append(targets, rd)
				break
			}
		}
		if len(targets) == 0 {
			fmt.Fprintf(os.Stderr, "未找到名为 %s 的远端配置。\n", targetName)
			return 1
		}
	} else if len(remotes) == 1 {
		targets = remotes
	} else {
		// 多远端交互式多选 (Bubble Tea 复选菜单)
		var menuItems []pick.MenuItem
		for i, rd := range remotes {
			selected := (strings.EqualFold(rd.Name, "origin") || (info.HasUpstream && strings.HasPrefix(info.Upstream, rd.Name+"/")))
			badge := ""
			auth := resolveRepoAuth(ctx, r, rd)
			if auth.Token != "" {
				badge = "[Token免密]"
			}
			detail := rd.PushURL
			if detail == "" {
				detail = rd.FetchURL
			}
			menuItems = append(menuItems, pick.MenuItem{
				Key:      strconv.Itoa(i + 1),
				Label:    rd.Name,
				Badge:    badge,
				Detail:   detail,
				Selected: selected,
				Value:    rd,
			})
		}
		picked, cancelled := pick.MultiMenu(pick.MenuOpt{
			Title:       fmt.Sprintf("仓库 %s 包含 %d 个远端，请选择推送目标 (空格勾选/取消，回车确认):", r.Name, len(remotes)),
			Items:       menuItems,
			Interactive: isTerminal(os.Stdin),
		})
		if cancelled || len(picked) == 0 {
			fmt.Println("已取消推送。")
			return 0
		}
		for _, it := range picked {
			targets = append(targets, it.Value.(gitinfo.RemoteDetail))
		}
	}

	// 2. 检查跟踪分支提示
	if !info.HasUpstream {
		branch := info.Branch
		if branch == "" {
			branch = r.Branch
		}
		fmt.Printf("💡 提示: 当前分支尚未建立远端跟踪关系 (可执行: git push --set-upstream %s %s)\n", targets[0].Name, branch)
	}

	extraArgs := []string{}
	if len(a.pos) > 1 {
		extraArgs = a.pos[1:]
	}

	// 3. 依次推送到目标远端
	for _, target := range targets {
		auth := resolveRepoAuth(ctx, r, target)
		tokenHint := "如有密码提示请输入:"
		if auth.Token != "" {
			tokenHint = "采用配置 Token 免密推送..."
		}
		fmt.Printf("正在推送到远端 [%s] (%s)... %s\n", target.Name, target.PushURL, tokenHint)
		err := gitinfo.GitPushWithAuth(r.Path, ctx.cfg.GitExe, target.Name, "", extraArgs, auth)
		if err != nil {
			fmt.Fprintf(os.Stderr, "远端 [%s] 推送失败: %v\n", target.Name, err)
		} else {
			fmt.Printf("远端 [%s] 推送成功！\n", target.Name)
		}
	}
	refreshRepoState(ctx, r)
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
