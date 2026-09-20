package gitinfo

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"grscan/internal/state"
	"grscan/internal/util"
)

const (
	KindRepo      = "repo"
	KindBare      = "bare"
	KindWorktree  = "worktree"
	KindSubmodule = "submodule"
)

const maxStatusOutput = 64 << 20 // 64MB 上限，防止异常仓库的输出炸内存

// Inspect 读取单个仓库的事实信息。
// kind 由扫描阶段判定；withStatus=false 时完全不起 git 进程（fast 模式）。
func Inspect(dir, kind, gitExe string, withStatus bool) *state.Repo {
	r := &state.Repo{
		ID:      util.NormalizePath(dir),
		Path:    dir,
		Name:    filepath.Base(filepath.Clean(dir)),
		Kind:    kind,
		Remotes: map[string]string{},
	}

	gitDir, commonDir, errMsg := resolveDirs(dir, kind)
	if errMsg != "" {
		r.ScanIssue = errMsg
		return r
	}

	cfg := readConfig(gitDir)
	r.Bare = cfg.bare
	r.Remotes = cfg.remotes
	r.PrimaryURL = pickPrimaryRemote(cfg.remotes)

	// HEAD
	headRef, headSHA, headOK := readHEAD(gitDir)
	if !headOK {
		r.ScanIssue = "HEAD 不可读"
	}
	switch {
	case headRef != "":
		r.Branch = strings.TrimPrefix(headRef, "refs/heads/")
	case headSHA != "":
		// 分支名留空，交给 UI 层按 Detached 标记决定怎么显示，避免两处各加一遍装饰
		r.Detached = true
	}

	// commit
	sha := headSHA
	if headRef != "" {
		sha = resolveRef(gitDir, commonDir, headRef)
	}
	if sha != "" {
		r.Commit = sha
		if au, ts, subj, ok := readCommit(gitDir, commonDir, sha); ok {
			r.Author = au
			r.Subject = subj
			r.CommitAt = ts.UTC().Format(time.RFC3339)
		}
	}

	exe := ResolveGit(gitExe)
	if exe == "" {
		if r.Commit == "" {
			r.ScanIssue = "未安装 git，无法读取提交信息"
		}
		return r
	}

	// 松散对象被打包（git gc 之后）时，上面的读取会失败，回退到 git 进程。
	if r.CommitAt == "" {
		if c, err := fallbackCommit(dir, exe); err == nil {
			if r.Commit == "" {
				r.Commit = c.sha
			}
			r.Author = c.author
			r.Subject = c.subject
			r.CommitAt = c.at
			if r.Branch == "" {
				r.Branch = c.branch
			}
		} else if r.ScanIssue == "" {
			r.ScanIssue = trimErr(err)
		}
	}

	if !withStatus {
		return r
	}
	if kind == KindBare {
		return r
	}

	st, err := readStatus(dir, exe)
	if err != nil {
		if r.ScanIssue == "" {
			r.ScanIssue = trimErr(err)
		}
		return r
	}
	r.Staged, r.Unstaged, r.Untracked = st.staged, st.unstaged, st.untracked
	r.Ahead, r.Behind, r.HasUpstream = st.ahead, st.behind, st.hasUpstream
	r.Dirty = st.staged+st.unstaged+st.untracked > 0
	return r
}

// resolveDirs 定位 .git 目录与 common 目录。
// worktree / submodule 的 .git 是个文件，里面写着 gitdir: xxx。
// 返回空 errMsg 表示可用；否则 errMsg 会写进记录的 scan_issue 字段。
func resolveDirs(dir, kind string) (gitDir, commonDir, errMsg string) {
	if kind == KindBare {
		if !isBareDir(util.LongPath(dir)) {
			return "", "", "裸仓目录不完整"
		}
		return dir, "", ""
	}
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Lstat(util.LongPath(gitPath))
	if err != nil {
		return "", "", "缺少 .git"
	}
	if fi.IsDir() {
		return gitPath, "", ""
	}
	// .git 是文件：指向真正的 git 目录（worktree / submodule）
	raw, err := os.ReadFile(util.LongPath(gitPath))
	if err != nil {
		return "", "", ".git 文件不可读"
	}
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "gitdir:"))
	target = strings.Trim(target, "\"")
	if target == "" {
		return "", "", ".git 文件内容异常"
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	target = filepath.Clean(target)
	if fi, err := os.Stat(util.LongPath(target)); err != nil || !fi.IsDir() {
		// worktree 被 prune、或 submodule 被删了，只剩一个空壳目录
		return "", "", "指向的 git 目录已失效（悬空工作树/子模块）"
	}
	if cd, err := os.ReadFile(util.LongPath(filepath.Join(target, "commondir"))); err == nil {
		cdPath := strings.TrimSpace(string(cd))
		if !filepath.IsAbs(cdPath) {
			cdPath = filepath.Join(target, cdPath)
		}
		commonDir = filepath.Clean(cdPath)
	}
	return target, commonDir, ""
}

func isBareDir(long string) bool {
	for _, n := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Lstat(filepath.Join(long, n)); err != nil {
			return false
		}
	}
	return true
}

type gitConfig struct {
	bare    bool
	remotes map[string]string
}

// readConfig 手写一个够用的 ini 解析，只读 core.bare 和 remote.*.url。
func readConfig(gitDir string) gitConfig {
	out := gitConfig{remotes: map[string]string{}}
	f, err := os.Open(util.LongPath(filepath.Join(gitDir, "config")))
	if err != nil {
		return out
	}
	defer f.Close()
	var section string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		val := strings.TrimSpace(kv[1])
		switch {
		case section == "core" && key == "bare":
			out.bare = val == "true"
		case strings.HasPrefix(section, `remote "`):
			name := strings.TrimSuffix(strings.TrimPrefix(section, `remote "`), `"`)
			if name != "" && (key == "url" || key == "pushurl") {
				if _, dup := out.remotes[name]; !dup {
					out.remotes[name] = val
				}
			}
		}
	}
	return out
}

func pickPrimaryRemote(remotes map[string]string) string {
	for _, n := range []string{"origin", "upstream"} {
		if v, ok := remotes[n]; ok {
			return v
		}
	}
	if len(remotes) == 0 {
		return ""
	}
	keys := util.SortedKeys(remotes)
	return remotes[keys[0]]
}

func readHEAD(gitDir string) (ref, sha string, ok bool) {
	raw, err := os.ReadFile(util.LongPath(filepath.Join(gitDir, "HEAD")))
	if err != nil {
		return "", "", false
	}
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "ref:") {
		return strings.TrimSpace(strings.TrimPrefix(s, "ref:")), "", true
	}
	if len(s) >= 40 {
		return "", s, true
	}
	return "", "", false
}

func readFileIfExists(p string) (string, bool) {
	b, err := os.ReadFile(util.LongPath(p))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// resolveRef 先找松散 ref，再找 packed-refs；worktree 还要看 commonDir。
func resolveRef(gitDir, commonDir, ref string) string {
	candidates := []string{}
	if gitDir != "" {
		candidates = append(candidates, filepath.Join(gitDir, filepath.FromSlash(ref)))
	}
	if commonDir != "" {
		candidates = append(candidates, filepath.Join(commonDir, filepath.FromSlash(ref)))
	}
	for _, p := range candidates {
		if s, ok := readFileIfExists(p); ok && len(s) >= 40 {
			return s
		}
	}
	for _, base := range dedup([]string{gitDir, commonDir}) {
		if base == "" {
			continue
		}
		f, err := os.Open(util.LongPath(filepath.Join(base, "packed-refs")))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == ref {
				f.Close()
				return fields[0]
			}
		}
		f.Close()
	}
	return ""
}

// readCommit 尝试纯 Go 读松散对象，拿提交者/时间/主题。
func readCommit(gitDir, commonDir, sha string) (author string, ts time.Time, subject string, ok bool) {
	data, err := readObject(gitDir, commonDir, sha)
	if err != nil {
		return "", time.Time{}, "", false
	}
	i := bytes.IndexByte(data, 0)
	if i <= 0 || !bytes.HasPrefix(data, []byte("commit ")) {
		return "", time.Time{}, "", false
	}
	body := data[i+1:]
	if a, t, s := parseCommitBody(body); !t.IsZero() {
		return a, t, s, true
	}
	return "", time.Time{}, "", false
}

func parseCommitBody(body []byte) (author string, ts time.Time, subject string) {
	lines := strings.Split(string(body), "\n")
	for idx, line := range lines {
		if line == "" {
			// 空行之后是提交信息
			for _, rest := range lines[idx+1:] {
				if strings.TrimSpace(rest) != "" {
					subject = strings.TrimSpace(rest)
					break
				}
			}
			break
		}
		if strings.HasPrefix(line, "author ") {
			author, ts = parseIdent(line[len("author "):])
		}
		if idx > 60 {
			break
		}
	}
	return author, ts, subject
}

func parseIdent(s string) (string, time.Time) {
	// Name <email> 1700000000 +0800
	end := strings.LastIndex(s, ">")
	var name string
	rest := s
	if end >= 0 {
		name = strings.TrimSpace(s[:end+1])
		rest = strings.TrimSpace(s[end+1:])
	} else {
		fields := strings.Fields(s)
		if len(fields) >= 2 {
			rest = strings.Join(fields[len(fields)-2:], " ")
			name = strings.TrimSpace(strings.Join(fields[:len(fields)-2], " "))
		}
	}
	f := strings.Fields(rest)
	if len(f) >= 1 {
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			return name, time.Unix(n, 0)
		}
	}
	return name, time.Time{}
}

// objectDirs 收集所有可能存放对象的目录，含 alternates 指向的位置。
func objectDirs(gitDir, commonDir string) []string {
	out := []string{}
	add := func(base string) {
		if base == "" {
			return
		}
		p := filepath.Join(base, "objects")
		if _, err := os.Stat(util.LongPath(p)); err == nil {
			out = append(out, p)
		}
	}
	add(gitDir)
	add(commonDir)
	if gitDir != "" {
		altPath := filepath.Join(gitDir, "objects", "info", "alternates")
		if b, err := os.ReadFile(util.LongPath(altPath)); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if !filepath.IsAbs(line) {
					line = filepath.Join(gitDir, "objects", line)
				}
				p := filepath.Clean(line)
				if _, err := os.Stat(util.LongPath(p)); err == nil {
					out = append(out, p)
				}
			}
		}
	}
	return dedup(out)
}

func readObject(gitDir, commonDir, sha string) ([]byte, error) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) < 3 {
		return nil, io.ErrUnexpectedEOF
	}
	for _, od := range objectDirs(gitDir, commonDir) {
		p := filepath.Join(od, sha[:2], sha[2:])
		data, err := readLooseObject(p)
		if err == nil {
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

func readLooseObject(p string) ([]byte, error) {
	f, err := os.Open(util.LongPath(p))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 8<<20))
}

type commitInfo struct {
	sha, author, subject, at, branch string
}

func fallbackCommit(dir, exe string) (commitInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--no-optional-locks", "-C", dir,
		"log", "-1", "--format=%H%x1f%aI%x1f%an <%ae>%x1f%s%x1f%D")
	out, err := cmd.Output()
	if err != nil {
		return commitInfo{}, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return commitInfo{}, io.EOF
	}
	parts := strings.Split(line, "\x1f")
	var c commitInfo
	if len(parts) > 0 {
		c.sha = parts[0]
	}
	if len(parts) > 1 {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[1])); err == nil {
			c.at = t.UTC().Format(time.RFC3339)
		}
	}
	if len(parts) > 2 {
		c.author = strings.TrimSpace(parts[2])
	}
	if len(parts) > 3 {
		c.subject = strings.TrimSpace(parts[3])
	}
	return c, nil
}

type statusInfo struct {
	staged, unstaged, untracked, ahead, behind int
	hasUpstream                                bool
}

// readStatus 跑一次 git status --porcelain=v2 --branch，把分支/领先落后/改动文件数一次拿全。
func readStatus(dir, exe string) (statusInfo, error) {
	var si statusInfo
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--no-optional-locks", "-c", "gc.auto=0",
		"-C", dir, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	var buf bytes.Buffer
	cmd.Stdout = &limitWriter{w: &buf, n: maxStatusOutput}
	cmd.Stderr = &limitWriter{w: &buf, n: 4096}
	err := cmd.Run()
	if err != nil {
		return si, err
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == "branch.ab" {
				si.ahead = atoiSign(fields[2][1:])
				if len(fields) > 3 {
					si.behind = atoiSign(fields[3][1:])
				}
			}
			if len(fields) >= 3 && fields[1] == "branch.upstream" {
				si.hasUpstream = true
			}
			continue
		}
		// 冲突(u)、普通变更(1)、重命名(2)、未跟踪(?)
		if strings.HasPrefix(line, "? ") {
			si.untracked++
			continue
		}
		if strings.HasPrefix(line, "u ") {
			si.unstaged++
			continue
		}
		if len(line) > 2 && (line[0] == '1' || line[0] == '2') && line[1] == ' ' {
			rest := line[2:]
			// XY sub m m m m m m m m h h path...
			if len(rest) >= 2 {
				if rest[0] != '.' {
					si.staged++
				}
				if rest[1] != '.' {
					si.unstaged++
				}
			}
		}
	}
	return si, nil
}

func atoiSign(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ResolveGit 定位 git 可执行文件；找不到返回空串。
func ResolveGit(explicit string) string {
	if explicit != "" {
		if p, err := exec.LookPath(explicit); err == nil {
			return p
		}
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Base(p), "git.exe") {
		return p
	}
	return p
}

// DirSize 统计目录占用；不跟随符号链接，遇到错误就当 0。
func DirSize(root string) int64 {
	var total int64
	stack := []string{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(util.LongPath(cur))
		if err != nil {
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			total += info.Size()
			if e.IsDir() {
				stack = append(stack, filepath.Join(cur, e.Name()))
			}
		}
	}
	return total
}

type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	l.n -= int64(len(p))
	_, err := l.w.Write(p)
	if err != nil {
		return len(p), err
	}
	return len(p), nil
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func trimErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	if len(s) > 160 {
		s = s[:160]
	}
	return strings.ReplaceAll(s, "\n", " ")
}
