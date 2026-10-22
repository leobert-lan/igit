package gitinfo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"grscan/internal/util"
)

// RemoteDetail 单个远端配置详情。
type RemoteDetail struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetch_url"`
	PushURL  string `json:"push_url"`
	User     string `json:"user,omitempty"`
	Host     string `json:"host,omitempty"`
	IsHTTPS  bool   `json:"is_https"`
}

// ParseRemoteURL 从远端 URL 中解析出用户名、主机名以及是否为 HTTP(S)。
func ParseRemoteURL(raw string) (user, host string, isHTTPS bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err == nil {
			isHTTPS = true
			host = u.Host
			if u.User != nil {
				user = u.User.Username()
				// u.Host 可能包含端口，提取真正的 host
				if h := u.Hostname(); h != "" {
					host = h
				}
			} else if idx := strings.Index(host, "@"); idx >= 0 {
				user = host[:idx]
				host = host[idx+1:]
			}
			return user, host, isHTTPS
		}
	}
	// 支持 SCP 类似语法：git@github.com:user/repo.git
	if strings.Contains(raw, "@") && strings.Contains(raw, ":") && !strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, "@", 2)
		user = parts[0]
		hostPart := parts[1]
		if colon := strings.Index(hostPart, ":"); colon >= 0 {
			host = hostPart[:colon]
		} else {
			host = hostPart
		}
		return user, host, false
	}
	return "", "", false
}

// BuildRemoteURLWithUser 为 HTTPS URL 注入或替换用户名。
// 如果 newUser 为空，则移除 URL 中的用户名。
func BuildRemoteURLWithUser(rawURL, newUser string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", fmt.Errorf("只支持为 HTTP/HTTPS 协议远端注入用户名: %s", rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	newUser = strings.TrimSpace(newUser)
	if newUser == "" {
		u.User = nil
	} else {
		u.User = url.User(newUser)
	}
	return u.String(), nil
}

// GetRemotesDetailed 读取仓库所有远端详细信息。
func GetRemotesDetailed(dir, gitExe string) ([]RemoteDetail, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return nil, errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-C", dir, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取远端列表失败: %v", err)
	}

	remotesMap := map[string]*RemoteDetail{}
	order := []string{}

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		rawURL := fields[1]
		kind := ""
		if len(fields) >= 3 {
			kind = strings.Trim(fields[2], "()")
		}

		rd, exists := remotesMap[name]
		if !exists {
			u, h, isHTTPS := ParseRemoteURL(rawURL)
			rd = &RemoteDetail{
				Name:    name,
				User:    u,
				Host:    h,
				IsHTTPS: isHTTPS,
			}
			remotesMap[name] = rd
			order = append(order, name)
		}
		if kind == "push" {
			rd.PushURL = rawURL
		} else {
			rd.FetchURL = rawURL
		}
	}

	res := make([]RemoteDetail, 0, len(order))
	for _, name := range order {
		rd := remotesMap[name]
		if rd.PushURL == "" {
			rd.PushURL = rd.FetchURL
		}
		if rd.FetchURL == "" {
			rd.FetchURL = rd.PushURL
		}
		// 再次检查 push url 是否有 user
		if rd.User == "" && rd.PushURL != "" {
			u, h, isH := ParseRemoteURL(rd.PushURL)
			if u != "" {
				rd.User = u
			}
			if h != "" {
				rd.Host = h
			}
			rd.IsHTTPS = rd.IsHTTPS || isH
		}
		res = append(res, *rd)
	}
	return res, nil
}

// SetRemoteUser 将当前仓库指定 remote 的 URL 更新为带有新 username 的 HTTPS 地址。
func SetRemoteUser(dir, gitExe, remoteName, newUser string) (oldURL, newURL string, err error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return "", "", errors.New("未找到 git 可执行文件")
	}
	remotes, err := GetRemotesDetailed(dir, exe)
	if err != nil {
		return "", "", err
	}
	var target *RemoteDetail
	for i := range remotes {
		if strings.EqualFold(remotes[i].Name, remoteName) {
			target = &remotes[i]
			break
		}
	}
	if target == nil {
		return "", "", fmt.Errorf("找不到名为 %s 的远端", remoteName)
	}
	base := target.PushURL
	if base == "" {
		base = target.FetchURL
	}
	if !target.IsHTTPS {
		return "", "", fmt.Errorf("远端 %s 不是 HTTP/HTTPS 协议 (%s)，无法注入用户名", remoteName, base)
	}

	updated, err := BuildRemoteURLWithUser(base, newUser)
	if err != nil {
		return "", "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "-C", dir, "remote", "set-url", remoteName, updated)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("设置 remote url 失败: %s (%v)", strings.TrimSpace(string(out)), err)
	}

	// 如果原来单独配了 pushurl，也一并更新
	if target.PushURL != "" && target.PushURL != target.FetchURL {
		pushUpdated, _ := BuildRemoteURLWithUser(target.PushURL, newUser)
		if pushUpdated != "" {
			_ = exec.CommandContext(ctx, exe, "-C", dir, "remote", "set-url", "--push", remoteName, pushUpdated).Run()
		}
	}

	return base, updated, nil
}

// UntrackedItem 未跟踪项（目录或文件）。
type UntrackedItem struct {
	Path  string // 相对路径，如 "__pycache__/" 或 "foo.txt"
	IsDir bool
}

// GetUntrackedItems 获取当前仓库工作区中未被跟踪的目录与文件（供 Tab 补全及排查）。
func GetUntrackedItems(dir, gitExe string) ([]UntrackedItem, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return nil, errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// --untracked-files=normal 模式下，整块未跟踪的目录会以 "?? dir/" 出现，非常适合直接作为 ignore 候选
	cmd := exec.CommandContext(ctx, exe, "--no-optional-locks", "-C", dir, "status", "--porcelain=v1", "--untracked-files=normal")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("读取状态失败: %v", err)
	}

	var items []UntrackedItem
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		p := strings.TrimSpace(line[3:])
		p = strings.Trim(p, "\"") // 去掉可能的带空格路径双引号
		if p == "" {
			continue
		}
		isDir := strings.HasSuffix(p, "/") || strings.HasSuffix(p, "\\")
		if !isDir {
			full := filepath.Join(dir, p)
			if fi, err := os.Stat(util.LongPath(full)); err == nil && fi.IsDir() {
				isDir = true
				p = strings.TrimSuffix(p, "/") + "/"
			}
		}
		items = append(items, UntrackedItem{
			Path:  p,
			IsDir: isDir,
		})
	}
	return items, nil
}

// ReadIgnore 读取 .gitignore 所有行，返回去除了空行和注释的规则集。
func ReadIgnore(repoDir string) (lines []string, exists bool, err error) {
	path := filepath.Join(repoDir, ".gitignore")
	b, err := os.ReadFile(util.LongPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, true, nil
}

// AppendIgnore 将模式追加到 .gitignore 中。
// 如果 .gitignore 不存在则自动新建；自动去重并按标准换行格式化。
func AppendIgnore(repoDir string, patterns []string) (added []string, err error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	path := filepath.Join(repoDir, ".gitignore")
	existingLines, exists, err := ReadIgnore(repoDir)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, l := range existingLines {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") {
			seen[t] = true
			seen[filepath.ToSlash(t)] = true
		}
	}

	toAdd := []string{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		p = filepath.ToSlash(p)
		if p == "" {
			continue
		}
		if !seen[p] {
			seen[p] = true
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	if exists {
		raw, rerr := os.ReadFile(util.LongPath(path))
		if rerr == nil && len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
			buf.WriteString("\n")
		}
	}

	for _, p := range toAdd {
		buf.WriteString(p + "\n")
	}

	f, err := os.OpenFile(util.LongPath(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("无法写入 .gitignore: %v", err)
	}
	defer f.Close()

	if _, err := f.Write(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("写入 .gitignore 失败: %v", err)
	}
	return toAdd, nil
}

// GitFetch 执行 git fetch。
func GitFetch(dir, gitExe, remote string) (string, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return "", errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmdArgs := []string{"-C", dir, "fetch"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	cmd := exec.CommandContext(ctx, exe, cmdArgs...)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg != "" {
			return "", fmt.Errorf("fetch 失败: %s", msg)
		}
		return "", fmt.Errorf("fetch 失败: %v", err)
	}
	return msg, nil
}

// GitFetchInteractive 在当前终端交互执行 git fetch (允许输入密码与查看进度)。
func GitFetchInteractive(dir, gitExe, remote string) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	cmdArgs := []string{"-C", dir, "fetch"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// GitPullFFOnly 执行带有仅快进锁的 git pull --ff-only。
func GitPullFFOnly(dir, gitExe string) (string, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return "", errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "-C", dir, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg != "" {
			return "", fmt.Errorf("pull 失败 (非快进或冲突): %s", msg)
		}
		return "", fmt.Errorf("pull 失败: %v", err)
	}
	return msg, nil
}

// GitPullFFOnlyInteractive 在当前终端交互执行 git pull --ff-only。
func GitPullFFOnlyInteractive(dir, gitExe string) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	cmd := exec.Command(exe, "-C", dir, "pull", "--ff-only")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// GitCommitAll 执行全量 add 并提交 (git add -A && git commit -m <msg>)。
func GitCommitAll(dir, gitExe, message string) (string, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return "", errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. git add -A
	addCmd := exec.CommandContext(ctx, exe, "-C", dir, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("暂存改动失败: %s (%v)", strings.TrimSpace(string(out)), err)
	}

	// 2. git commit -m
	commitCmd := exec.CommandContext(ctx, exe, "-C", dir, "commit", "-m", message)
	out, err := commitCmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("提交失败: %s (%v)", msg, err)
	}
	return msg, nil
}

// UpstreamInfo 分支及上游追踪信息。
type UpstreamInfo struct {
	Branch      string
	Upstream    string
	HasUpstream bool
}

// GetUpstreamInfo 获取当前分支及上游分支信息。
func GetUpstreamInfo(dir, gitExe string) (UpstreamInfo, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return UpstreamInfo{}, errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取当前分支名
	bCmd := exec.CommandContext(ctx, exe, "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	bOut, err := bCmd.Output()
	if err != nil {
		return UpstreamInfo{}, fmt.Errorf("无法获取当前分支: %v", err)
	}
	branch := strings.TrimSpace(string(bOut))

	info := UpstreamInfo{Branch: branch}
	if branch == "HEAD" || branch == "" {
		return info, nil
	}

	// 检查上游跟踪分支 @{u}
	uCmd := exec.CommandContext(ctx, exe, "-C", dir, "rev-parse", "--abbrev-ref", "@{u}")
	uOut, err := uCmd.Output()
	if err == nil {
		info.Upstream = strings.TrimSpace(string(uOut))
		info.HasUpstream = info.Upstream != ""
	}
	return info, nil
}


// GitAuthContext 传递给 Git 命令执行器的鉴权与环境上下文
type GitAuthContext struct {
	User  string
	Token string
}

func injectGitAuth(cmd *exec.Cmd, auth GitAuthContext) {
	if auth.Token == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	env := os.Environ()
	env = append(env,
		"GIT_ASKPASS="+exe,
		"IGIT_AUTH_USER="+auth.User,
		"IGIT_AUTH_TOKEN="+auth.Token,
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Env = env
}

// CheckFastForwardStatus 检查本地当前分支与指定远端分支的拓扑相对关系。
func CheckFastForwardStatus(dir, gitExe, remote, branch string) (ahead, behind int, hasRemoteBranch bool, err error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return 0, 0, false, errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ref := fmt.Sprintf("%s/%s", remote, branch)
	checkCmd := exec.CommandContext(ctx, exe, "-C", dir, "rev-parse", "--verify", ref)
	if err := checkCmd.Run(); err != nil {
		return 0, 0, false, nil
	}

	rangeArg := fmt.Sprintf("%s...%s", branch, ref)
	cmd := exec.CommandContext(ctx, exe, "-C", dir, "rev-list", "--left-right", "--count", rangeArg)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, true, fmt.Errorf("检查分支拓扑失败: %v", err)
	}

	parts := strings.Fields(string(out))
	if len(parts) >= 2 {
		ah, _ := strconv.Atoi(parts[0])
		bh, _ := strconv.Atoi(parts[1])
		return ah, bh, true, nil
	}
	return 0, 0, true, nil
}

// GitFetchWithAuth 执行抓取，支持可选 Token 鉴权。
func GitFetchWithAuth(dir, gitExe, remote string, auth GitAuthContext) (string, error) {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return "", errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmdArgs := []string{"-C", dir, "fetch"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	cmd := exec.CommandContext(ctx, exe, cmdArgs...)
	injectGitAuth(cmd, auth)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg != "" {
			return "", fmt.Errorf("fetch 失败: %s", msg)
		}
		return "", fmt.Errorf("fetch 失败: %v", err)
	}
	return msg, nil
}

// GitFetchInteractiveWithAuth 在当前终端交互执行 git fetch，支持可选 Token 鉴权。
func GitFetchInteractiveWithAuth(dir, gitExe, remote string, auth GitAuthContext) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	cmdArgs := []string{"-C", dir, "fetch"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
	}
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	injectGitAuth(cmd, auth)
	return cmd.Run()
}

// GitPullFFOnlyWithAuth 执行带有仅快进锁与可选 Token 的 git pull --ff-only。
func GitPullFFOnlyWithAuth(dir, gitExe, remote, branch string, auth GitAuthContext) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	cmdArgs := []string{"-C", dir, "pull", "--ff-only"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
		if branch != "" {
			cmdArgs = append(cmdArgs, branch)
		}
	}
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	injectGitAuth(cmd, auth)
	return cmd.Run()
}

// GitPushWithAuth 执行推送，支持指定 remote 与可选 Token。
func GitPushWithAuth(dir, gitExe, remote, branch string, extraArgs []string, auth GitAuthContext) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	cmdArgs := []string{"-C", dir, "push"}
	if remote != "" {
		cmdArgs = append(cmdArgs, remote)
		if branch != "" {
			cmdArgs = append(cmdArgs, branch)
		}
	}
	cmdArgs = append(cmdArgs, extraArgs...)
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	injectGitAuth(cmd, auth)
	return cmd.Run()
}

// AddRemote 为仓库新增远端。
func AddRemote(dir, gitExe, name, rawURL string) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-C", dir, "remote", "add", name, rawURL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("添加远端失败: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RemoveRemote 删除仓库的远端配置。
func RemoveRemote(dir, gitExe, name string) error {
	exe := ResolveGit(gitExe)
	if exe == "" {
		return errors.New("未找到 git 可执行文件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-C", dir, "remote", "remove", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("删除远端失败: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
