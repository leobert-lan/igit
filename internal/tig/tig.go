// Package tig 把清单里的一个仓库交给 tig 去看。
//
// grscan 管的是"几百个仓库里哪些值得看、看到什么程度"，tig 管的是"某一个仓库里发生了什么"。
// 两个工具的接缝就在这里：grscan 负责挑出仓库并说明为什么挑它，tig 负责让人看进去。
// 本包只做三件事——决定 tig 该落在哪一屏、拼出命令行、在新终端窗口里把它拉起来。
package tig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"grscan/internal/config"
	"grscan/internal/state"
)

// View 决定 tig 打开时停在哪一屏。
type View struct {
	Name string   // tig 子命令：status / log / refs。空串表示主视图
	Args []string // 跟在子命令后面的参数
	Why  string   // 为什么是这一屏，说给人听
}

func (v View) String() string {
	if v.Name == "" {
		return "main"
	}
	return v.Name
}

// Decide 按仓库当前的事实挑开屏位置。
//
// 光把 tig 弹出来是不够的：每次都停在主视图，你得自己按 s 进 status、
// 自己回想"我到底为什么打开这个仓库"。grscan 扫的时候已经知道答案了 ——
// 有未提交改动、有没推上去的提交、或者干脆没配上游 —— 这里把它变成开屏位置。
//
// force 为 main / status / log / refs 时按指定来，auto 或空则走推断。
func Decide(r *state.Repo, force string) (View, error) {
	switch strings.ToLower(strings.TrimSpace(force)) {
	case "status":
		return View{Name: "status", Why: "手动指定"}, nil
	case "log":
		return View{Name: "log", Why: "手动指定"}, nil
	case "refs":
		return View{Name: "refs", Why: "手动指定"}, nil
	case "main":
		return View{Why: "手动指定：主视图"}, nil
	case "", "auto":
		// 落到下面的推断
	default:
		return View{}, fmt.Errorf("不认识的视图 %q，可选: auto / main / status / log / refs", force)
	}

	switch {
	case r.Dirty:
		n := r.Staged + r.Unstaged + r.Untracked
		why := "有未提交改动"
		if n > 0 {
			why = fmt.Sprintf("%d 个文件有改动没提交", n)
		}
		return View{Name: "status", Why: why}, nil
	case r.Ahead > 0 && r.HasUpstream:
		return View{
			Name: "log",
			Args: []string{"@{u}..HEAD"},
			Why:  fmt.Sprintf("领先远端 %d 个提交", r.Ahead),
		}, nil
	case !r.HasUpstream:
		return View{Name: "refs", Why: "没配上游，看看分支和远端"}, nil
	}
	return View{Why: "没有明显待办，看提交历史"}, nil
}

// IsViewWord 判断一个位置参数是不是视图保留字。
//
// 位置参数只能有一个含义。是保留字就当视图，不是就当仓库关键字 —— 不靠上下文猜：
// 靠猜的话同一个词在不同作用域下含义会变，最难预测的那类行为。
func IsViewWord(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto", "main", "status", "log", "refs":
		return true
	}
	return false
}

// Exe 确定 tig 可执行文件：配置优先，其次 PATH。
// 找不到时把怎么修说清楚 —— 这条命令人在面板里按一下就会碰到。
func Exe(cfg *config.Config) (string, error) {
	if cfg != nil {
		if p := strings.TrimSpace(cfg.TigExe); p != "" {
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("配置的 tig 路径不可用: %s", p)
			}
			return p, nil
		}
	}
	p, err := exec.LookPath("tig")
	if err != nil {
		return "", fmt.Errorf("PATH 里没有 tig。装好后若不在 PATH，用 grscan config set tig-exe=<完整路径> 指一下")
	}
	return p, nil
}

// Args 拼出 tig 的参数。不加引号 —— 交给 exec 处理转义，比手拼字符串稳。
func Args(exe, dir string, v View) []string {
	out := []string{exe, "-C", dir}
	if v.Name != "" {
		out = append(out, v.Name)
	}
	return append(out, v.Args...)
}

// Launch 在新终端窗口里打开 tig，不等它结束。
// 返回实际执行的命令行，调用方拿去回显，让人知道发生了什么。
func Launch(cfg *config.Config, r *state.Repo, v View) (string, error) {
	exe, err := Exe(cfg)
	if err != nil {
		return "", err
	}
	if r.Missing {
		return "", fmt.Errorf("磁盘上已经没有这个仓库: %s（grscan prune 可清掉记录）", r.Path)
	}
	dir := r.Path
	fi, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("仓库目录不可用: %s（%v）", dir, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("仓库路径不是目录: %s", dir)
	}

	argv := Args(exe, dir, v)
	var cmd *exec.Cmd
	if tmpl := strings.TrimSpace(cfg.TermCmd); tmpl != "" {
		line := strings.NewReplacer("{dir}", dir, "{cmd}", Line(argv)).Replace(tmpl)
		cmd = shellLine(line)
	} else {
		cmd = defaultTerm(exe, dir, argv)
	}
	cmd.Env = ensureTERM(cmd.Env)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动终端失败: %v", err)
	}
	go func() { _ = cmd.Wait() }() // 不等，但也别留僵尸
	return Line(argv), nil
}

// msys2Terminal tig 若是 MSYS2 编出来的（PortableGit、Git for Windows 里带的那份），
// 它旁边通常就躺着 mintty.exe —— 这是 ncurses 程序的原生终端。
// conhost 对 ncurses 的支持一直不稳，能用 mintty 就别硬上 conhost。
func msys2Terminal(exe string) string {
	for _, name := range []string{"mintty.exe", "mintty"} {
		cand := filepath.Join(filepath.Dir(exe), name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// defaultTerm 系统默认的新窗口方式。
//
// 先试 mintty（tig 多半是 MSYS2 编的），没有就用 start 拉一个 conhost：
// 不写 start 的标题参数，省略时第一个 token 才会被当成命令；
// 外面再包一层 cmd /k，tig 退出后窗口留着 —— 万一 tig 起不来，报错不会一闪而过。
// Windows 11 把默认终端设成 Windows Terminal 时，conhost 那个窗口会自动托管过去。
func defaultTerm(exe, dir string, argv []string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		if mintty := msys2Terminal(exe); mintty != "" {
			return exec.Command(mintty, append([]string{"-e"}, argv...)...)
		}
		head := []string{"cmd", "/c", "start", "/d", dir, "cmd", "/k"}
		return exec.Command(head[0], append(head[1:], argv...)...)
	case "darwin":
		script := fmt.Sprintf("cd %s && %s", posixQuote(dir), Line(argv))
		return exec.Command("osascript",
			"-e", fmt.Sprintf(`tell application "Terminal" to do script %s`, posixQuote(script)),
			"-e", "activate")
	default:
		return exec.Command("x-terminal-emulator", "-e", "sh", "-c",
			fmt.Sprintf("cd %s && %s", posixQuote(dir), Line(argv)))
	}
}

// defaultTERM 兜底的终端类型。Windows 上没有 TERM 这个变量，
// 而 MSYS2 编出来的 tig（PortableGit、Git for Windows 里带的那份）靠它初始化 ncurses ——
// 缺了就是开屏即退，连一句报错都没有，最难查的那种失败。
const defaultTERM = "xterm-256color"

// ensureTERM 在没有 TERM 的环境里补上它。已经设了就原样返回，不覆盖用户的配置。
func ensureTERM(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	for _, e := range env {
		if len(e) >= 5 && strings.EqualFold(e[:5], "TERM=") {
			return env
		}
	}
	return append(env, "TERM="+defaultTERM)
}

func shellLine(line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", line)
	}
	return exec.Command("sh", "-c", line)
}

// Line 把参数拼成一行看得懂的命令行。只用于回显，
// 真正执行走的是 exec 的参数数组。
func Line(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			parts[i] = `"` + a + `"`
			continue
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

func posixQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}
