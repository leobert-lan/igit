package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"grscan/internal/config"
	"grscan/internal/export"
	"grscan/internal/gitinfo"
	"grscan/internal/pick"
	"grscan/internal/render"
	"grscan/internal/scan"
	"grscan/internal/scope"
	"grscan/internal/state"
	"grscan/internal/tig"
	"grscan/internal/tree"
	"grscan/internal/tui"
	"grscan/internal/util"

	"github.com/chzyer/readline"
)

const version = "0.1.0"


type appCtx struct {
	home    string
	cfg     *config.Config
	cfgPath string
	stPath  string
	state   *state.State
	res     *scope.Resolved // 当前作用域，按需解析
	prev    *scope.Resolved // 上一个作用域，cd - 用
	repl        bool               // 是否处于交互会话里
	rl          *readline.Instance // 会话内的交互式 readline 实例
	pipeScanner *bufio.Scanner     // 管道会话模式下的 scanner
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(raw []string) int {
	raw, homeFlag := takeGlobalHome(raw)
	if len(raw) > 0 {
		switch raw[0] {
		case "-h", "--help", "help":
			printHelp(raw[1:])
			return 0
		case "-v", "--version", "version":
			fmt.Printf("igit %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return 0
		}
	}
	raw, scopeFlag := takeGlobalScope(raw)
	cmd := ""
	if len(raw) > 0 {
		cmd, raw = raw[0], raw[1:]
	}

	home := config.ResolveHome(homeFlag)
	if err := os.MkdirAll(home, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建数据目录 %s: %v\n", home, err)
		return 1
	}
	cfgPath, stPath := config.Paths(home)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取配置失败: %v\n", err)
		return 1
	}
	st, err := state.Load(stPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取状态失败: %v\n", err)
		return 1
	}

	ctx := &appCtx{home: home, cfg: cfg, cfgPath: cfgPath, stPath: stPath, state: st}
	a := parseArgs(raw)

	// 定作用域：按当前目录推断，或 --in=<scope> 临时指定。
	// cd 失败也不拦 —— 它是绝对定位，不依赖当前站在哪。
	ok := resolveScope(ctx, a, scopeFlag)
	if !ok && cmd != "cd" && needsHardScope(cmd) {
		return 1
	}

	// 进会话：真终端直接进；管道里喂了一串指令也当会话跑，方便脚本。
	// 什么都没给就只打印一次摘要，绝不卡住等输入。
	if cmd == "" {
		if !ok {
			return 1
		}
		if isTerminal(os.Stdin) {
			return runREPL(ctx)
		}
		if pipe := peekStdin(); pipe != nil {
			return replPipe(ctx, pipe)
		}
		return cmdScope(ctx, a)
	}
	return dispatch(ctx, cmd, a)
}

// needsScope 这些命令的结果随作用域变化，执行前都要先定作用域。
func needsScope(cmd string) bool {
	switch cmd {
	case "", "ls", "list", "pwd", "triage", "tui", "tree", "export", "stats":
		return true
	}
	return false
}

// needsHardScope 这些命令没有作用域就没有东西可输出：定不出作用域必须报错，
// 绝不静默退回全集 —— 你以为在看局部其实在看全局，这比报错危险得多。
// 其余命令（triage/tree/export/stats）定不出时退回全集，但会把话说出来。
func needsHardScope(cmd string) bool {
	switch cmd {
	case "", "ls", "list", "pwd":
		return true
	}
	return false
}

// dispatch 执行一条指令。一次性命令与 REPL 共用同一套分发，
// 区别只在 REPL 里 cd 改的是会话内状态，进程退出就没了。
func dispatch(ctx *appCtx, cmd string, a args) int {
	if a.has("help", "h") {
		return printCommandHelp(cmd)
	}
	switch cmd {
	case "cd":
		return cmdCd(ctx, a)
	case "pwd":
		return cmdPwd(ctx, a)
	case "ls":
		return cmdLs(ctx, a)
	case "scan":
		return cmdScan(ctx, a)
	case "list":
		return cmdList(ctx, a)
	case "tree":
		return cmdTree(ctx, a)
	case "triage", "tui":
		return cmdTriage(ctx, a)
	case "set":
		return cmdSet(ctx, a)
	case "group":
		return cmdGroup(ctx, a)
	case "tag":
		return cmdTag(ctx, a)
	case "lsbytag", "ls-by-tag":
		return cmdLsByTag(ctx, a)
	case "status":
		return cmdStatus(ctx, a)
	case "note":
		return cmdNote(ctx, a)
	case "dupes":
		return cmdDupes(ctx, a)
	case "stats":
		return cmdStats(ctx, a)
	case "open":
		return cmdOpen(ctx, a)
	case "tig":
		return cmdTig(ctx, a)
	case "rm":
		return cmdRm(ctx, a)
	case "prune":
		return cmdPrune(ctx, a)
	case "hide":
		return cmdHide(ctx, a, true)
	case "unhide":
		return cmdHide(ctx, a, false)
	case "fold":
		return cmdFold(ctx, a, true)
	case "unfold":
		return cmdFold(ctx, a, false)
	case "dirs":
		return cmdDirs(ctx, a)
	case "export":
		return cmdExport(ctx, a)
	case "config":
		return cmdConfig(ctx, a)
	case "ignore":
		return cmdIgnore(ctx, a)
	case "remote":
		return cmdRemote(ctx, a)
	case "fetch":
		return cmdFetch(ctx, a)
	case "pull":
		return cmdPull(ctx, a)
	case "commit":
		return cmdCommit(ctx, a)
	case "push":
		return cmdPush(ctx, a)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		printHelp(nil)
		return 2
	}
}

// ---------------------------------------------------------------- 会话

// runREPL 从 terminal 进到 igit 自己的世界：提示符带当前作用域，
// 敲一行执行一条，exit 出来。作用域在会话内流转，进程结束就没了。
func runREPL(ctx *appCtx) int {
	ctx.repl = true
	fmt.Printf("igit %s · %s\n", version, scopeLine(ctx.res))
	fmt.Println("  敲 help 看这个界面能做什么，exit 退出")
	fmt.Println()

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            prompt(ctx),
		HistoryFile:       filepath.Join(ctx.home, "history"),
		HistoryLimit:      500,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
		AutoComplete:      replCompleter{ctx: ctx},
	})
	if err != nil {
		return replPipe(ctx, bufio.NewReader(os.Stdin))
	}
	defer rl.Close()
	ctx.rl = rl
	defer func() { ctx.rl = nil }()

	for {
		rl.SetPrompt(prompt(ctx))
		line, err := rl.Readline()
		switch {
		case err == readline.ErrInterrupt:
			continue
		case err == io.EOF:
			fmt.Println("bye")
			return 0
		case err != nil:
			fmt.Println("bye")
			return 0
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line {
		case "exit", "quit", "q", ":q":
			fmt.Println("bye")
			return 0
		}
		execLine(ctx, line)
	}
}

// ---------------------------------------------------------------- Tab 补全
//
// 补的不是固定词典，而是"站在这一层能敲什么、能去哪"：
// 第一个词是当前界面的命令，cd 的参数是这一层的子项和磁盘上的真实目录。
// 歧义节点上两个视角的候选会一起冒出来，选哪个就是哪个，不用记语法。

// replCompleter 把候选交给 readline。它要的是"候选去掉已输入部分"，
// 长度是已输入这个词的长度 —— 已输入的内容改不了，只能往后补。
type replCompleter struct{ ctx *appCtx }

func (c replCompleter) Do(line []rune, pos int) ([][]rune, int) {
	head := string(line[:pos])
	word := lastWordOf(head)
	cands := candidates(c.ctx, head, word)
	if len(cands) == 0 {
		return nil, 0
	}
	wr := []rune(word)
	out := make([][]rune, 0, len(cands))
	for _, cand := range cands {
		cr := []rune(cand)
		if len(cr) < len(wr) {
			continue // 比已输入的还短，补不了
		}
		out = append(out, cr[len(wr):])
	}
	if len(out) == 0 {
		return nil, 0
	}
	return out, len(wr)
}

// lastWordOf 取正在敲的那个词（按空格切分）。
func lastWordOf(head string) string {
	if i := strings.LastIndexAny(head, " \t"); i >= 0 {
		return head[i+1:]
	}
	return head
}

// candidates 按当前界面给出完整候选。
func candidates(ctx *appCtx, head, word string) []string {
	fields := strings.Fields(head)
	endsSpace := head == "" || strings.HasSuffix(head, " ") || strings.HasSuffix(head, "\t")

	// 还在敲第一个词：补这个界面能用的命令
	if len(fields) == 0 || (len(fields) == 1 && !endsSpace) {
		return matchPrefix(commandsFor(ctx), word)
	}
	switch fields[0] {
	case "cd":
		// 正在敲 file: 开头的路径 → 直接读磁盘。
		// 没扫过的目录也补得出来，顺带暴露"这儿还没扫到"。
		if i := strings.IndexAny(word, ":#"); i >= 0 && strings.EqualFold(word[:i], scope.DimFile) {
			return filePaths(word[:i+1], word[i+1:])
		}
		return matchPrefix(cdTargets(ctx), word)
	case "open", "rm", "show", "log", "set", "tig":
		return matchPrefix(repoNames(ctx), word)
	case "group":
		if len(fields) == 1 && endsSpace || (len(fields) == 2 && !endsSpace) {
			subc := []string{"list", "new", "set", "clear"}
			return matchPrefix(append(subc, allGroupNames(ctx)...), word)
		}
		if len(fields) >= 2 {
			return matchPrefix(allGroupNames(ctx), word)
		}
	case "tag":
		if len(fields) == 1 && endsSpace || (len(fields) == 2 && !endsSpace) {
			subc := []string{"list", "add", "rm", "filter", "find"}
			return matchPrefix(subc, word)
		}
		if len(fields) >= 2 {
			return matchPrefix(allTagNames(ctx), word)
		}
	case "lsbytag", "ls-by-tag":
		return matchPrefix(allTagNames(ctx), word)
	case "status":
		return matchPrefix([]string{"new", "active", "done", "archived", "待处理", "跟进中", "已处理", "已归档"}, word)
	case "ignore":
		if len(fields) == 1 && endsSpace || (len(fields) == 2 && !endsSpace) {
			return matchPrefix([]string{"add", "show", "untracked"}, word)
		}
		if len(fields) >= 2 && fields[1] == "add" {
			return matchPrefix(untrackedCandidates(ctx), word)
		}
	case "remote":
		if len(fields) == 1 && endsSpace || (len(fields) == 2 && !endsSpace) {
			return matchPrefix([]string{"user", "list", "remember", "clear-user"}, word)
		}
		if len(fields) >= 2 && fields[1] == "user" {
			return matchPrefix(remoteUserCandidates(ctx), word)
		}
	case "help":
		return matchPrefix(helpTopicNames(), word)
	}
	return nil
}

func allGroupNames(ctx *appCtx) []string {
	if ctx == nil || ctx.state == nil {
		return nil
	}
	return ctx.state.AllGroups()
}

func refreshCurrentScope(ctx *appCtx) {
	if ctx != nil && ctx.res != nil && ctx.state != nil {
		if res, err := scope.Resolve(ctx.res.Scope, ctx.state); err == nil {
			ctx.res = res
		}
	}
}

func allTagNames(ctx *appCtx) []string {
	m := map[string]bool{}
	for _, r := range ctx.state.Repos {
		for _, t := range r.Tags {
			m[t] = true
		}
	}
	return util.SortedKeys(m)
}

// filePaths 列出磁盘上真实存在的子目录，拼成 file: 段。
func filePaths(prefix, body string) []string {
	if strings.TrimSpace(body) == "" {
		return driveTargets(prefix)
	}
	dir, stem := splitForComplete(body)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if stem != "" && !strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(stem)) {
			continue
		}
		out = append(out, prefix+filepath.Join(dir, e.Name()))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// splitForComplete 把路径拆成"已经确定的目录"+"正在敲的片段"。
func splitForComplete(body string) (dir, stem string) {
	if len(body) == 2 && body[1] == ':' { // D: → D:\
		return body + `\`, ""
	}
	if strings.HasSuffix(body, "/") || strings.HasSuffix(body, `\`) {
		return body, ""
	}
	idx := strings.LastIndexAny(body, `/\`)
	if idx < 0 {
		return "", body
	}
	return body[:idx+1], body[idx+1:]
}

// driveTargets 刚敲下 file: 时给盘符，Windows 没有统一的根可列。
func driveTargets(prefix string) []string {
	out := []string{}
	for c := 'C'; c <= 'Z'; c++ {
		p := string(c) + `:\`
		if _, err := os.Stat(p); err == nil {
			out = append(out, prefix+p)
		}
	}
	return out
}

// commandsFor 这个界面上能敲的命令。功能集由界面定义，所以 Tab 补出来的
// 就是此刻真正可用的那几个 —— 顺带把"这个界面能做什么"摆明了。
func commandsFor(ctx *appCtx) []string {
	common := []string{"cd", "pwd", "help", "exit", "quit", "group", "tag", "config", "lsbytag"}
	res := ctx.res
	if res == nil {
		return append(common, "ls", "list", "scan")
	}
	switch res.Kind {
	case scope.KindRepo:
		return append(common, "ls", "list", "open", "tig", "stats", "status", "note", "scan", "dupes", "set", "ignore", "remote", "fetch", "pull", "commit", "push")
	case scope.KindRoot:
		return append(common,
			"ls", "list", "scan", "stats", "tree", "triage", "tui", "export", "dupes", "dirs", "tig", "fetch", "remote")
	default:
		acts := []string{
			"ls", "list", "tree", "triage", "tui", "stats", "export", "set", "status", "note", "tig", "open", "hide", "fold", "dirs", "scan", "dupes", "fetch", "remote",
		}
		return append(common, acts...)
	}
}

// cdTargets 这一层能去哪：维度入口、子项、自身（嵌套时）、上一层、全集。
func cdTargets(ctx *appCtx) []string {
	res := ctx.res
	if res == nil {
		return nil
	}
	out := []string{}
	// 维度前缀本身也要能补出来，否则 file: 后面的路径补全无从下手
	out = append(out, "file:", "group:", "tag:", "repo:")

	switch res.Kind {
	case scope.KindRoot:
		for _, it := range res.Children {
			out = append(out, it.Target) // file:D:\work / group:工作 / tag:xxx
		}
	default:
		for _, it := range res.Children {
			out = append(out, it.Label)
		}
		if res.Self != nil {
			out = append(out, "repo:"+res.Self.Path) // 这个目录自己也是个仓库
		}
	}
	out = append(out, "..", "/", "-")
	return out
}

// repoNames 当前这批仓库的名字，给 open/rm/show 这类"指名道姓"的命令补。
func repoNames(ctx *appCtx) []string {
	if ctx.res == nil {
		return nil
	}
	out := make([]string, 0, len(ctx.res.Repos))
	for _, r := range ctx.res.Repos {
		out = append(out, r.Name)
	}
	return out
}

// matchPrefix 大小写不敏感地挑出候选。Windows 上路径和名字都不分大小写，
// 敲小写能补出真实写法（补的是后半截，已输入的部分改不了）。
func matchPrefix(in []string, word string) []string {
	out := make([]string, 0, len(in))
	lower := strings.ToLower(word)
	seen := map[string]bool{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		if word != "" && !strings.HasPrefix(strings.ToLower(s), lower) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// replPipe 从管道读指令执行。没有补全和历史，但脚本能喂一整串进来。
func replPipe(ctx *appCtx, in *bufio.Reader) int {
	ctx.repl = true
	fmt.Printf("igit %s · %s\n", version, scopeLine(ctx.res))
	sc := bufio.NewScanner(in)
	ctx.pipeScanner = sc
	defer func() { ctx.pipeScanner = nil }()
	for {
		fmt.Print(prompt(ctx))
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch line {
		case "exit", "quit", "q", ":q":
			fmt.Println("bye")
			return 0
		}
		execLine(ctx, line)
	}
	fmt.Println("bye")
	return 0
}

// peekStdin 管道里真有东西就返回带缓冲的 reader（Peek 不消费，后面还能正常读），
// 空管道或 /dev/null 返回 nil。
func peekStdin() *bufio.Reader {
	st, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}
	if st.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	br := bufio.NewReader(os.Stdin)
	if _, err := br.Peek(1); err != nil {
		return nil
	}
	return br
}

// execLine 执行会话里的一行。这里不重新推断作用域 —— 会话内 cd 改的就是它。
func execLine(ctx *appCtx, line string) {
	toks := tokenize(line)
	if len(toks) == 0 {
		return
	}
	cmd, rest := toks[0], toks[1:]
	if cmd == "help" || cmd == "?" {
		cmdHelp(ctx, rest)
		return
	}
	dispatch(ctx, cmd, parseArgs(rest))
}

// prompt 提示符带上作用域和仓库数，让人随时知道自己在哪。
func prompt(ctx *appCtx) string {
	if ctx.res == nil {
		return "igit > "
	}
	trail := ctx.res.Scope.TrailString()
	if ctx.res.TargetRepo() != nil && (ctx.res.NodeType == scope.NodeRepo || ctx.res.Kind == scope.KindRepo) {
		return fmt.Sprintf("igit [%s] > ", trail)
	}
	return fmt.Sprintf("igit [%s · %d 个仓库] > ", trail, ctx.res.Totals.Repos)
}

func scopeLine(res *scope.Resolved) string {
	if res == nil {
		return "当前目录不在扫描范围内"
	}
	return fmt.Sprintf("作用域 %s · %d 个仓库", scopeLabel(res), res.Totals.Repos)
}

// tokenize 把一行拆成参数。反斜杠不当转义符 —— Windows 路径里全是它。
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}


// ---------------------------------------------------------------- 参数解析

type args struct {
	kv    map[string]string
	multi map[string][]string
	pos   []string
}

func parseArgs(in []string) args {
	a := args{kv: map[string]string{}, multi: map[string][]string{}}
	for i := 0; i < len(in); i++ {
		tok := in[i]
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			a.pos = append(a.pos, tok)
			continue
		}
		name := strings.TrimLeft(tok, "-")
		val := "true"
		if j := strings.Index(name, "="); j >= 0 {
			name, val = name[:j], name[j+1:]
		} else if val == "true" && i+1 < len(in) && !strings.HasPrefix(in[i+1], "-") {
			val = in[i+1]
			i++
		}
		name = strings.ReplaceAll(name, "-", "_")
		a.kv[name] = val
		a.multi[name] = append(a.multi[name], val)
	}
	return a
}

func takeGlobalHome(in []string) (rest []string, home string) {
	rest = in[:0:0]
	for i := 0; i < len(in); i++ {
		tok := in[i]
		if tok == "--home" && i+1 < len(in) {
			home = in[i+1]
			i++
			continue
		}
		if strings.HasPrefix(tok, "--home=") {
			home = strings.TrimPrefix(tok, "--home=")
			continue
		}
		rest = append(rest, tok)
	}
	if len(in) == 0 {
		rest = nil
	}
	return rest, home
}

// takeGlobalScope 摘出 --in= / --scope=，它们写在命令前后都算数。
func takeGlobalScope(in []string) (rest []string, scope string) {
	rest = in[:0:0]
	for i := 0; i < len(in); i++ {
		tok := in[i]
		if tok == "--in" || tok == "--scope" {
			if i+1 < len(in) {
				scope = in[i+1]
				i++
			}
			continue
		}
		for _, p := range []string{"--in=", "--scope="} {
			if strings.HasPrefix(tok, p) {
				scope = strings.TrimPrefix(tok, p)
				break
			}
		}
		if strings.HasPrefix(tok, "--in=") || strings.HasPrefix(tok, "--scope=") {
			continue
		}
		rest = append(rest, tok)
	}
	if len(in) == 0 {
		rest = nil
	}
	return rest, scope
}

func (a args) has(keys ...string) bool {
	for _, k := range keys {
		if v, ok := a.kv[k]; ok && v != "false" && v != "0" {
			return true
		}
	}
	return false
}

func (a args) str(def string, keys ...string) string {
	for _, k := range keys {
		if v, ok := a.kv[k]; ok {
			return v
		}
	}
	return def
}

func (a args) int(def int, keys ...string) int {
	for _, k := range keys {
		if v, ok := a.kv[k]; ok {
			if n, err := fmt.Sscanf(v, "%d", &def); err == nil && n == 1 {
				return def
			}
		}
	}
	return def
}

// ---------------------------------------------------------------- 作用域

// resolveScope 定出当前作用域：--in= 临时指定 → 当前目录推断 → 报错。
// 每次进来都重新推断，不读上次的落盘状态：焦点是会话内的，不是配置。
// 最后一步绝不静默退回全局：你以为在看局部其实在看全局，这比报错危险得多。
func resolveScope(ctx *appCtx, a args, override string) bool {
	in := override
	if in == "" {
		in = a.str("", "in", "scope")
	}
	if in != "" {
		sc, err := scope.Parse(in)
		if err != nil {
			fmt.Printf("读不懂这个作用域: %s（%v）\n", in, err)
			return false
		}
		return setResolved(ctx, sc)
	}
	return resolveFromCWD(ctx)
}

// resolveFromCWD 零状态的入口 B：当前目录本身就是作用域。
// 向下为主，向上只作兜底 —— 向下能同时表达两种粒度，向上只能锁死在一个仓库。
func resolveFromCWD(ctx *appCtx) bool {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "拿不到当前目录: %v\n", err)
		return false
	}
	cwdID := util.NormalizePath(cwd)
	var self, anc *state.Repo
	under := 0
	for _, r := range scope.Visible(ctx.state) {
		switch {
		case util.NormalizePath(r.Path) == cwdID:
			self = r
		case util.HasPrefixPath(cwd, r.Path):
			if anc == nil || len(r.Path) > len(anc.Path) {
				anc = r
			}
		case util.HasPrefixPath(r.Path, cwd):
			under++
		}
	}
	switch {
	case self != nil, under > 0:
		return setResolved(ctx, scope.Scope{
			Segs:   []scope.Seg{{Dim: scope.DimFile, Ident: cwd}},
			Source: scope.SourceCWD,
		})
	case anc != nil:
		return setResolved(ctx, scope.Scope{
			Segs:   []scope.Seg{{Dim: scope.DimFile, Ident: anc.Path}},
			Source: scope.SourceCWD,
		})
	}
	fmt.Printf("当前目录不在扫描范围内: %s\n", cwd)
	fmt.Println("  先跑  igit scan  扫一遍，或  igit config add-root <目录>  把这里加进扫描根")
	fmt.Println("  也可以  igit cd /  直接看全集")
	return false
}

// setResolved 解析失败时逐级回退到父节点并说明原因，不崩不 panic。
func setResolved(ctx *appCtx, sc scope.Scope) bool {
	res, err := scope.Resolve(sc, ctx.state)
	if err == nil {
		ctx.res = res
		return true
	}
	if len(sc.Segs) == 0 {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return false
	}
	for cur := sc.Parent(); ; cur = cur.Parent() {
		if r2, e2 := scope.Resolve(cur, ctx.state); e2 == nil {
			fmt.Printf("（%v，已退到 %s）\n", err, scopeLabel(r2))
			ctx.res = r2
			return true
		}
		if len(cur.Segs) == 0 {
			break
		}
	}
	fmt.Fprintf(os.Stderr, "%v\n", err)
	return false
}

func scopeLabel(res *scope.Resolved) string {
	if s := res.Scope.TrailString(); s != "" && s != "/" {
		return s
	}
	return "全集"
}

// scopeRepos 当前作用域圈定的仓库。全集或没定出作用域时返回 nil，交给调用方按 st.Repos 处理。
func scopeRepos(ctx *appCtx) []*state.Repo {
	if ctx.res == nil || ctx.res.Scope.IsRoot() {
		return nil
	}
	return ctx.res.Repos
}

// inPool 仓库是否在这批里。pool 代表全集时恒真。
func inPool(pool []*state.Repo, r *state.Repo) bool {
	if pool == nil {
		return true
	}
	for _, p := range pool {
		if p.ID == r.ID {
			return true
		}
	}
	return false
}

// pickTarget 在当前作用域内定出命令的宾语。
//
// 所有"作用于某一个仓库"的命令都走它，别再各自 ctx.state.Find 一遍：
// 各自找一遍的结果就是有的认作用域、有的不认 —— 提示符上写着站在哪，命令却跑去别的盘上开窗口。
// 定不出来时原因和候选已经打出来了，调用方原样返回退出码即可。
func pickTarget(ctx *appCtx, cmd, kw string) *state.Repo {
	pool := scopeRepos(ctx)
	label := "全集"
	switch {
	case pool != nil:
		label = scopeLabel(ctx.res)
	case ctx.res == nil:
		label = "全集（当前目录不在扫描范围内）"
	}
	var in io.Reader
	if ctx.repl {
		in = os.Stdin // REPL 里能等人选；管道模式下不能，只列候选
	}
	return pick.Repo(pick.Opt{
		Cmd: cmd, Label: label, Pool: pool, All: ctx.state.Repos, In: in, Out: os.Stdout,
	}, kw)
}

func pickTargetGlobal(ctx *appCtx, cmd, kw string) *state.Repo {
	var in io.Reader
	if ctx.repl {
		in = os.Stdin
	}
	visible := scope.Visible(ctx.state)
	return pick.Repo(pick.Opt{
		Cmd: cmd, Label: "全集", Pool: visible, All: ctx.state.Repos, In: in, Out: os.Stdout,
	}, kw)
}

// poolHits 把全清单的命中收窄到作用域内。
//
// 批量命令（set / rm）一次本来就能吃多个宾语，所以不走 pickTarget 的单数语义，
// 但作用域的约束一样成立 —— 池外的命中一律丢掉，并且要说清楚丢了几个。
func poolHits(ctx *appCtx, kw string) []*state.Repo {
	pool := scopeRepos(ctx)
	hits := ctx.state.Find(kw)
	if pool == nil {
		return hits
	}
	var in []*state.Repo
	for _, h := range hits {
		if inPool(pool, h) {
			in = append(in, h)
		}
	}
	if len(in) == 0 && len(hits) > 0 {
		fmt.Fprintf(os.Stderr, "  %s 只匹配到作用域外的 %d 个，已跳过（要改先 cd 过去）\n", kw, len(hits))
	}
	return in
}

func scopeLabelOf(sc scope.Scope) string {
	if s := sc.TrailString(); s != "" && s != "/" {
		return s
	}
	return "全集"
}

// printScopeHead 每条受作用域影响的命令都要先回显，否则用户永远在猜。
func printScopeHead(res *scope.Resolved) {
	fmt.Printf("作用域  %s · %d 个仓库\n", scopeLabel(res), res.Totals.Repos)
	if res.Freshness != "" {
		fmt.Printf("  数据来自 %s的扫描（igit scan 刷新）\n", res.Freshness)
	}
	printSelf(res)
}

// printSelf 目录自身也是仓库时把话说明白。
// 默认的 file: 视角是"里面有什么"，自身被摘出去单独报 ——
// 不说的话这一层永远看不见，看它自己要显式写 repo:。
func printSelf(res *scope.Resolved) {
	if res.Self == nil {
		return
	}
	fmt.Printf("  （这里本身也是个仓库：%s · 看它自己用 cd repo:%s）\n", repoGist(res.Self), res.Self.Path)
}

// repoGist 一句话说完这个仓库最该知道的事。
func repoGist(r *state.Repo) string {
	parts := []string{}
	branch := r.Branch
	if r.Detached {
		branch = "detached"
	}
	if branch != "" {
		parts = append(parts, branch)
	}
	if r.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("领先 %d", r.Ahead))
	}
	if r.Behind > 0 {
		parts = append(parts, fmt.Sprintf("落后 %d", r.Behind))
	}
	if r.Dirty {
		parts = append(parts, "有未提交改动")
	}
	if !r.HasUpstream {
		parts = append(parts, "没配上游")
	}
	if r.Group != "" {
		parts = append(parts, "["+r.Group+"]")
	}
	if len(r.Tags) > 0 {
		var tags []string
		for _, t := range r.Tags {
			tags = append(tags, "#"+t)
		}
		parts = append(parts, strings.Join(tags, " "))
	}
	if len(parts) == 0 {
		return "干净"
	}
	return strings.Join(parts, " · ")
}

// setScope 把会话挪到指定作用域。只改内存，不落盘：
// 焦点是"我这会儿站在哪"，下次进来重新按当前目录推断。
func setScope(ctx *appCtx, sc scope.Scope) int {
	res, err := scope.Resolve(sc, ctx.state)
	if err != nil {
		fmt.Printf("进不去 %s：%v\n", scopeLabelOf(sc), err)
		return 1
	}
	ctx.prev, ctx.res = ctx.res, res // 记住来路，cd - 能回去
	fmt.Printf("作用域  %s · %d 个仓库\n", scopeLabel(res), res.Totals.Repos)
	printSelf(res)
	return 0
}

// ---------------------------------------------------------------- 作用域命令

type action struct{ cmd, desc string }

// cmdScope 是不带命令时的默认动作：把当前能做什么摆出来，
// 因为命令按节点收敛了，不摆出来用户看不见。
func cmdScope(ctx *appCtx, a args) int {
	res := ctx.res
	printScopeHead(res)
	fmt.Println()
	fmt.Println("  可用")
	for _, act := range availableActions(res) {
		fmt.Printf("    %s%s\n", util.PadRightDisplay(act.cmd, 14), act.desc)
	}
	return 0
}

func availableActions(res *scope.Resolved) []action {
	switch res.Kind {
	case scope.KindRepo:
		acts := []action{
			{"tig", "拉起 tig 查看提交历史与改动"},
			{"open", "在资源管理器中打开此目录"},
			{"ignore [add]", "查看或追加 .gitignore 规则 (支持 Tab 补全)"},
			{"remote [user]", "查看远端或绑定推送账号"},
			{"fetch", "拉取远端更新并分析同步拓扑"},
			{"pull", "安全快进同步 (阻止隐式合并与分叉)"},
			{"commit \"说明\"", "全量暂存并提交当前改动"},
			{"push", "推送到远端 (直接交互输入密码)"},
			{"stats", "查看此仓库详细统计画像"},
			{"list", "查看此仓库表格信息"},
			{"group [set]", "查看或调整所属分组"},
			{"tag [add|rm]", "查看或添加/移除标签"},
			{"status <状态>", "更新状态 (new|active|done|archived)"},
			{"note <备注>", "记录备注"},
			{"scan", "原位重新检测当前仓库状态"},
			{"dupes", "查询全盘中是否有此仓库副本"},
			{"cd ..", "原路返回上一层"},
			{"cd /", "回到全集"},
			{"pwd", "查看当前来时路"},
		}
		if len(res.Children) > 0 {
			acts = append(acts, action{"cd <子仓库>", "进入嵌套子仓库"})
		}
		return acts
	case scope.KindRoot:
		return []action{
			{"ls", "看维度入口：按目录 / 按分组 / 按标签"},
			{"list", "列出仓库（默认只看待处理）"},
			{"lsbytag", "响应式标签漏斗检索与就地导航"},
			{"tree", "看全局目录分布树"},
			{"stats", "查看全局聚合统计"},
			{"cd <目标>", "走进某个目录、标签或分组"},
			{"group list/new", "查看或新建全局分组"},
			{"tag list", "查看全局标签"},
			{"scan", "全盘扫描"},
			{"dupes", "全盘重复克隆比对"},
		}
	default:
		acts := []action{
			{"ls", "看这批的详细清单与子项"},
			{"list", "按表格看这批"},
			{"lsbytag", "响应式标签漏斗检索与就地导航"},
			{"tree", "看这批的空间分布树"},
			{"stats", "看这批的汇总统计"},
			{"cd <子项>", "进入子目录或仓库"},
			{"cd ..", "原路返回上一层"},
			{"cd /", "回到全集"},
			{"group list/new", "查看或新建全局分组"},
			{"tag list", "查看全局标签"},
			{"scan", "窄化刷新当前目录/范围"},
		}
		if res.Self != nil {
			acts = append(acts, action{"cd repo:<路径>", "看这个目录自身（它也是个仓库）"})
		}
		return acts
	}
}

// cmdLs 列当前作用域的子项（导航语义，不受"只看待处理"过滤）。
func cmdLs(ctx *appCtx, a args) int {
	refreshCurrentScope(ctx)
	res := ctx.res
	printScopeHead(res)
	fmt.Println()
	switch res.Kind {
	case scope.KindRepo:
		if len(res.Children) > 0 {
			fmt.Println("  嵌套子仓库:")
			for _, it := range res.Children {
				fmt.Printf("    %s%s\n", util.PadRightDisplay(it.Label, 26), it.Summary)
			}
			return 0
		}
		if target := res.TargetRepo(); target != nil {
			fmt.Printf("  这是单个仓库: %s (%s)\n", target.Name, repoGist(target))
			fmt.Println("  可用操作: tig, open, stats, list, group, tag, status, note; 回上一层用 cd ..")
		} else {
			fmt.Println("  这是单个仓库，回上一层用 cd ..")
		}
		return 0
	case scope.KindRoot:
		for _, f := range []struct{ dim, title string }{
			{scope.DimFile, "按目录"},
			{scope.DimGroup, "按分组"},
			{scope.DimTag, "按标签"},
		} {
			fmt.Printf("  %s\n", f.title)
			for _, it := range res.Children {
				if it.Facet != f.dim {
					continue
				}
				if it.Count == 0 && it.Target == "" {
					fmt.Printf("    %s\n", it.Label)
					continue
				}
				fmt.Printf("    %s%d 个\n", util.PadRightDisplay(it.Label, 26), it.Count)
			}
			fmt.Println()
		}
		return 0
	}

	t := res.Totals
	fmt.Printf("  %s%d 个\n", util.PadRightDisplay(scopeLabel(res), 14), t.Repos)
	if t.Ahead > 0 {
		fmt.Printf("    领先远端      %d 个\n", t.Ahead)
	}
	if t.Dirty > 0 {
		fmt.Printf("    工作区脏      %d 个\n", t.Dirty)
	}
	if t.NoUpstream > 0 {
		fmt.Printf("    无上游        %d 个\n", t.NoUpstream)
	}
	if t.Missing > 0 {
		fmt.Printf("    已丢失        %d 个\n", t.Missing)
	}
	fmt.Println()
	fmt.Println("  子项")
	if len(res.Children) == 0 {
		fmt.Println("    （暂无仓库）")
	} else {
		for _, it := range res.Children {
			if it.Facet == scope.DimRepo {
				fmt.Printf("    %s%s\n", util.PadRightDisplay(it.Label, 26), it.Summary)
				continue
			}
			fmt.Printf("    %s%d 个\n", util.PadRightDisplay(it.Label, 26), it.Count)
		}
	}
	return 0
}

func cmdPwd(ctx *appCtx, a args) int {
	if ctx.res != nil {
		fmt.Printf("来时路: %s\n", ctx.res.Scope.TrailString())
		return 0
	}
	fmt.Println("/")
	return 0
}

func cmdCd(ctx *appCtx, a args) int {
	target := ""
	if len(a.pos) > 0 {
		target = strings.TrimSpace(a.pos[0])
	}

	// 一次性用法里进程结束焦点就没了，说清楚比默默执行强
	if !ctx.repl && target != "" {
		fmt.Println("（cd 只在会话里有效。一次性用法：先 cd 到那个目录，或用 igit --in=<作用域> <命令>）")
		return 1
	}

	switch target {
	case "":
		if !hasScope(ctx) {
			return 1
		}
		fmt.Println(scopeLabel(ctx.res))
		return 0
	case "/":
		return setScope(ctx, scope.Scope{})
	case ".", "auto":
		if !resolveFromCWD(ctx) {
			return 1
		}
		fmt.Printf("作用域  %s · %d 个仓库\n", scopeLabel(ctx.res), ctx.res.Totals.Repos)
		return 0
	case "-":
		// 回到来路。尤其有用：从集合进到"它自己"之后，cd .. 会一路退到全集，
		// 而人多半只是想退回刚才那一层。
		if ctx.prev == nil {
			fmt.Println("（没有上一个作用域。cd .. 上一层，cd / 回全集）")
			return 1
		}
		ctx.prev, ctx.res = ctx.res, ctx.prev
		fmt.Printf("作用域  %s · %d 个仓库\n", scopeLabel(ctx.res), ctx.res.Totals.Repos)
		printSelf(ctx.res)
		return 0
	case "..":
		if !hasScope(ctx) {
			return 1
		}
		return setScope(ctx, ctx.res.Scope.Parent())
	}

	sc, err := scope.Parse(target)
	if err != nil {
		fmt.Printf("读不懂这个作用域: %s（%v）\n", target, err)
		return 1
	}
	// 写了维度前缀就是绝对定位（`repo:D:\x` 表示"我要看这一个"，不管现在站在哪）；
	// 光秃秃一个名字才是相对当前位置下钻。
	if sc.Segs[0].Explicit {
		return setScope(ctx, sc)
	}
	if sc.Segs[0].Dim == scope.DimRepo {
		if !hasScope(ctx) {
			return 1
		}
		cur := ctx.res.Scope
		if cur.IsRoot() {
			guess, ok := guessFromRoot(ctx, target)
			if !ok {
				fmt.Printf("在全集里找不到 %s（分组/标签/仓库都没匹配上）\n", target)
				return 1
			}
			sc = guess
		} else {
			sc = descendFrom(ctx, cur, target)
		}
	}
	return setScope(ctx, sc)
}

// hasScope 当前目录不在扫描范围内时 ctx.res 是空的，此时相对导航无从谈起。
func hasScope(ctx *appCtx) bool {
	if ctx.res != nil {
		return true
	}
	fmt.Println("（当前目录不在扫描范围内，还没定出作用域。igit cd / 看全集，或 cd tag:xxx 直接定位）")
	return false
}

// descendFrom 往下走一步。file 段下优先当子目录 —— ls 的列的就是这个顺序，
// 用户照着 ls 的输出敲就不会走错地方。
func descendFrom(ctx *appCtx, cur scope.Scope, name string) scope.Scope {
	if cur.LastIsFile() {
		cand := cur.DescendFile(name)
		if _, err := scope.Resolve(cand, ctx.state); err == nil {
			return cand
		}
	}
	return cur.Child(scope.Seg{Dim: scope.DimRepo, Ident: name})
}

// guessFromRoot 在根下敲一个裸名字时，依次猜分组 / 标签 / 仓库。
func guessFromRoot(ctx *appCtx, name string) (scope.Scope, bool) {
	for _, dim := range []string{scope.DimGroup, scope.DimTag, scope.DimRepo} {
		cand := scope.Scope{Segs: []scope.Seg{{Dim: dim, Ident: name}}}
		if _, err := scope.Resolve(cand, ctx.state); err == nil {
			return cand, true
		}
	}
	return scope.Scope{}, false
}

// ---------------------------------------------------------------- scan

func cmdScan(ctx *appCtx, a args) int {
	roots := a.multi["root"]
	if len(roots) == 0 {
		if !a.has("all") && ctx.res != nil && !ctx.res.Scope.IsRoot() {
			if target := ctx.res.TargetRepo(); target != nil {
				roots = []string{target.Path}
				fmt.Printf("原位刷新当前仓库: %s\n", target.Path)
			} else if baseDir := ctx.res.Scope.BaseDir(); baseDir != "" {
				roots = []string{baseDir}
				fmt.Printf("原位窄化扫描目录: %s\n", baseDir)
			}
		}
		if len(roots) == 0 {
			roots = ctx.cfg.Roots
		}
	}
	roots = normalizeRoots(roots)
	if len(roots) == 0 {
		fmt.Println("还没有配置扫描根目录。")
		fmt.Println("  先跑  igit config discover  探查一下，再  igit config add-root <目录>")
		return 1
	}

	ignored := map[string]bool{}
	for _, r := range ctx.state.Repos {
		if r.Ignored {
			ignored[r.ID] = true
		}
	}
	ignoredIDs := make([]string, 0, len(ignored))
	for id := range ignored {
		ignoredIDs = append(ignoredIDs, id)
	}

	// 进度只在真终端里用 \b 重绘；重定向到文件/管道时保持安静，避免留下控制字符
	lastLine := 0
	onProgress := func(visited, found int) {
		clearLine(&lastLine, fmt.Sprintf("  遍历目录 %d  发现仓库 %d", visited, found))
	}
	tty := isTerminal(os.Stderr)
	if !tty {
		onProgress = nil
	}

	fmt.Fprintf(os.Stderr, "扫描中（%d 个根目录，并发 %d）...\n", len(roots), jobsOf(a, ctx.cfg))
	res, err := scan.Run(scan.Options{
		Roots:        roots,
		ExcludeNames: ctx.cfg.ExcludeNames,
		ExcludePaths: ctx.cfg.ExcludePaths,
		SkipPrefixes: ctx.state.HiddenPaths(),
		IgnoredIDs:   ignored,
		MaxDepth:     a.int(ctx.cfg.MaxDepth, "max_depth", "depth"),
		Jobs:         jobsOf(a, ctx.cfg),
		Fast:         ctx.cfg.Fast || a.has("fast"),
		Deep:         ctx.cfg.Deep || a.has("deep"),
		Size:         a.has("size"),
		GitExe:       ctx.cfg.GitExe,
		Progress:     onProgress,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n扫描失败: %v\n", err)
		return 1
	}
	final := fmt.Sprintf("  遍历目录 %d  发现仓库 %d", res.Dirs, len(res.Repos))
	if tty {
		clearLine(&lastLine, final)
		fmt.Fprintln(os.Stderr, final)
	}
	fmt.Fprintln(os.Stderr)

	seen := map[string]bool{}
	for _, r := range res.Repos {
		seen[r.ID] = true
	}
	// 这几类都不会出现在扫描结果里，自然也不该被判成"磁盘已找不到"
	noMissing := append([]string{}, ignoredIDs...)
	noMissing = append(noMissing, ctx.state.HiddenPaths()...)
	noMissing = append(noMissing, ctx.cfg.ExcludePaths...)
	added := ctx.state.Upsert(res.Repos, seen, roots, noMissing)

	missing := 0
	for _, r := range ctx.state.Repos {
		if r.Missing {
			missing++
		}
	}
	ctx.state.Scans = append(ctx.state.Scans, state.ScanRecord{
		At:         time.Now().Format(time.RFC3339),
		Roots:      roots,
		Found:      len(res.Repos),
		New:        added,
		Missing:    missing,
		SkippedIgn: res.SkippedIgnore,
		DurationMS: res.Duration.Milliseconds(),
		Dirs:       res.Dirs,
		Fast:       ctx.cfg.Fast || a.has("fast"),
	})
	if len(ctx.state.Scans) > 50 {
		ctx.state.Scans = ctx.state.Scans[len(ctx.state.Scans)-50:]
	}
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存状态失败: %v\n", err)
		return 1
	}

	pending := pendingCount(ctx.state, nil)
	fmt.Printf("扫描完成，用时 %s\n", roundDuration(res.Duration))
	fmt.Printf("  遍历目录   %d\n", res.Dirs)
	fmt.Printf("  扫到仓库   %d（新增 %d）\n", len(res.Repos), added)
	if res.SkippedIgnore > 0 {
		fmt.Printf("  已忽略跳过 %d\n", res.SkippedIgnore)
	}
	if missing > 0 {
		fmt.Printf("  磁盘已无   %d（运行 igit prune 清理）\n", missing)
	}
	fmt.Printf("  清单总数   %d，其中待处理 %d\n", len(ctx.state.Repos), pending)
	fmt.Println()
	fmt.Printf("状态文件: %s\n", ctx.state.Path())
	if pending > 0 {
		fmt.Println("下一步: igit triage   逐个判别；或 igit list 看清单")
	} else {
		fmt.Println("待处理已清空。")
	}
	return 0
}

func jobsOf(a args, cfg *config.Config) int {
	if v := a.int(0, "jobs", "j"); v > 0 {
		return v
	}
	if cfg.Jobs > 0 {
		return cfg.Jobs
	}
	return 8
}

func normalizeRoots(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		abs, err := filepath.Abs(strings.TrimSpace(strings.Trim(r, `"`)))
		if err != nil {
			continue
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "  跳过不可用的根目录: %s\n", abs)
			continue
		}
		k := strings.ToLower(filepath.Clean(abs))
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, abs)
	}
	return out
}

// pendingCount 数待处理。pool 为空表示数全集。
func pendingCount(st *state.State, pool []*state.Repo) int {
	if pool == nil {
		pool = st.Repos
	}
	n := 0
	for _, r := range pool {
		if r.Ignored || r.Missing || st.IsHidden(r.Path) {
			continue
		}
		if r.Status == "" || r.Status == state.StatusNew {
			n++
		}
	}
	return n
}

func roundDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

var consoleWidthGuess = func() int {
	w := os.Getenv("COLUMNS")
	if n := atoi(w); n > 40 {
		return n
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func clearLine(last *int, msg string) {
	n := *last
	*last = len([]rune(msg))
	if n > 0 {
		fmt.Fprint(os.Stderr, strings.Repeat("\b", n))
	}
	fmt.Fprint(os.Stderr, msg)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------- list

func cmdList(ctx *appCtx, a args) int {
	ctx.state.SortBy(state.ParseSortField(a.str("path", "sort", "s")), a.has("desc", "r"))
	selected := filterRepos(ctx.state, a, ctx.res.Repos)
	matched := len(selected)

	// 先折叠、再截断：否则 --limit 会把折叠组切碎，
	// "90 个仓库的目录" 会被显示成 "4 个仓库"
	allEntries := foldEntries(ctx.state, selected)
	totalRows := len(allEntries)
	entries := allEntries
	if n := a.int(0, "limit", "n"); n > 0 && n < len(entries) {
		entries = entries[:n]
	}

	if a.has("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(selected)
		return 0
	}
	printScopeHead(ctx.res)
	fmt.Println()
	if len(selected) == 0 {
		fmt.Println("没有匹配的仓库。加 --all 看看全部，或者先跑一次 scan。")
		return 0
	}

	width := a.int(0, "width", "w")
	if width <= 0 {
		width = 120
		if w := consoleWidthGuess(); w > 0 {
			width = w
		}
	}
	// 默认显示完整路径：长信息的价值高于对齐的美观，
	// 要窄表格自己加 --short
	v := render.View{Roots: ctx.cfg.Roots, Width: width, Full: !a.has("short")}
	fmt.Println(v.Header())
	for _, e := range entries {
		if e.fold != nil {
			fmt.Println(v.FoldRow(e.fold.dir, e.fold.repos, countUnderDir(ctx.state, e.fold.dir)))
			continue
		}
		fmt.Println(v.Row(e.repo))
	}
	fmt.Println()
	fmt.Printf("共 %d 个仓库", matched)
	switch {
	case totalRows < matched && len(entries) < totalRows:
		fmt.Printf("（折叠后 %d 行，本次显示 %d 行）", totalRows, len(entries))
	case totalRows < matched:
		fmt.Printf("（折叠成 %d 行）", totalRows)
	case len(entries) < matched:
		fmt.Printf("（显示 %d 行，加 --limit 调整）", len(entries))
	}
	fmt.Println()
	return 0
}

// countUnderDir 统计某个目录下清单里的仓库总数，不受当前筛选影响。
func countUnderDir(st *state.State, dir string) int {
	n := 0
	for _, r := range st.Repos {
		if util.HasPrefixPath(r.Path, dir) {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- tree

// cmdTree 按磁盘上的目录结构把仓库摆出来，看的是"仓库在机器上怎么分布"，
// 而不是清单顺序。
func cmdTree(ctx *appCtx, a args) int {
	ctx.state.SortBy(state.SortPath, false)
	if ctx.res != nil && !ctx.res.Scope.IsRoot() {
		printScopeHead(ctx.res)
		fmt.Println()
	}
	selected := filterRepos(ctx.state, a, scopeRepos(ctx))
	if len(selected) == 0 {
		fmt.Println("没有匹配的仓库。加 --all 看看全部，或者先跑一次 scan。")
		return 0
	}
	nodes := tree.Build(selected)
	if len(nodes) == 0 {
		fmt.Println("没有可展示的目录结构。")
		return 0
	}

	width := a.int(0, "width", "w")
	if width <= 0 {
		width = 120
		if w := consoleWidthGuess(); w > 0 {
			width = w
		}
	}
	opt := tree.Opt{
		Depth:    a.int(0, "depth", "d"),
		Brief:    a.has("brief", "b"),
		ShowAll:  true,
		MaxWidth: 0,
		Folded:   ctx.state.FoldedPaths(),
	}
	lines := tree.Render(nodes, opt)
	for _, l := range lines {
		fmt.Println(l)
	}
	groups := tree.Groups(nodes)
	multi := 0
	for _, g := range groups {
		if g.Count > 1 {
			multi++
		}
	}
	fmt.Println()
	fmt.Printf("共 %d 个仓库，分布在 %d 个目录里\n", len(selected), len(groups))
	if multi > 0 {
		fmt.Printf("其中 %d 个目录塞了不止一个仓库\n", multi)
	}
	fmt.Println("提示: --brief 只看骨架 / --depth=N 控制展开层数 / 过滤参数同 list")
	return 0
}

// foldGroup 一个被折叠的目录，压成一行显示。
type foldGroup struct {
	dir   string
	repos []*state.Repo
}

// listEntry 列表里的一行：要么是一个仓库，要么是一个折叠起来的目录。
type listEntry struct {
	sortKey string
	repo    *state.Repo
	fold    *foldGroup
}

// foldEntries 把落在折叠目录下的仓库收拢成一条，其余照常单列。
// 折叠组沿用组内首个成员在排序中的位置，所以整体顺序和原来一致。
func foldEntries(st *state.State, in []*state.Repo) []listEntry {
	out := make([]listEntry, 0, len(in))
	at := map[string]int{}
	for _, r := range in {
		dir := st.IsFolded(r.Path)
		if dir == "" {
			out = append(out, listEntry{sortKey: r.Path, repo: r})
			continue
		}
		if i, ok := at[dir]; ok {
			out[i].fold.repos = append(out[i].fold.repos, r)
			continue
		}
		at[dir] = len(out)
		out = append(out, listEntry{sortKey: r.Path, fold: &foldGroup{dir: dir, repos: []*state.Repo{r}}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].sortKey < out[j].sortKey })
	if len(at) == 0 {
		return out
	}
	// 折叠组再按目录名排个序，读起来更整齐
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].fold != nil) != (out[j].fold != nil) {
			return out[i].repo == nil // 折叠行靠后，视觉上像"小结"
		}
		return out[i].sortKey < out[j].sortKey
	})
	return out
}

// filterRepos 在一批仓库里按命令行参数筛选。in 为 nil 时表示全集。
// 被 hide 的目录任何视图都不出现，--all 也不破例。
func filterRepos(st *state.State, a args, in []*state.Repo) []*state.Repo {
	if in == nil {
		in = st.Repos
	}
	all := a.has("all", "a")
	showIgnored := a.has("ignored")
	showMissing := a.has("missing")
	wantGroup := a.str("", "group", "g")
	wantTag := a.str("", "tag", "t")
	wantStatus := normalizeStatus(a.str("", "status"))
	wantKind := a.str("", "kind", "k")
	query := strings.ToLower(a.str("", "query", "q"))
	wantDirty := a.has("dirty")
	wantClean := a.has("clean")
	wantUnpushed := a.has("unpushed", "ahead")
	wantNoRemote := a.has("no_remote", "noremote")

	var out []*state.Repo
	for _, r := range in {
		if st.IsHidden(r.Path) {
			continue
		}
		if !all {
			// --ignored / --missing 是"只要这一类"的强开关，不再叠加"待处理"的默认过滤
			if showIgnored {
				if !r.Ignored {
					continue
				}
			} else if r.Ignored {
				continue
			}
			if showMissing {
				if !r.Missing {
					continue
				}
			} else if r.Missing {
				continue
			}
			if !showIgnored && !showMissing {
				if !(r.Status == "" || r.Status == state.StatusNew) {
					continue
				}
			}
		}
		if wantStatus != "" && canonicalStatus(r) != wantStatus {
			continue
		}
		if wantGroup != "" && !strings.EqualFold(r.Group, wantGroup) {
			continue
		}
		if wantTag != "" && !hasTag(r, wantTag) {
			continue
		}
		if wantKind != "" && !strings.EqualFold(r.Kind, wantKind) {
			continue
		}
		if wantDirty && !r.Dirty {
			continue
		}
		if wantClean && r.Dirty {
			continue
		}
		if wantUnpushed && r.Ahead <= 0 {
			continue
		}
		if wantNoRemote && r.PrimaryURL != "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(r.Path), query) &&
			!strings.Contains(strings.ToLower(r.Name), query) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func hasTag(r *state.Repo, tag string) bool {
	for _, t := range r.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func canonicalStatus(r *state.Repo) string {
	switch {
	case r.Ignored:
		return "ignored"
	case r.Status == state.StatusActive:
		return state.StatusActive
	case r.Status == state.StatusDone:
		return state.StatusDone
	case r.Status == state.StatusArchived:
		return state.StatusArchived
	default:
		return state.StatusNew
	}
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "new", "待处理":
		return state.StatusNew
	case "active", "wip", "跟进中", "进行中":
		return state.StatusActive
	case "done", "已处理", "完成":
		return state.StatusDone
	case "archived", "已归档", "归档":
		return state.StatusArchived
	case "ignored", "ignore", "已忽略":
		return "ignored"
	default:
		return ""
	}
}

// ---------------------------------------------------------------- set

func cmdSet(ctx *appCtx, a args) int {
	if len(a.pos) == 0 {
		fmt.Println("用法: igit set <关键字> [--group=X] [--status=X] [--tag=a,b] [--untag=a,b] [--note=X] [--ignore|--unignore] [--yes]")
		fmt.Println("关键字可以是完整路径、路径片段或仓库名。")
		return 2
	}
	targets := map[string]*state.Repo{}
	for _, q := range a.pos {
		hits := poolHits(ctx, q)
		if len(hits) == 0 {
			fmt.Fprintf(os.Stderr, "没有匹配到仓库: %s\n", q)
			continue
		}
		for _, h := range hits {
			targets[h.ID] = h
		}
	}
	if len(targets) == 0 {
		return 1
	}

	list := make([]*state.Repo, 0, len(targets))
	for _, r := range targets {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	fmt.Printf("将要修改 %d 个仓库:\n", len(list))
	v := render.View{Roots: ctx.cfg.Roots, Width: 110, Full: true}
	for _, r := range list {
		fmt.Println("  " + v.Row(r))
	}
	if len(list) > 1 && !a.has("yes", "y") {
		if !confirmPrompt(ctx, fmt.Sprintf("确认修改这 %d 个? [y/N] ", len(list))) {
			fmt.Println("已取消。")
			return 0
		}
	}

	group, hasGroup := a.kv["group"]
	statusArg, hasStatus := a.kv["status"]
	addTags := splitList(a.str("", "tag", "tags"))
	delTags := splitList(a.str("", "untag", "untags"))
	note, hasNote := a.kv["note"]
	dropNote := a.has("clear_note")
	dropGroup := a.has("clear_group")
	ignore := a.has("ignore")
	unignore := a.has("unignore")
	dropTags := a.has("clear_tags")

	if hasStatus && normalizeStatus(statusArg) == "" {
		fmt.Fprintf(os.Stderr, "无法识别的状态 %q，可选: new/active/done/archived\n", statusArg)
		return 2
	}

	now := time.Now().Format(time.RFC3339)
	for _, r := range list {
		if hasGroup {
			r.Group = strings.TrimSpace(group)
		}
		if dropGroup {
			r.Group = ""
		}
		if hasStatus {
			r.Status = normalizeStatus(statusArg)
		}
		if dropTags {
			r.Tags = nil
		}
		for _, t := range addTags {
			if !hasTag(r, t) {
				r.Tags = append(r.Tags, t)
			}
		}
		for _, t := range delTags {
			r.Tags = removeTag(r, t)
		}
		if hasNote {
			r.Note = note
		}
		if dropNote {
			r.Note = ""
		}
		if ignore {
			r.Ignored = true
		}
		if unignore {
			r.Ignored = false
		}
		if hasGroup || hasStatus || len(addTags) > 0 || len(delTags) > 0 || hasNote || ignore || unignore {
			r.ReviewedAt = now
		}
		state.Canonicalize(r)
	}
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	fmt.Printf("已更新 %d 个仓库 -> %s\n", len(list), ctx.state.Path())
	return 0
}

// printAllGroupsWithHints 列出所有分组并附带交互式引导提示。
func printAllGroupsWithHints(ctx *appCtx) int {
	all := ctx.state.AllGroups()
	if len(all) == 0 {
		fmt.Println("当前尚未创建任何分组。可用 'group new <名称>' 或 'set --group=<名称>' 创建。")
		return 0
	}
	groups := map[string]int{}
	for _, r := range ctx.state.Repos {
		if r.Group != "" {
			groups[r.Group]++
		}
	}
	fmt.Println("现有分组:")
	for _, k := range all {
		fmt.Printf("  %-20s %d 个仓库\n", k, groups[k])
	}
	fmt.Println("\n提示:")
	fmt.Println("  在单仓下输入 'group' 可直接按编号分配/切换分组;")
	fmt.Println("  在全集下输入 'group <仓库名>' 可为指定仓库选择分组;")
	fmt.Println("  输入 'group new <名称>' 可新建空分组。")
	return 0
}

// pickGroupForRepo 为指定仓库交互式挑选目标分组（依据现有分组编号选择，支持新建与清除）。
// @design docs/DESIGN-group-tag.md#31-仓库选择分组菜单原型-pickgroupforrepo
func pickGroupForRepo(ctx *appCtx, r *state.Repo) bool {
	all := ctx.state.AllGroups()
	curGroup := r.Group

	if curGroup == "" {
		fmt.Printf("\n仓库: %s (当前未设置分组)\n", r.Name)
	} else {
		fmt.Printf("\n仓库: %s (当前分组: %s)\n", r.Name, curGroup)
	}

	if len(all) == 0 {
		fmt.Println("当前尚未创建任何分组。")
		fmt.Println("  +) [新建分组并加入...]")
		fmt.Println("  q) 取消")
		fmt.Println()
		choice := readPrompt(ctx, "请选择 (+ / q 取消): ")
		if choice == "+" || strings.EqualFold(choice, "new") || strings.EqualFold(choice, "add") {
			name := readPrompt(ctx, "请输入新分组名称: ")
			name = strings.TrimSpace(name)
			if name == "" {
				fmt.Println("分组名称不能为空，已取消。")
				return false
			}
			r.Group = name
			r.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(r)
			ctx.state.AddGroup(name)
			if err := ctx.state.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
				return false
			}
			refreshCurrentScope(ctx)
			fmt.Printf("已创建分组并设置 %s 为: %s\n", r.Name, r.Group)
			return true
		}
		fmt.Println("已取消。")
		return false
	}

	fmt.Println("\n请选择目标分组:")
	for i, g := range all {
		mark := "  "
		if strings.EqualFold(g, curGroup) {
			mark = "[√]"
		}
		fmt.Printf("  %2d) %s %s\n", i+1, mark, g)
	}
	fmt.Println("   +) [新建分组...]")
	if curGroup != "" {
		fmt.Println("   0) [清除分组] (移出当前分组)")
	}
	fmt.Println()

	choice := readPrompt(ctx, "请选择 (编号 / + / 0 / q 取消): ")
	if choice == "" || strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") {
		fmt.Println("已取消。")
		return false
	}

	var targetGroup string
	switch choice {
	case "0", "clear", "unset":
		if curGroup == "" {
			fmt.Println("当前尚未设置分组，无需清除。")
			return false
		}
		r.Group = ""
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return false
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已清除 %s 的分组。\n", r.Name)
		return true
	case "+", "new", "add":
		name := readPrompt(ctx, "请输入新分组名称: ")
		name = strings.TrimSpace(name)
		if name == "" {
			fmt.Println("分组名称不能为空，已取消。")
			return false
		}
		targetGroup = name
	default:
		idx, err := strconv.Atoi(choice)
		if err == nil && idx >= 1 && idx <= len(all) {
			targetGroup = all[idx-1]
		} else {
			if ctx.state.HasGroup(choice) {
				targetGroup = choice
			} else {
				fmt.Printf("无效的选项: %s\n", choice)
				return false
			}
		}
	}

	if strings.EqualFold(targetGroup, curGroup) {
		fmt.Printf("仓库 %s 已属于分组 [%s]，无需变更。\n", r.Name, curGroup)
		return false
	}

	// 唯一性替换确认：若原先已有分组且不同，必须确认
	if curGroup != "" {
		if !confirmPrompt(ctx, fmt.Sprintf("仓库 %s 当前属于 [%s]，确认变更为 [%s]? [y/N] ", r.Name, curGroup, targetGroup)) {
			fmt.Println("已取消。")
			return false
		}
	}

	r.Group = targetGroup
	r.ReviewedAt = time.Now().Format(time.RFC3339)
	state.Canonicalize(r)
	ctx.state.AddGroup(targetGroup)
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return false
	}
	refreshCurrentScope(ctx)
	fmt.Printf("已将 %s 分组设置为: %s\n", r.Name, r.Group)
	return true
}

// pickRepoToRemoveFromGroup 在分组作用域下交互式列出组内仓库并移出。
// @design docs/DESIGN-group-tag.md#33-分组内移出仓库原型-pickrepotoremovefromgroup
func pickRepoToRemoveFromGroup(ctx *appCtx, grpName string) bool {
	var repos []*state.Repo
	for _, r := range ctx.state.Repos {
		if strings.EqualFold(r.Group, grpName) {
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		fmt.Printf("分组 %s 内当前没有仓库。\n", grpName)
		return false
	}

	fmt.Printf("\n分组 %s 内的仓库 (共 %d 个):\n", grpName, len(repos))
	for i, r := range repos {
		fmt.Printf("  %2d) %s\n", i+1, r.Name)
	}
	fmt.Println()

	choice := readPrompt(ctx, "请选择要移出的仓库 (序号 / q 取消): ")
	if choice == "" || strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") {
		fmt.Println("已取消。")
		return false
	}

	var target *state.Repo
	idx, err := strconv.Atoi(choice)
	if err == nil && idx >= 1 && idx <= len(repos) {
		target = repos[idx-1]
	} else {
		for _, r := range repos {
			if strings.EqualFold(r.Name, choice) || strings.EqualFold(r.ID, choice) {
				target = r
				break
			}
		}
	}
	if target == nil {
		fmt.Printf("无效的选项: %s\n", choice)
		return false
	}

	target.Group = ""
	target.ReviewedAt = time.Now().Format(time.RFC3339)
	state.Canonicalize(target)
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return false
	}
	refreshCurrentScope(ctx)
	fmt.Printf("已将 %s 从分组 %s 移出。\n", target.Name, grpName)
	return true
}

// pickGroupActionInGroupScope 在分组作用域下输入 group 时弹出本组管理菜单。
// @design docs/DESIGN-group-tag.md#2-作用域推断矩阵-scope-inference-matrix
func pickGroupActionInGroupScope(ctx *appCtx, grpName string) int {
	var count int
	for _, r := range ctx.state.Repos {
		if strings.EqualFold(r.Group, grpName) {
			count++
		}
	}
	fmt.Printf("\n分组: %s (共 %d 个仓库)\n", grpName, count)
	fmt.Println("  1) 移出仓库 (rm / clear)")
	fmt.Println("  2) 添加仓库 (add)")
	fmt.Println("  3) 查看所有分组 (list)")
	fmt.Println()

	choice := readPrompt(ctx, "请选择操作 (编号 / q 取消): ")
	switch choice {
	case "1", "rm", "clear":
		pickRepoToRemoveFromGroup(ctx, grpName)
		return 0
	case "2", "add":
		kw := readPrompt(ctx, "请输入要加入该分组的仓库名称/关键字: ")
		if kw == "" {
			fmt.Println("已取消。")
			return 0
		}
		r := pickTargetGlobal(ctx, "group add", kw)
		if r == nil {
			return 2
		}
		if strings.EqualFold(r.Group, grpName) {
			fmt.Printf("仓库 %s 当前已在分组 %s 中。\n", r.Name, grpName)
			return 0
		}
		if r.Group != "" {
			if !confirmPrompt(ctx, fmt.Sprintf("仓库 %s 当前属于 [%s]，确认变更为 [%s]? [y/N] ", r.Name, r.Group, grpName)) {
				fmt.Println("已取消。")
				return 0
			}
		}
		r.Group = grpName
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已将 %s 加入分组: %s\n", r.Name, grpName)
		return 0
	case "3", "list", "ls":
		return printAllGroupsWithHints(ctx)
	default:
		if choice != "" && !strings.EqualFold(choice, "q") && !strings.EqualFold(choice, "quit") {
			fmt.Printf("无效的选项: %s\n", choice)
		}
		return 0
	}
}

func cmdGroup(ctx *appCtx, a args) int {
	sub := ""
	if len(a.pos) > 0 {
		sub = a.pos[0]
	}
	switch sub {
	case "", "list", "ls":
		if sub == "" {
			if target := ctx.res.TargetRepo(); target != nil {
				pickGroupForRepo(ctx, target)
				return 0
			}
			if ctx.res != nil && ctx.res.NodeType == scope.NodeGroup && len(ctx.res.Scope.Segs) > 0 {
				grpName := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
				return pickGroupActionInGroupScope(ctx, grpName)
			}
		}
		return printAllGroupsWithHints(ctx)

	case "new", "create":
		if len(a.pos) < 2 {
			fmt.Println("用法: group new <分组名称>")
			return 2
		}
		name := strings.TrimSpace(a.pos[1])
		if name == "" {
			fmt.Println("分组名称不能为空")
			return 2
		}
		if !ctx.state.AddGroup(name) {
			fmt.Printf("分组已存在: %s\n", name)
			return 0
		}
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("分组已就绪: %s（给仓库分配请用 'group set <仓库> %s'）\n", name, name)
		return 0

	case "add":
		// 在分组作用域下，group add [kw] 表示将仓库拉入当前组
		if ctx.res != nil && ctx.res.NodeType == scope.NodeGroup && len(ctx.res.Scope.Segs) > 0 {
			currentGroup := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			var kw string
			if len(a.pos) >= 2 {
				kw = a.pos[1]
			} else {
				kw = readPrompt(ctx, "请输入要加入该分组的仓库名称/关键字: ")
				if kw == "" {
					fmt.Println("已取消。")
					return 0
				}
			}
			r := pickTargetGlobal(ctx, "group add", kw)
			if r == nil {
				return 2
			}
			if strings.EqualFold(r.Group, currentGroup) {
				fmt.Printf("仓库 %s 当前已在分组 %s 中。\n", r.Name, currentGroup)
				return 0
			}
			if r.Group != "" && !a.has("yes", "y") {
				if !confirmPrompt(ctx, fmt.Sprintf("仓库 %s 当前属于 [%s]，确认变更为 [%s]? [y/N] ", r.Name, r.Group, currentGroup)) {
					fmt.Println("已取消。")
					return 0
				}
			}
			r.Group = currentGroup
			r.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(r)
			if err := ctx.state.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
				return 1
			}
			refreshCurrentScope(ctx)
			fmt.Printf("已将 %s 加入分组: %s\n", r.Name, currentGroup)
			return 0
		}
		// 否则作为 group new 的别名
		if len(a.pos) < 2 {
			fmt.Println("用法: group add <分组名称>")
			return 2
		}
		name := strings.TrimSpace(a.pos[1])
		if !ctx.state.AddGroup(name) {
			fmt.Printf("分组已存在: %s\n", name)
			return 0
		}
		_ = ctx.state.Save()
		refreshCurrentScope(ctx)
		fmt.Printf("分组已就绪: %s\n", name)
		return 0

	case "set":
		var kw, grpName string
		target := ctx.res.TargetRepo()
		if len(a.pos) == 1 {
			if target != nil {
				pickGroupForRepo(ctx, target)
				return 0
			}
			fmt.Println("用法: group set <仓库关键字> [分组名称]")
			return 2
		} else if len(a.pos) == 2 {
			if target != nil {
				grpName = a.pos[1]
			} else if ctx.res != nil && ctx.res.NodeType == scope.NodeGroup && len(ctx.res.Scope.Segs) > 0 {
				kw = a.pos[1]
				grpName = ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			} else {
				// 用户在全局敲了 group set <仓库名>，缺少分组名 -> 直接进入该仓库的选择菜单！
				kw = a.pos[1]
				r := pickTarget(ctx, "group set", kw)
				if r == nil {
					return 2
				}
				pickGroupForRepo(ctx, r)
				return 0
			}
		} else if len(a.pos) >= 3 {
			kw = a.pos[1]
			grpName = a.pos[2]
		}
		var r *state.Repo
		if kw != "" {
			r = pickTarget(ctx, "group set", kw)
			if r == nil {
				return 2
			}
		} else {
			r = target
		}
		grpName = strings.TrimSpace(grpName)
		if r.Group != "" && !strings.EqualFold(r.Group, grpName) && !a.has("yes", "y") {
			if !confirmPrompt(ctx, fmt.Sprintf("仓库 %s 当前属于 [%s]，确认变更为 [%s]? [y/N] ", r.Name, r.Group, grpName)) {
				fmt.Println("已取消。")
				return 0
			}
		}
		r.Group = grpName
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		ctx.state.AddGroup(r.Group)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已将 %s 分组设置为: %s\n", r.Name, r.Group)
		return 0

	case "rm", "del", "delete":
		// 分组作用域下：
		if ctx.res != nil && ctx.res.NodeType == scope.NodeGroup && len(ctx.res.Scope.Segs) > 0 {
			currentGroup := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			if len(a.pos) == 1 {
				pickRepoToRemoveFromGroup(ctx, currentGroup)
				return 0
			}
			name := strings.TrimSpace(a.pos[1])
			// 优先看是不是当前组内的仓库
			var matchedRepo *state.Repo
			for _, r := range ctx.state.Repos {
				if strings.EqualFold(r.Group, currentGroup) && (strings.EqualFold(r.Name, name) || strings.EqualFold(r.ID, name)) {
					matchedRepo = r
					break
				}
			}
			if matchedRepo != nil {
				matchedRepo.Group = ""
				matchedRepo.ReviewedAt = time.Now().Format(time.RFC3339)
				state.Canonicalize(matchedRepo)
				_ = ctx.state.Save()
				refreshCurrentScope(ctx)
				fmt.Printf("已将 %s 从分组 %s 移出。\n", matchedRepo.Name, currentGroup)
				return 0
			}
			// 如果该名称是一个全局分组名：删除整个分组
			if ctx.state.HasGroup(name) {
				ctx.state.RemoveGroup(name)
				_ = ctx.state.Save()
				refreshCurrentScope(ctx)
				fmt.Printf("已删除分组: %s\n", name)
				return 0
			}
			fmt.Printf("未在当前分组或系统分组中找到: %s\n", name)
			return 2
		}

		if len(a.pos) < 2 {
			if target := ctx.res.TargetRepo(); target != nil {
				target.Group = ""
				target.ReviewedAt = time.Now().Format(time.RFC3339)
				state.Canonicalize(target)
				_ = ctx.state.Save()
				refreshCurrentScope(ctx)
				fmt.Printf("已清除 %s 的分组\n", target.Name)
				return 0
			}
			fmt.Println("用法: group rm <分组名称或仓库关键字>")
			return 2
		}
		grpName := strings.TrimSpace(a.pos[1])
		if !ctx.state.HasGroup(grpName) {
			if r := pickTarget(ctx, "group rm", grpName); r != nil {
				r.Group = ""
				r.ReviewedAt = time.Now().Format(time.RFC3339)
				state.Canonicalize(r)
				_ = ctx.state.Save()
				refreshCurrentScope(ctx)
				fmt.Printf("已清除 %s 的分组\n", r.Name)
				return 0
			}
			fmt.Printf("未找到分组或仓库: %s\n", grpName)
			return 2
		}
		ctx.state.RemoveGroup(grpName)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已删除分组: %s\n", grpName)
		return 0

	case "clear", "unset":
		if ctx.res != nil && ctx.res.NodeType == scope.NodeGroup && len(ctx.res.Scope.Segs) > 0 && len(a.pos) == 1 {
			currentGroup := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			pickRepoToRemoveFromGroup(ctx, currentGroup)
			return 0
		}
		var kw string
		target := ctx.res.TargetRepo()
		if len(a.pos) >= 2 {
			kw = a.pos[1]
		}
		var r *state.Repo
		if kw != "" {
			r = pickTarget(ctx, "group clear", kw)
			if r == nil {
				return 2
			}
		} else if target != nil {
			r = target
		} else {
			fmt.Println("用法: group clear [仓库关键字]")
			return 2
		}
		r.Group = ""
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已清除 %s 的分组\n", r.Name)
		return 0

	default:
		// 如果当前站在具体仓库上，允许直接输入 'group <组名>' 作为 set 的简写
		if target := ctx.res.TargetRepo(); target != nil && len(a.pos) == 1 {
			grpName := strings.TrimSpace(a.pos[0])
			if target.Group != "" && !strings.EqualFold(target.Group, grpName) && !a.has("yes", "y") {
				if !confirmPrompt(ctx, fmt.Sprintf("仓库 %s 当前属于 [%s]，确认变更为 [%s]? [y/N] ", target.Name, target.Group, grpName)) {
					fmt.Println("已取消。")
					return 0
				}
			}
			target.Group = grpName
			target.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(target)
			ctx.state.AddGroup(target.Group)
			if err := ctx.state.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
				return 1
			}
			refreshCurrentScope(ctx)
			fmt.Printf("已将 %s 分组设置为: %s\n", target.Name, target.Group)
			return 0
		}

		arg := strings.TrimSpace(a.pos[0])
		if ctx.state.HasGroup(arg) {
			fmt.Printf("分组: %s（输入 'cd group:%s' 可进入该分组作用域，或 'group rm %s' 可删除该分组）\n", arg, arg, arg)
			return 0
		}
		if r := pickTarget(ctx, "group", arg); r != nil {
			pickGroupForRepo(ctx, r)
			return 0
		}
		return 2
	}
}

var ansiEscapeRegex = regexp.MustCompile(`(?:\x1b|\^\[)(?:\[[0-9;?]*[ -/]*[@-~]|O[A-Za-z0-9]|.)`)

// cleanTerminalInput 清理终端输入，去除 ANSI 转义序列、模拟处理退格/DEL 并剔除不可见控制字符，
// 杜绝因终端按下方向键 (如 \x1b[A-\x1b[D)、删除键 (\x1b[3~) 或退格产生字符污染/乱码。
func cleanTerminalInput(raw string) string {
	// 1. 去除 ANSI 转义序列 (如方向键 \x1b[A-\x1b[D、删除键 \x1b[3~、^[[ 等)
	cleaned := ansiEscapeRegex.ReplaceAllString(raw, "")

	// 2. 模拟处理退格键 (\b 和 \x7f)
	var runes []rune
	for _, r := range cleaned {
		if r == '\b' || r == 127 {
			if len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
		} else if r >= 32 || r == '\t' || r == '\n' {
			runes = append(runes, r)
		}
	}
	return strings.TrimSpace(string(runes))
}

// cleanTagInput 清理标签输入，复用终端清理逻辑
func cleanTagInput(raw string) string {
	return cleanTerminalInput(raw)
}

// printAllTagsWithHints 列出全盘所有标签及使用量，附带操作引导提示。
func printAllTagsWithHints(ctx *appCtx) int {
	tags := map[string]int{}
	for _, r := range ctx.state.Repos {
		for _, t := range r.Tags {
			tags[t]++
		}
	}
	if len(tags) == 0 {
		fmt.Println("当前全集尚未创建任何标签。可用 'tag add <标签名>' 给仓库打标签。")
		return 0
	}
	fmt.Println("现有标签:")
	keys := util.SortedKeys(tags)
	for _, k := range keys {
		fmt.Printf("  %-20s %d 个仓库\n", k, tags[k])
	}
	fmt.Println("\n提示:")
	fmt.Println("  在单仓下输入 'tag' 可就地按编号复选/新建标签;")
	fmt.Println("  在全集下输入 'tag <仓库名>' 可为指定仓库编辑标签;")
	fmt.Println("  输入 'lsbytag' 可进入响应式多维标签漏斗检索。")
	return 0
}

// pickTagForRepo 为指定仓库交互式管理标签（支持复选框勾选切换、新建、清空）。
// @design docs/DESIGN-group-tag.md
func pickTagForRepo(ctx *appCtx, r *state.Repo) bool {
	all := allTagNames(ctx)
	curTags := r.Tags

	tagStr := "无"
	if len(curTags) > 0 {
		tagStr = strings.Join(curTags, ", ")
	}
	fmt.Printf("\n仓库: %s (当前标签: %s)\n", r.Name, tagStr)

	// 若系统内目前完全没有标签
	if len(all) == 0 {
		fmt.Println("\n当前全集尚未创建任何标签。")
		fmt.Println("  +) [新建标签并打上...]")
		fmt.Println("  q) 取消")
		fmt.Println()
		choice := readPrompt(ctx, "请选择 (+ / q 取消): ")
		choice = cleanTagInput(choice)
		if choice == "+" || strings.EqualFold(choice, "new") || strings.EqualFold(choice, "add") {
			names := readPrompt(ctx, "请输入新标签名称 (多个可用逗号/空格隔开): ")
			names = cleanTagInput(names)
			if names == "" {
				fmt.Println("标签名称为空，已取消。")
				return false
			}
			added := splitList(names)
			for _, t := range added {
				t = strings.TrimSpace(t)
				if t != "" && !hasTag(r, t) {
					r.Tags = append(r.Tags, t)
				}
			}
			r.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(r)
			if err := ctx.state.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
				return false
			}
			refreshCurrentScope(ctx)
			fmt.Printf("已为 %s 添加标签: %s\n", r.Name, strings.Join(r.Tags, ", "))
			return true
		}
		fmt.Println("已取消。")
		return false
	}

	// 系统内已有标签：复选勾选模式
	for {
		curMap := make(map[string]bool)
		for _, t := range r.Tags {
			curMap[strings.ToLower(t)] = true
		}

		fmt.Println("\n现有标签 (输入编号切换勾选，支持多个编号用空格隔开):")
		for i, t := range all {
			mark := "[ ]"
			if curMap[strings.ToLower(t)] {
				mark = "[√]"
			}
			fmt.Printf("  %2d) %s %s\n", i+1, mark, t)
		}
		fmt.Println("   +) [新建标签...]")
		if len(r.Tags) > 0 {
			fmt.Println("   0) [清空此仓库所有标签]")
		}
		fmt.Println()

		choice, ok := readPromptLine(ctx, "请选择 (编号 / + / 0 / q 完成退出): ")
		if !ok {
			return true
		}
		choice = cleanTagInput(choice)
		if choice == "" || strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") || strings.EqualFold(choice, "exit") {
			fmt.Println("完成标签设置。")
			return true
		}

		if choice == "0" || strings.EqualFold(choice, "clear") || strings.EqualFold(choice, "unset") {
			r.Tags = nil
			r.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(r)
			_ = ctx.state.Save()
			refreshCurrentScope(ctx)
			fmt.Printf("已清空 %s 的所有标签。\n", r.Name)
			return true
		}

		if choice == "+" || strings.EqualFold(choice, "new") || strings.EqualFold(choice, "add") {
			names := readPrompt(ctx, "请输入新标签名称 (多个可用逗号/空格隔开): ")
			names = cleanTagInput(names)
			if names != "" {
				added := splitList(names)
				for _, t := range added {
					t = strings.TrimSpace(t)
					if t != "" && !hasTag(r, t) {
						r.Tags = append(r.Tags, t)
					}
				}
				r.ReviewedAt = time.Now().Format(time.RFC3339)
				state.Canonicalize(r)
				_ = ctx.state.Save()
				refreshCurrentScope(ctx)
				fmt.Printf("已更新 %s 标签: %s\n", r.Name, strings.Join(r.Tags, ", "))
				all = allTagNames(ctx)
			}
			continue
		}

		tokens := strings.Fields(choice)
		changed := false
		for _, tok := range tokens {
			idx, err := strconv.Atoi(tok)
			if err == nil && idx >= 1 && idx <= len(all) {
				tagToToggle := all[idx-1]
				if hasTag(r, tagToToggle) {
					r.Tags = removeTag(r, tagToToggle)
				} else {
					r.Tags = append(r.Tags, tagToToggle)
				}
				changed = true
			} else {
				found := false
				for _, t := range all {
					if strings.EqualFold(t, tok) {
						if hasTag(r, t) {
							r.Tags = removeTag(r, t)
						} else {
							r.Tags = append(r.Tags, t)
						}
						changed = true
						found = true
						break
					}
				}
				if !found {
					fmt.Printf("忽略无效选项: %s\n", tok)
				}
			}
		}

		if changed {
			r.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(r)
			_ = ctx.state.Save()
			refreshCurrentScope(ctx)
			fmt.Printf("已更新 %s 标签: %s\n", r.Name, strings.Join(r.Tags, ", "))
		}
	}
}

func pickRepoToRemoveFromTag(ctx *appCtx, tagName string) bool {
	var repos []*state.Repo
	for _, r := range ctx.state.Repos {
		if hasTag(r, tagName) {
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		fmt.Printf("标签 %s 下当前没有关联仓库。\n", tagName)
		return false
	}
	fmt.Printf("\n拥有标签 [%s] 的仓库 (共 %d 个):\n", tagName, len(repos))
	for i, r := range repos {
		fmt.Printf("  %2d) %s\n", i+1, r.Name)
	}
	fmt.Println()
	choice := readPrompt(ctx, "请选择要移除此标签的仓库 (编号 / q 取消): ")
	choice = cleanTagInput(choice)
	if choice == "" || strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") {
		fmt.Println("已取消。")
		return false
	}
	var target *state.Repo
	idx, err := strconv.Atoi(choice)
	if err == nil && idx >= 1 && idx <= len(repos) {
		target = repos[idx-1]
	} else {
		for _, r := range repos {
			if strings.EqualFold(r.Name, choice) || strings.EqualFold(r.ID, choice) {
				target = r
				break
			}
		}
	}
	if target == nil {
		fmt.Printf("无效的选项: %s\n", choice)
		return false
	}
	target.Tags = removeTag(target, tagName)
	target.ReviewedAt = time.Now().Format(time.RFC3339)
	state.Canonicalize(target)
	_ = ctx.state.Save()
	refreshCurrentScope(ctx)
	fmt.Printf("已将 %s 的标签 [%s] 移除。\n", target.Name, tagName)
	return true
}

func pickTagActionInTagScope(ctx *appCtx, tagName string) int {
	var count int
	for _, r := range ctx.state.Repos {
		if hasTag(r, tagName) {
			count++
		}
	}
	fmt.Printf("\n当前标签: %s (共 %d 个仓库)\n", tagName, count)
	fmt.Println("  1) 移除关联仓库 (rm / clear)")
	fmt.Println("  2) 添加关联仓库 (add)")
	fmt.Println("  3) 查看所有标签 (list)")
	fmt.Println()
	choice := readPrompt(ctx, "请选择操作 (编号 / q 取消): ")
	choice = cleanTagInput(choice)
	switch choice {
	case "1", "rm", "clear":
		pickRepoToRemoveFromTag(ctx, tagName)
		return 0
	case "2", "add":
		kw := readPrompt(ctx, "请输入要添加此标签的仓库名称/关键字: ")
		kw = cleanTagInput(kw)
		if kw == "" {
			fmt.Println("已取消。")
			return 0
		}
		r := pickTargetGlobal(ctx, "tag add", kw)
		if r == nil {
			return 2
		}
		if hasTag(r, tagName) {
			fmt.Printf("仓库 %s 已拥有标签 [%s]。\n", r.Name, tagName)
			return 0
		}
		r.Tags = append(r.Tags, tagName)
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		_ = ctx.state.Save()
		refreshCurrentScope(ctx)
		fmt.Printf("已为 %s 添加标签: %s\n", r.Name, tagName)
		return 0
	case "3", "list", "ls":
		return printAllTagsWithHints(ctx)
	default:
		return 0
	}
}

func cmdTag(ctx *appCtx, a args) int {
	sub := ""
	if len(a.pos) > 0 {
		sub = a.pos[0]
	}
	switch sub {
	case "", "list", "ls":
		if sub == "" {
			// Priority 1: 单仓聚焦上下文
			if target := ctx.res.TargetRepo(); target != nil {
				pickTagForRepo(ctx, target)
				return 0
			}
			// Priority 2: 标签作用域上下文
			if ctx.res != nil && ctx.res.NodeType == scope.NodeTag && len(ctx.res.Scope.Segs) > 0 {
				tagName := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
				return pickTagActionInTagScope(ctx, tagName)
			}
		}
		return printAllTagsWithHints(ctx)

	case "add", "set":
		var kw, tagStr string
		target := ctx.res.TargetRepo()
		if len(a.pos) == 2 {
			if target != nil {
				tagStr = a.pos[1]
			} else {
				fmt.Println("用法: tag add <仓库关键字> <标签...>")
				return 2
			}
		} else if len(a.pos) >= 3 {
			kw = a.pos[1]
			tagStr = a.pos[2]
		} else {
			if target != nil {
				pickTagForRepo(ctx, target)
				return 0
			}
			fmt.Println("用法: tag add [仓库] <标签,标签...>")
			return 2
		}
		var r *state.Repo
		if kw != "" {
			r = pickTarget(ctx, "tag add", kw)
			if r == nil {
				return 2
			}
		} else {
			r = target
		}
		tagStr = cleanTagInput(tagStr)
		newTags := splitList(tagStr)
		for _, t := range newTags {
			t = strings.TrimSpace(t)
			if t != "" && !hasTag(r, t) {
				r.Tags = append(r.Tags, t)
			}
		}
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已更新 %s 标签: %s\n", r.Name, strings.Join(r.Tags, ", "))
		return 0

	case "rm", "del", "remove":
		if ctx.res != nil && ctx.res.NodeType == scope.NodeTag && len(ctx.res.Scope.Segs) > 0 && len(a.pos) == 1 {
			tagName := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			pickRepoToRemoveFromTag(ctx, tagName)
			return 0
		}
		var kw, tagStr string
		target := ctx.res.TargetRepo()
		if len(a.pos) == 2 {
			if target != nil {
				tagStr = a.pos[1]
			} else {
				fmt.Println("用法: tag rm <仓库关键字> <标签...>")
				return 2
			}
		} else if len(a.pos) >= 3 {
			kw = a.pos[1]
			tagStr = a.pos[2]
		} else {
			fmt.Println("用法: tag rm [仓库] <标签,标签...>")
			return 2
		}
		var r *state.Repo
		if kw != "" {
			r = pickTarget(ctx, "tag rm", kw)
			if r == nil {
				return 2
			}
		} else {
			r = target
		}
		tagStr = cleanTagInput(tagStr)
		delTags := splitList(tagStr)
		for _, t := range delTags {
			t = strings.TrimSpace(t)
			if t != "" {
				r.Tags = removeTag(r, t)
			}
		}
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		refreshCurrentScope(ctx)
		fmt.Printf("已更新 %s 标签: %s\n", r.Name, strings.Join(r.Tags, ", "))
		return 0

	case "clear", "unset":
		target := ctx.res.TargetRepo()
		var r *state.Repo
		if len(a.pos) >= 2 {
			r = pickTarget(ctx, "tag clear", a.pos[1])
			if r == nil {
				return 2
			}
		} else if target != nil {
			r = target
		} else if ctx.res != nil && ctx.res.NodeType == scope.NodeTag && len(ctx.res.Scope.Segs) > 0 {
			tagName := ctx.res.Scope.Segs[len(ctx.res.Scope.Segs)-1].Ident
			pickRepoToRemoveFromTag(ctx, tagName)
			return 0
		} else {
			fmt.Println("用法: tag clear [仓库关键字]")
			return 2
		}
		r.Tags = nil
		r.ReviewedAt = time.Now().Format(time.RFC3339)
		state.Canonicalize(r)
		_ = ctx.state.Save()
		refreshCurrentScope(ctx)
		fmt.Printf("已清空 %s 的所有标签。\n", r.Name)
		return 0

	case "filter", "find":
		subArgs := a
		if len(subArgs.pos) > 0 {
			subArgs.pos = subArgs.pos[1:]
		}
		return cmdLsByTag(ctx, subArgs)

	default:
		// Priority 1: 单仓聚焦下，敲 tag <标签...> 直接追加
		if target := ctx.res.TargetRepo(); target != nil && len(a.pos) >= 1 {
			for _, tok := range a.pos {
				tok = cleanTagInput(tok)
				for _, t := range splitList(tok) {
					t = strings.TrimSpace(t)
					if t != "" && !hasTag(target, t) {
						target.Tags = append(target.Tags, t)
					}
				}
			}
			target.ReviewedAt = time.Now().Format(time.RFC3339)
			state.Canonicalize(target)
			_ = ctx.state.Save()
			refreshCurrentScope(ctx)
			fmt.Printf("已更新 %s 标签: %s\n", target.Name, strings.Join(target.Tags, ", "))
			return 0
		}
		// Priority 4/5: 全局/目录视角下，敲 tag <仓库名>
		arg := cleanTagInput(a.pos[0])
		if r := pickTarget(ctx, "tag", arg); r != nil {
			pickTagForRepo(ctx, r)
			return 0
		}
		return 2
	}
}

// TagFacet 伴生标签及当前出现频次。
type TagFacet struct {
	Name  string
	Count int
}

// aggregateByTags 计算当前包含所有 activeTags 的仓库集，并聚合剩余伴生标签及其出现频次。
func aggregateByTags(pool []*state.Repo, activeTags []string) ([]*state.Repo, []TagFacet) {
	var matched []*state.Repo
	for _, r := range pool {
		hasAll := true
		for _, at := range activeTags {
			if !hasTag(r, at) {
				hasAll = false
				break
			}
		}
		if hasAll {
			matched = append(matched, r)
		}
	}

	activeMap := make(map[string]bool)
	for _, at := range activeTags {
		activeMap[strings.ToLower(at)] = true
	}

	freq := make(map[string]int)
	for _, r := range matched {
		for _, t := range r.Tags {
			tl := strings.TrimSpace(t)
			if tl == "" || activeMap[strings.ToLower(tl)] {
				continue
			}
			freq[tl]++
		}
	}

	var facets []TagFacet
	for name, count := range freq {
		facets = append(facets, TagFacet{Name: name, Count: count})
	}
	sort.Slice(facets, func(i, j int) bool {
		if facets[i].Count != facets[j].Count {
			return facets[i].Count > facets[j].Count
		}
		return facets[i].Name < facets[j].Name
	})

	return matched, facets
}

func enterRepo(ctx *appCtx, targetRepo *state.Repo) int {
	fmt.Printf("\n-> 正在进入仓库: %s (%s)\n", targetRepo.Name, targetRepo.Path)
	sc, err := scope.Parse("file:" + targetRepo.Path)
	if err != nil {
		fmt.Printf("解析仓库作用域失败: %v\n", err)
		return 1
	}
	if ctx.repl {
		return setScope(ctx, sc)
	}
	res, err := scope.Resolve(sc, ctx.state)
	if err != nil {
		return 1
	}
	printScopeHead(res)
	return 0
}

// cmdLsByTag 响应式多维标签漏斗检索与就地导航。
// @design docs/ANALYSIS-tag-workflow.md#3-lsbytag-响应式多维漏斗交互模型
func cmdLsByTag(ctx *appCtx, a args) int {
	pool := scopeRepos(ctx)
	if pool == nil {
		pool = scope.Visible(ctx.state)
	}
	if len(pool) == 0 {
		fmt.Println("当前作用域或全集下没有可见仓库。")
		return 0
	}

	var activeTags []string
	for _, p := range a.pos {
		p = strings.TrimSpace(p)
		if p != "" {
			activeTags = append(activeTags, p)
		}
	}

	for {
		matched, facets := aggregateByTags(pool, activeTags)

		fmt.Println()
		fmt.Println("==================== 响应式标签漏斗检索 (lsByTag) ====================")
		if len(activeTags) == 0 {
			fmt.Printf("[已选标签]: (暂无，显示当前全部 %d 个仓库)\n", len(matched))
		} else {
			tagListStr := strings.Join(activeTags, "] + [")
			fmt.Printf("[已选标签]: [%s]  (当前空间收敛至 %d 个仓库)\n", tagListStr, len(matched))
		}

		if len(facets) > 0 {
			fmt.Println("\n[伴生推荐标签]:")
			displayCount := len(facets)
			if displayCount > 15 {
				displayCount = 15
			}
			col := 0
			line := "  "
			for i := 0; i < displayCount; i++ {
				item := fmt.Sprintf("%2d) %s (%d)", i+1, facets[i].Name, facets[i].Count)
				line += util.PadRightDisplay(item, 24)
				col++
				if col >= 3 {
					fmt.Println(line)
					line = "  "
					col = 0
				}
			}
			if col > 0 {
				fmt.Println(line)
			}
			if len(facets) > displayCount {
				fmt.Printf("      ... 还有 %d 个标签未列出\n", len(facets)-displayCount)
			}
		} else if len(activeTags) > 0 {
			fmt.Println("\n[伴生推荐标签]: (已无更多伴生标签)")
		}

		fmt.Printf("\n[命中仓库清单] (共 %d 个):\n", len(matched))
		if len(matched) == 0 {
			fmt.Println("  (无匹配仓库，请尝试移除或修改已选标签)")
		} else {
			maxShow := 10
			if len(matched) <= 15 {
				maxShow = len(matched)
			}
			for i := 0; i < maxShow; i++ {
				r := matched[i]
				tagStr := ""
				if len(r.Tags) > 0 {
					tagStr = fmt.Sprintf(" [%s]", strings.Join(r.Tags, ", "))
				}
				grpStr := ""
				if r.Group != "" {
					grpStr = fmt.Sprintf(" (%s)", r.Group)
				}
				fmt.Printf("  #%-2d %s%s%s\n", i+1, util.PadRightDisplay(r.Name, 20), grpStr, tagStr)
			}
			if len(matched) > maxShow {
				fmt.Printf("      ... 还有 %d 个仓库未展开\n", len(matched)-maxShow)
			}
		}

		fmt.Println("\n操作提示:")
		fmt.Println("  输入数字编号或标签名: 追加标签收缩范围")
		fmt.Println("  输入 -<标签名> 或 pop: 移除标签回弹范围 | clear: 清空所有标签")
		if len(matched) > 0 {
			fmt.Println("  输入 #<编号> (如 #1): 直达进入该仓库作用域")
		}
		fmt.Println("  输入 q 或 quit: 退出检索")
		fmt.Println()

		input, ok := readPromptLine(ctx, "lsByTag> ")
		if !ok {
			fmt.Println("已退出标签检索。")
			return 0
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "q") || strings.EqualFold(input, "quit") || strings.EqualFold(input, "exit") {
			fmt.Println("已退出标签检索。")
			return 0
		}
		if strings.EqualFold(input, "clear") || strings.EqualFold(input, "reset") {
			activeTags = nil
			continue
		}
		if strings.EqualFold(input, "pop") || strings.EqualFold(input, "back") {
			if len(activeTags) > 0 {
				activeTags = activeTags[:len(activeTags)-1]
			} else {
				fmt.Println("当前尚未选择任何标签。")
			}
			continue
		}
		if strings.HasPrefix(input, "-") {
			toRm := strings.TrimSpace(strings.TrimPrefix(input, "-"))
			found := false
			var remaining []string
			for _, at := range activeTags {
				if strings.EqualFold(at, toRm) {
					found = true
				} else {
					remaining = append(remaining, at)
				}
			}
			if found {
				activeTags = remaining
			} else {
				fmt.Printf("已选标签中未包含: %s\n", toRm)
			}
			continue
		}

		// 检查是否输入 #<数字> 直达仓库
		if strings.HasPrefix(input, "#") {
			idxStr := strings.TrimSpace(strings.TrimPrefix(input, "#"))
			idx, err := strconv.Atoi(idxStr)
			if err == nil && idx >= 1 && idx <= len(matched) {
				targetRepo := matched[idx-1]
				return enterRepo(ctx, targetRepo)
			}
			fmt.Printf("无效的仓库编号: %s\n", input)
			continue
		}

		// 检查是否纯数字:
		// 优先作为伴生标签编号;
		// 若当前已无伴生标签 (facets 为空) 或数字大于伴生标签且落在 matched 范围内，作为仓库编号
		num, err := strconv.Atoi(input)
		if err == nil {
			if num >= 1 && num <= len(facets) {
				activeTags = append(activeTags, facets[num-1].Name)
				continue
			}
			if (len(facets) == 0 || num > len(facets)) && num >= 1 && num <= len(matched) {
				targetRepo := matched[num-1]
				return enterRepo(ctx, targetRepo)
			}
			fmt.Printf("无效的编号: %d\n", num)
			continue
		}

		// 检查是否匹配仓库名
		var hitRepo *state.Repo
		for _, r := range matched {
			if strings.EqualFold(r.Name, input) {
				hitRepo = r
				break
			}
		}
		if hitRepo != nil && len(matched) <= 5 {
			return enterRepo(ctx, hitRepo)
		}

		// 否则作为标签名追加
		already := false
		for _, at := range activeTags {
			if strings.EqualFold(at, input) {
				already = true
				break
			}
		}
		if already {
			fmt.Printf("标签 [%s] 已在过滤条件中。\n", input)
			continue
		}
		activeTags = append(activeTags, input)
	}
}

func cmdStatus(ctx *appCtx, a args) int {
	target := ctx.res.TargetRepo()
	if len(a.pos) == 0 {
		if target != nil {
			st := target.Status
			if st == "" {
				st = "new"
			}
			fmt.Printf("%s 当前状态: %s\n", target.Name, st)
			return 0
		}
		fmt.Println("用法: status [仓库] <new|active|done|archived>")
		return 2
	}
	var kw, stVal string
	if len(a.pos) == 1 {
		if target != nil {
			stVal = a.pos[0]
		} else {
			fmt.Println("用法: status <仓库关键字> <状态值>")
			return 2
		}
	} else {
		kw = a.pos[0]
		stVal = a.pos[1]
	}
	norm := normalizeStatus(stVal)
	if norm == "" {
		fmt.Fprintf(os.Stderr, "无法识别的状态 %q，可选: new/active/done/archived\n", stVal)
		return 2
	}
	var r *state.Repo
	if kw != "" {
		r = pickTarget(ctx, "status", kw)
		if r == nil {
			return 2
		}
	} else {
		r = target
	}
	r.Status = norm
	r.ReviewedAt = time.Now().Format(time.RFC3339)
	state.Canonicalize(r)
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	fmt.Printf("已将 %s 状态设置为: %s\n", r.Name, r.Status)
	return 0
}

func cmdNote(ctx *appCtx, a args) int {
	target := ctx.res.TargetRepo()
	if len(a.pos) == 0 {
		if target != nil {
			if target.Note == "" {
				fmt.Printf("%s 暂无备注\n", target.Name)
			} else {
				fmt.Printf("%s 备注: %s\n", target.Name, target.Note)
			}
			return 0
		}
		fmt.Println("用法: note [仓库] <备注内容>")
		return 2
	}
	var r *state.Repo
	var noteText string
	if target != nil && len(a.pos) >= 1 {
		r = target
		noteText = strings.Join(a.pos, " ")
	} else if len(a.pos) >= 2 {
		r = pickTarget(ctx, "note", a.pos[0])
		if r == nil {
			return 2
		}
		noteText = strings.Join(a.pos[1:], " ")
	} else {
		fmt.Println("用法: note <仓库关键字> <备注内容>")
		return 2
	}
	r.Note = noteText
	r.ReviewedAt = time.Now().Format(time.RFC3339)
	state.Canonicalize(r)
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	fmt.Printf("已为 %s 保存备注: %s\n", r.Name, r.Note)
	return 0
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func removeTag(r *state.Repo, tag string) []string {
	out := []string{}
	for _, t := range r.Tags {
		if strings.EqualFold(t, tag) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func readPromptLine(ctx *appCtx, msg string) (string, bool) {
	if ctx != nil && ctx.rl != nil {
		oldPrompt := prompt(ctx)
		defer ctx.rl.SetPrompt(oldPrompt)
		ctx.rl.SetPrompt(msg)
		line, err := ctx.rl.Readline()
		if err != nil {
			return "", false
		}
		return cleanTerminalInput(line), true
	}
	if ctx != nil && ctx.pipeScanner != nil {
		fmt.Print(msg)
		if !ctx.pipeScanner.Scan() {
			return "", false
		}
		return cleanTerminalInput(ctx.pipeScanner.Text()), true
	}
	fmt.Print(msg)
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", false
	}
	return cleanTerminalInput(line), true
}

func readPrompt(ctx *appCtx, msg string) string {
	line, _ := readPromptLine(ctx, msg)
	return line
}

func confirmPrompt(ctx *appCtx, msg string) bool {
	line := strings.ToLower(readPrompt(ctx, msg))
	return line == "y" || line == "yes"
}

func confirm() bool {
	return confirmPrompt(nil, "")
}

// ---------------------------------------------------------------- 其它命令

func cmdDupes(ctx *appCtx, a args) int {
	if target := ctx.res.TargetRepo(); target != nil && (ctx.res.NodeType == scope.NodeRepo || len(scopeRepos(ctx)) == 1) {
		key := target.PrimaryURL
		if key == "" {
			fmt.Printf("当前仓库 %s 尚未配置远端地址，无法按远端查重。\n", target.Name)
			return 0
		}
		var matches []*state.Repo
		for _, r := range ctx.state.Repos {
			if r.PrimaryURL == key && r.ID != target.ID {
				matches = append(matches, r)
			}
		}
		if len(matches) == 0 {
			fmt.Printf("全盘未发现与当前仓库远端 (%s) 相同的重复克隆。\n", key)
			return 0
		}
		fmt.Printf("发现当前仓库在全盘存在 %d 个相同远端的副本 (%s):\n", len(matches), key)
		for _, r := range matches {
			fmt.Printf("  %-10s %s\n", render.CommitAge(r), r.Path)
		}
		return 0
	}

	buckets := map[string][]*state.Repo{}
	for _, r := range ctx.state.Repos {
		if r.Ignored && !a.has("all") {
			continue
		}
		if ctx.state.IsHidden(r.Path) {
			continue
		}
		key := r.PrimaryURL
		if key == "" {
			key = "local:" + strings.ToLower(r.Name)
		}
		buckets[key] = append(buckets[key], r)
	}
	type row struct {
		key   string
		repos []*state.Repo
	}
	rows := []row{}
	for k, v := range buckets {
		if len(v) > 1 {
			rows = append(rows, row{k, v})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return len(rows[i].repos) > len(rows[j].repos) })

	if len(rows) == 0 {
		fmt.Println("没有发现重复克隆。")
		return 0
	}
	for _, rw := range rows {
		fmt.Printf("× %d  %s\n", len(rw.repos), rw.key)
		ctx.state.SortBy(state.SortCommit, true)
		for _, r := range rw.repos {
			fmt.Printf("    %-10s %s\n", render.CommitAge(r), r.Path)
		}
		fmt.Println()
	}
	fmt.Printf("共 %d 组重复。同一仓库克隆多份，多半可以只留一份。\n", len(rows))
	return 0
}

func cmdStats(ctx *appCtx, a args) int {
	st := ctx.state
	pool := scopeRepos(ctx)
	if pool == nil {
		pool = st.Repos
	}
	if ctx.res != nil {
		printScopeHead(ctx.res)
		fmt.Println()
	}
	if target := ctx.res.TargetRepo(); target != nil && (ctx.res.NodeType == scope.NodeRepo || len(pool) == 1) {
		printSingleRepoStats(target)
		return 0
	}
	byStatus := map[string]int{}
	byGroup := map[string]int{}
	byKind := map[string]int{}
	dirty, noRemote, ahead, sizeSum := 0, 0, 0, int64(0)
	sized := 0
	for _, r := range pool {
		if st.IsHidden(r.Path) {
			continue
		}
		byStatus[canonicalStatus(r)]++
		byGroup[r.Group]++
		byKind[r.Kind]++
		if r.Dirty {
			dirty++
		}
		if r.PrimaryURL == "" {
			noRemote++
		}
		if r.Ahead > 0 {
			ahead++
		}
		if r.SizeBytes > 0 {
			sizeSum += r.SizeBytes
			sized++
		}
	}
	fmt.Println("仓库概览")
	fmt.Printf("  清单总数     %d\n", len(pool))
	fmt.Printf("  待处理       %d\n", pendingCount(st, scopeRepos(ctx)))
	fmt.Println("  状态分布:")
	for _, s := range state.StatusList {
		fmt.Printf("    %-8s %d\n", s, byStatus[s])
	}
	fmt.Printf("    ignored  %d\n", byStatus["ignored"])
	if len(byGroup) > 0 {
		fmt.Println("  分组:")
		keys := util.SortedKeys(byGroup)
		for _, k := range keys {
			name := k
			if name == "" {
				name = "(未分组)"
			}
			fmt.Printf("    %-16s %d\n", name, byGroup[k])
		}
	}
	fmt.Println("  类型:")
	for _, k := range util.SortedKeys(byKind) {
		kn := k
		if kn == "" {
			kn = "unknown"
		}
		fmt.Printf("    %-12s %d\n", kn, byKind[k])
	}
	fmt.Printf("  有未提交改动 %d\n", dirty)
	fmt.Printf("  有未推送提交 %d\n", ahead)
	fmt.Printf("  无远端配置   %d\n", noRemote)
	if sized > 0 {
		fmt.Printf("  已统计体积   %s（%d 个仓库，scan --size 可补全）\n", util.HumanSize(sizeSum), sized)
	}
	if len(st.Scans) > 0 {
		last := st.Scans[len(st.Scans)-1]
		fmt.Printf("  上次扫描     %s，发现 %d，新增 %d\n", last.At, last.Found, last.New)
	}
	return 0
}

func printSingleRepoStats(r *state.Repo) {
	fmt.Printf("仓库画像: %s\n", r.Name)
	fmt.Printf("  绝对路径:    %s\n", r.Path)
	branch := r.Branch
	if r.Detached {
		branch = "(detached)"
	}
	fmt.Printf("  当前分支:    %s\n", branch)
	if r.Ahead > 0 || r.Behind > 0 {
		fmt.Printf("  远端同步:    领先 %d, 落后 %d\n", r.Ahead, r.Behind)
	} else if r.HasUpstream {
		fmt.Printf("  远端同步:    与上游同步\n")
	} else {
		fmt.Printf("  远端同步:    未配置上游\n")
	}
	if r.Dirty {
		fmt.Printf("  工作区:      脏 (暂存:%d, 未暂存:%d, 未跟踪:%d)\n", r.Staged, r.Unstaged, r.Untracked)
	} else {
		fmt.Printf("  工作区:      干净\n")
	}
	if r.PrimaryURL != "" {
		fmt.Printf("  远端地址:    %s\n", r.PrimaryURL)
	}
	if r.Commit != "" {
		cShort := r.Commit
		if len(cShort) > 7 {
			cShort = cShort[:7]
		}
		fmt.Printf("  最近提交:    %s · %s (%s)\n", cShort, r.Subject, r.CommitAt)
	}
	if r.SizeBytes > 0 {
		fmt.Printf("  磁盘体积:    %s\n", util.HumanSize(r.SizeBytes))
	}
	grp := r.Group
	if grp == "" {
		grp = "(未分组)"
	}
	fmt.Printf("  所属分组:    %s\n", grp)
	if len(r.Tags) > 0 {
		fmt.Printf("  标签:        %s\n", strings.Join(r.Tags, ", "))
	}
	st := r.Status
	if st == "" {
		st = "new (待处理)"
	}
	fmt.Printf("  处理状态:    %s\n", st)
	if r.Note != "" {
		fmt.Printf("  备注:        %s\n", r.Note)
	}
}

func cmdOpen(ctx *appCtx, a args) int {
	kw := ""
	if len(a.pos) > 0 {
		kw = a.pos[0]
	}
	var r *state.Repo
	if kw == "" && ctx.res != nil && ctx.res.TargetRepo() != nil {
		r = ctx.res.TargetRepo()
	} else {
		r = pickTarget(ctx, "open", kw)
	}
	if r == nil {
		return 2
	}
	return openInExplorer(r.Path)
}

func openInExplorer(path string) int {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "打开失败: %v\n", err)
		return 1
	}
	fmt.Println(path)
	return 0
}

// cmdTig 把清单里的一个仓库交给 tig，开在新终端窗口里。
//
// 关键字定位跟 open / rm 一致（名字、路径片段都行）。不给关键字时，
// 如果你正站在单个仓库节点上，就默认是它 —— cd 进来之后直接敲 igit tig。
func cmdTig(ctx *appCtx, a args) int {
	// 位置参数逐个归类：视图保留字归视图，其余归仓库关键字。
	// 一个位置只许有一个含义，不靠上下文猜 —— 靠猜的话同一个词在不同作用域下含义会变。
	var view, kw string
	force := a.str("", "repo", "repo_name")
	for _, p := range a.pos {
		if p == "" {
			continue
		}
		if force == "" && tig.IsViewWord(p) {
			if view != "" {
				fmt.Fprintf(os.Stderr, "视图给了两次: %s / %s\n", view, p)
				return 2
			}
			view = p
			continue
		}
		if kw != "" {
			fmt.Fprintf(os.Stderr, "只认一个仓库关键字，多了: %s\n", p)
			return 2
		}
		kw = p
	}
	if v, ok := a.kv["view"]; ok {
		// --view 光秃秃不带值时 parseArgs 记下的是 "true"，那不是视图名，是缺值。
		if v == "true" {
			fmt.Fprintln(os.Stderr, "--view 需要一个值，可选: auto / main / status / log / refs")
			return 2
		}
		if view != "" && !strings.EqualFold(view, v) {
			fmt.Fprintf(os.Stderr, "视图给了两次: %s / %s\n", view, v)
			return 2
		}
		view = v
	}
	if force != "" {
		// --repo 是逃生口：仓库真叫 log / main 这类名字时，强制当关键字。
		if kw != "" {
			fmt.Fprintf(os.Stderr, "给了 --repo 就别再给仓库关键字: %s\n", kw)
			return 2
		}
		kw = force
	}

	// 责任链：站在哪一层，就只干那一层的事。
	var r *state.Repo
	switch {
	case ctx.res != nil && ctx.res.Kind == scope.KindRepo && len(ctx.res.Repos) == 1:
		// 已经站在具体仓库上，这一层只负责定视图，不再负责挑对象。
		if kw != "" {
			fmt.Fprintf(os.Stderr, "这里已经是具体仓库 %s，不接受仓库关键字（要看别的先 cd）\n", ctx.res.Repos[0].Path)
			return 2
		}
		r = ctx.res.Repos[0]
	default:
		// 集合 / 根：这一层负责挑对象。
		r = pickTarget(ctx, "tig", kw)
		if r == nil {
			return 2 // 原因和候选 pick 已经打过了，不再重复一遍用法
		}
	}

	v, err := tig.Decide(r, view)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}

	// --print 只把命令行打出来，不起窗口。装 tig 之前也能先看看会跑什么。
	if a.has("print", "dry_run", "dry") {
		exe := "tig"
		if e, eerr := tig.Exe(ctx.cfg); eerr == nil {
			exe = e
		}
		fmt.Println(tig.Line(tig.Args(exe, r.Path, v)))
		return 0
	}

	line, err := tig.Launch(ctx.cfg, r, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Printf("已在新窗口打开 %s\n", r.Path)
	fmt.Printf("  %s\n", line)
	fmt.Printf("  开屏位置 %s · %s\n", v.String(), v.Why)
	return 0
}

// makeOpenTig 面板里按 g 时用的动作。跟命令走同一套 Decide / Launch，
// 只是回显短一些 —— 面板底部只有一行。
func makeOpenTig(ctx *appCtx) tui.OpenTig {
	return func(r *state.Repo) (string, error) {
		v, err := tig.Decide(r, "auto")
		if err != nil {
			return "", err
		}
		if _, err := tig.Launch(ctx.cfg, r, v); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s 视图 · %s", v.String(), v.Why), nil
	}
}

func cmdRm(ctx *appCtx, a args) int {
	if len(a.pos) == 0 {
		fmt.Println("用法: igit rm <关键字>   （只从清单移除，不动磁盘）")
		return 2
	}
	hits := poolHits(ctx, a.pos[0])
	if len(hits) == 0 {
		fmt.Println("没有匹配到仓库。")
		return 1
	}
	removed := 0
	for _, h := range hits {
		if ctx.state.Remove(h.ID) {
			removed++
			fmt.Printf("  移除 %s\n", h.Path)
		}
	}
	if removed > 0 {
		if err := ctx.state.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
	}
	fmt.Printf("已移除 %d 条记录。\n", removed)
	return 0
}

func cmdPrune(ctx *appCtx, a args) int {
	dropped := ctx.state.PruneMissing()
	if len(dropped) == 0 {
		fmt.Println("没有需要清理的记录。")
		return 0
	}
	for _, r := range dropped {
		fmt.Printf("  清理 %s\n", r.Path)
	}
	if a.has("dry_run", "dry") {
		fmt.Printf("（--dry-run，未落盘）\n")
		return 0
	}
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	fmt.Printf("已清理 %d 条记录。\n", len(dropped))
	return 0
}

// ---------------------------------------------------------------- 目录级规则

// requireDirArg 解析 hide / fold 的目录参数。
//
// 相对路径以**当前作用域**为基准，不是进程 cwd：REPL 里的 cd 只改内存里的作用域，
// 从不 os.Chdir，拿 cwd 当基准会把规则写到启动目录去（跨平台、跨盘，且完全静默）。
// 作用域里没有 file 段时无从推断，才退回 cwd，并且必须把基准打出来。
func requireDirArg(ctx *appCtx, a args, verb string) (string, bool) {
	if len(a.pos) == 0 {
		fmt.Printf("用法: igit %s <目录>\n", verb)
		fmt.Println("  绝对路径，或相对当前作用域的目录名（在当前仓库/目录下可直接用 '.'）")
		return "", false
	}
	raw := strings.Trim(strings.TrimSpace(a.pos[0]), `"`)
	if raw == "." {
		if ctx != nil && ctx.res != nil {
			if target := ctx.res.TargetRepo(); target != nil {
				return filepath.Clean(target.Path), true
			}
			if base := ctx.res.Scope.BaseDir(); base != "" {
				return filepath.Clean(base), true
			}
		}
		fmt.Printf("当前作用域是集合/全集（非具体目录或仓库），'.' 无法确定目标。请指定具体目录路径。\n")
		return "", false
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), true
	}
	if ctx != nil && ctx.res != nil {
		if target := ctx.res.TargetRepo(); target != nil {
			return filepath.Join(target.Path, raw), true
		}
		if base := ctx.res.Scope.BaseDir(); base != "" {
			return filepath.Join(base, raw), true
		}
	}
	cwd, _ := os.Getwd()
	fmt.Printf("  （当前作用域没有目录基准，按进程目录 %s 解析）\n", cwd)
	abs, err := filepath.Abs(raw)
	if err != nil {
		fmt.Printf("无法解析路径: %s\n", raw)
		return "", false
	}
	return abs, true
}

func shouldStepUp(res *scope.Resolved, dir string) bool {
	if res == nil {
		return false
	}
	if target := res.TargetRepo(); target != nil {
		return util.HasPrefixPath(target.Path, dir)
	}
	if base := res.Scope.BaseDir(); base != "" {
		return util.HasPrefixPath(base, dir)
	}
	return false
}

// suggestSubdirs 列出 dir 这一层有哪些子目录，路径写错时好有个对照。
func suggestSubdirs(st *state.State, dir string) []string {
	parent := util.NormalizePath(filepath.Dir(dir))
	seen := map[string]bool{}
	var out []string
	for _, r := range st.Repos {
		rp := util.NormalizePath(r.Path)
		if !strings.HasPrefix(rp, parent+"/") {
			continue
		}
		name := strings.Split(strings.TrimPrefix(rp, parent+"/"), "/")[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// guardEmptyRule 拒绝会盖住 0 个仓库的 hide / fold：
// 静默写一条永远不生效的规则，比报错危险得多 —— 用户以为生效了，其实什么都没发生。
func guardEmptyRule(ctx *appCtx, dir, verb string) bool {
	if affectedCount(ctx.state, dir) > 0 {
		return true
	}
	fmt.Printf("%s 没生效：%s 下面一个仓库都没有。\n", verb, dir)
	fmt.Println("  多半是路径写错了，规则没有保存。")
	if names := suggestSubdirs(ctx.state, dir); len(names) > 0 {
		fmt.Printf("  这一层有: %s\n", strings.Join(names, "  "))
	}
	return false
}

// affectedCount 统计一条规则会盖住多少仓库，执行前让用户心里有数。
func affectedCount(st *state.State, dir string) int {
	n := 0
	for _, r := range st.Repos {
		if util.HasPrefixPath(r.Path, dir) {
			n++
		}
	}
	return n
}

func cmdHide(ctx *appCtx, a args, hide bool) int {
	verb := "hide"
	if !hide {
		verb = "unhide"
	}
	dir, ok := requireDirArg(ctx, a, verb)
	if !ok {
		return 2
	}
	if hide && !guardEmptyRule(ctx, dir, verb) {
		return 2
	}
	if hide && !a.has("yes", "y") {
		cnt := affectedCount(ctx.state, dir)
		if !confirmPrompt(ctx, fmt.Sprintf("确认隐藏 %s（包含 %d 个仓库）? [y/N] ", dir, cnt)) {
			fmt.Println("已取消。")
			return 0
		}
	}
	var changed bool
	if hide {
		dir, changed = ctx.state.Hide(dir, a.str("", "note"))
	} else {
		dir, changed = ctx.state.Unhide(dir)
	}
	if !changed {
		fmt.Println("没有变化（这条规则已经存在 / 本来就没有）:", dir)
		return 0
	}
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	if hide {
		fmt.Printf("已隐藏 %s\n", dir)
		fmt.Printf("  该目录下 %d 个仓库不再出现在任何结果里，扫描也会整棵跳过。\n", affectedCount(ctx.state, dir))
		if ctx.repl && ctx.res != nil && shouldStepUp(ctx.res, dir) {
			sc := ctx.res.Scope.Parent()
			for !sc.IsRoot() {
				if _, err := scope.Resolve(sc, ctx.state); err == nil {
					break
				}
				sc = sc.Parent()
			}
			setScope(ctx, sc)
		} else {
			refreshCurrentScope(ctx)
		}
	} else {
		fmt.Printf("已取消隐藏 %s\n", dir)
		fmt.Printf("  该目录下 %d 个仓库恢复显示（下次 scan 才会重新采集）。\n", affectedCount(ctx.state, dir))
		refreshCurrentScope(ctx)
	}
	return 0
}

func cmdFold(ctx *appCtx, a args, fold bool) int {
	verb := "fold"
	if !fold {
		verb = "unfold"
	}
	dir, ok := requireDirArg(ctx, a, verb)
	if !ok {
		return 2
	}
	if fold && !guardEmptyRule(ctx, dir, verb) {
		return 2
	}
	if fold && !a.has("yes", "y") {
		cnt := affectedCount(ctx.state, dir)
		if !confirmPrompt(ctx, fmt.Sprintf("确认折叠 %s（包含 %d 个仓库）? [y/N] ", dir, cnt)) {
			fmt.Println("已取消。")
			return 0
		}
	}
	var changed bool
	if fold {
		dir, changed = ctx.state.Fold(dir, a.str("", "note"))
	} else {
		dir, changed = ctx.state.Unfold(dir)
	}
	if !changed {
		fmt.Println("没有变化（这条规则已经存在 / 本来就没有）:", dir)
		return 0
	}
	if err := ctx.state.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	if fold {
		fmt.Printf("已折叠 %s\n", dir)
		fmt.Printf("  该目录下 %d 个仓库收拢成一行，只显示数量。\n", affectedCount(ctx.state, dir))
	} else {
		fmt.Printf("已展开 %s\n", dir)
	}
	refreshCurrentScope(ctx)
	return 0
}

func cmdDirs(ctx *appCtx, a args) int {
	st := ctx.state
	if len(st.Hidden) == 0 && len(st.Folded) == 0 {
		fmt.Println("还没有设置任何目录规则。")
		fmt.Println("  igit hide <目录>   整个目录不出现、也不再扫描")
		fmt.Println("  igit fold <目录>   整个目录收拢成一行，只显示数量")
		return 0
	}
	if len(st.Hidden) > 0 {
		fmt.Println("已隐藏（完全不出现，扫描跳过）:")
		for _, r := range st.Hidden {
			line := fmt.Sprintf("  %-60s %d 个仓库", r.Path, affectedCount(st, r.Path))
			if r.Note != "" {
				line += "  " + r.Note
			}
			fmt.Println(line)
		}
		fmt.Println()
	}
	if len(st.Folded) > 0 {
		fmt.Println("已折叠（收拢成一行）:")
		for _, r := range st.Folded {
			line := fmt.Sprintf("  %-60s %d 个仓库", r.Path, affectedCount(st, r.Path))
			if r.Note != "" {
				line += "  " + r.Note
			}
			fmt.Println(line)
		}
	}
	return 0
}

// exportFilterKeys 导出时透传的过滤参数。
// 默认导出全集（"导出一份清单"通常是这个意思），
// 但用户显式给了条件就该生效——否则 --dirty 之类会被静默忽略。
var exportFilterKeys = []string{
	"group", "g", "tag", "t", "status", "query", "q", "kind", "k",
	"dirty", "clean", "unpushed", "ahead", "no_remote", "noremote",
	"missing", "ignored",
}

func cmdExport(ctx *appCtx, a args) int {
	fkv := map[string]string{"all": "true"} // 以全集为基底
	for _, k := range exportFilterKeys {
		if v, ok := a.kv[k]; ok {
			fkv[k] = v
		}
	}
	// 以当前作用域为基底：cd 进去再 export，导出的就是这一批。
	pool := scopeRepos(ctx)
	if pool == nil {
		pool = ctx.state.Repos
	}
	repos := filterRepos(ctx.state, args{kv: fkv, pos: nil}, pool)
	if len(a.pos) > 0 {
		repos = nil
		seen := map[string]bool{}
		for _, q := range a.pos {
			for _, r := range ctx.state.Find(q) {
				if !seen[r.ID] && inPool(pool, r) {
					seen[r.ID] = true
					repos = append(repos, r)
				}
			}
		}
	}
	format := strings.ToLower(a.str("md", "format", "f"))
	out := os.Stdout
	if o := a.str("", "out", "o"); o != "" {
		f, err := os.Create(o)
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法写入 %s: %v\n", o, err)
			return 1
		}
		defer f.Close()
		out = f
	}
	var err error
	switch format {
	case "csv":
		err = export.CSV(out, repos)
	case "json":
		err = export.JSON(out, repos)
	case "md", "markdown":
		if a.has("flat") {
			err = export.MarkdownFlat(out, repos)
		} else {
			err = export.MarkdownTree(out, repos, ctx.state.FoldedPaths())
		}
	case "outline", "skeleton":
		err = export.MarkdownOutline(out, repos, ctx.state.FoldedPaths())
	default:
		fmt.Fprintf(os.Stderr, "不支持的格式: %s（csv/json/md/outline）\n", format)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "导出失败: %v\n", err)
		return 1
	}
	outPath := a.str("", "out", "o")
	if outPath != "" {
		fmt.Printf("已导出 %d 条 -> %s\n", len(repos), outPath)
	}
	return 0
}

// ---------------------------------------------------------------- triage

func cmdTriage(ctx *appCtx, a args) int {
	initial := a.str("", "filter", "q")
	preset := normalizeStatus(a.str("", "status"))
	save := func() error { return ctx.state.Save() }

	// 面板只在当前作用域这批仓库里工作。没限定就全集，标题栏不显示范围。
	subset := scopeRepos(ctx)
	label := ""
	if ctx.res == nil {
		fmt.Println("（当前目录不在扫描范围内，这次按全集处理）")
	} else if !ctx.res.Scope.IsRoot() {
		label = scopeLabel(ctx.res)
		fmt.Printf("作用域  %s · %d 个仓库\n", label, ctx.res.Totals.Repos)
	}
	if err := tui.Run(ctx.state, ctx.cfg.Roots, initial, preset, subset, label, save, makeOpenTig(ctx)); err != nil {
		fmt.Fprintf(os.Stderr, "面板启动失败: %v\n", err)
		return 1
	}
	fmt.Println()
	if subset != nil {
		fmt.Printf("本作用域内待处理还剩 %d 个。\n", pendingCount(ctx.state, subset))
	} else {
		fmt.Printf("待处理还剩 %d 个。\n", pendingCount(ctx.state, nil))
	}
	return 0
}

// ---------------------------------------------------------------- config

func cmdConfig(ctx *appCtx, a args) int {
	sub := "show"
	if len(a.pos) > 0 {
		sub = strings.ToLower(a.pos[0])
	}
	switch sub {
	case "show":
		return configShow(ctx)
	case "init":
		return configInit(ctx, a)
	case "discover":
		return configDiscover(ctx, a)
	case "add-root", "addroot":
		return configMutateRoot(ctx, a, true)
	case "rm-root", "rmroot":
		return configMutateRoot(ctx, a, false)
	case "add-exclude", "addexclude":
		return configMutateExclude(ctx, a, true)
	case "rm-exclude", "rmexclude":
		return configMutateExclude(ctx, a, false)
	case "set":
		return configSet(ctx, a)
	default:
		fmt.Fprintf(os.Stderr, "未知配置命令: %s\n", sub)
		return 2
	}
}

func configShow(ctx *appCtx) int {
	fmt.Printf("数据目录  %s\n", ctx.home)
	fmt.Printf("配置文件  %s\n", ctx.cfgPath)
	fmt.Printf("状态文件  %s\n", ctx.stPath)
	fmt.Printf("仓库记录  %d 条（待处理 %d）\n", len(ctx.state.Repos), pendingCount(ctx.state, nil))
	fmt.Println()
	fmt.Println("扫描根:")
	if len(ctx.cfg.Roots) == 0 {
		fmt.Println("  (空)")
	}
	for _, r := range ctx.cfg.Roots {
		fmt.Printf("  %s\n", r)
	}
	fmt.Println("跳过的目录名:")
	for _, n := range ctx.cfg.ExcludeNames {
		fmt.Printf("  %s\n", n)
	}
	if len(ctx.cfg.ExcludePaths) > 0 {
		fmt.Println("跳过的路径:")
		for _, n := range ctx.cfg.ExcludePaths {
			fmt.Printf("  %s\n", n)
		}
	}
	fmt.Println()
	fmt.Printf("并发 %d  最大深度 %d  fast=%v  deep=%v\n", ctx.cfg.Jobs, ctx.cfg.MaxDepth, ctx.cfg.Fast, ctx.cfg.Deep)
	if ctx.cfg.GitExe != "" {
		fmt.Printf("git 可执行文件: %s\n", ctx.cfg.GitExe)
	} else if gitinfo.ResolveGit("") == "" {
		fmt.Println("警告: 未在 PATH 中找到 git，只能读取 .git 元数据，拿不到未提交改动")
	}
	if ctx.cfg.TigExe != "" {
		fmt.Printf("tig 可执行文件: %s\n", ctx.cfg.TigExe)
	} else if p, err := tig.Exe(ctx.cfg); err == nil {
		fmt.Printf("tig 可执行文件: %s（PATH 中找到）\n", p)
	} else {
		fmt.Printf("tig: %v\n", err)
	}
	if ctx.cfg.TermCmd != "" {
		fmt.Printf("终端命令模板: %s\n", ctx.cfg.TermCmd)
	}
	return 0
}

func configInit(ctx *appCtx, a args) int {
	if _, err := os.Stat(ctx.cfgPath); err == nil && !a.has("force") {
		fmt.Printf("配置已存在: %s\n加 --force 覆盖。\n", ctx.cfgPath)
		return 0
	}
	c := config.Default()
	if err := config.Save(ctx.cfgPath, c); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败: %v\n", err)
		return 1
	}
	fmt.Printf("已生成默认配置: %s\n", ctx.cfgPath)
	fmt.Println("扫描根:")
	for _, r := range c.Roots {
		fmt.Printf("  %s\n", r)
	}
	return 0
}

func configMutateRoot(ctx *appCtx, a args, add bool) int {
	if len(a.pos) < 2 {
		fmt.Println("用法: igit config add-root <目录>")
		return 2
	}
	var changed bool
	var abs string
	if add {
		changed, abs = config.AddRoot(ctx.cfg, a.pos[1])
	} else {
		changed, abs = config.RemoveRoot(ctx.cfg, a.pos[1])
	}
	if !changed {
		fmt.Println("没有变化（可能已经存在 / 不存在）:", abs)
		return 0
	}
	if err := config.Save(ctx.cfgPath, ctx.cfg); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	if add {
		fmt.Printf("已添加扫描根: %s\n", abs)
	} else {
		fmt.Printf("已移除扫描根: %s\n", abs)
	}
	return 0
}

func configMutateExclude(ctx *appCtx, a args, add bool) int {
	if len(a.pos) < 2 {
		fmt.Println("用法: igit config add-exclude <目录名>")
		return 2
	}
	name := strings.ToLower(strings.TrimSpace(a.pos[1]))
	out := []string{}
	changed := false
	if add {
		dup := false
		for _, n := range ctx.cfg.ExcludeNames {
			if strings.EqualFold(n, name) {
				dup = true
			}
		}
		if !dup {
			ctx.cfg.ExcludeNames = append(ctx.cfg.ExcludeNames, name)
			changed = true
		}
	} else {
		for _, n := range ctx.cfg.ExcludeNames {
			if strings.EqualFold(n, name) {
				changed = true
				continue
			}
			out = append(out, n)
		}
		ctx.cfg.ExcludeNames = out
	}
	if changed {
		if err := config.Save(ctx.cfgPath, ctx.cfg); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
	}
	fmt.Printf("当前跳过的目录名: %s\n", strings.Join(ctx.cfg.ExcludeNames, ", "))
	return 0
}

func configSet(ctx *appCtx, a args) int {
	for _, p := range a.pos[1:] {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.ReplaceAll(kv[0], "-", "_"))
		v := kv[1]
		switch k {
		case "jobs":
			fmt.Sscanf(v, "%d", &ctx.cfg.Jobs)
		case "max_depth", "depth":
			fmt.Sscanf(v, "%d", &ctx.cfg.MaxDepth)
		case "fast":
			ctx.cfg.Fast = v == "true" || v == "1" || v == "yes"
		case "deep":
			ctx.cfg.Deep = v == "true" || v == "1" || v == "yes"
		case "git_exe", "git":
			ctx.cfg.GitExe = v
		case "tig_exe", "tig":
			ctx.cfg.TigExe = v
		case "term_cmd", "term":
			ctx.cfg.TermCmd = v
		case "root":
			config.AddRoot(ctx.cfg, v)
		default:
			fmt.Fprintf(os.Stderr, "忽略未知项: %s\n", kv[0])
		}
	}
	if err := config.Save(ctx.cfgPath, ctx.cfg); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return 1
	}
	fmt.Printf("配置已更新: %s\n", ctx.cfgPath)
	return 0
}

// configDiscover 粗扫各盘顶层，按"两级目录"聚合，推荐值得加入的项目录。
func configDiscover(ctx *appCtx, a args) int {
	depth := a.int(2, "max_depth", "depth")
	if depth < 1 || depth > 5 {
		depth = 2
	}
	driveRoots := a.multi["root"]
	if len(driveRoots) == 0 {
		driveRoots = fixedDrives()
	}
	driveRoots = normalizeRoots(driveRoots)
	if len(driveRoots) == 0 {
		fmt.Println("没有找到可用磁盘。可以 igit config discover --root=D:\\ 自己指定。")
		return 1
	}
	fmt.Fprintf(os.Stderr, "粗扫（深度 %d）: %s\n", depth, strings.Join(driveRoots, ", "))

	res, err := scan.Run(scan.Options{
		Roots:        driveRoots,
		ExcludeNames: ctx.cfg.ExcludeNames,
		ExcludePaths: ctx.cfg.ExcludePaths,
		MaxDepth:     depth + 2,
		Jobs:         jobsOf(a, ctx.cfg),
		Fast:         true,
		GitExe:       ctx.cfg.GitExe,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描失败: %v\n", err)
		return 1
	}
	if len(res.Repos) == 0 {
		fmt.Println("这个深度下没扫到仓库，试试 --max-depth=3 或直接 add-root 你记得的目录。")
		return 0
	}

	type agg struct {
		root string
		n    int
	}
	buckets := map[string]int{}
	for _, r := range res.Repos {
		key := ancestorUnder(driveRoots, r.Path, depth+1)
		buckets[key]++
	}
	rows := []agg{}
	for k, v := range buckets {
		rows = append(rows, agg{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].root < rows[j].root
	})

	limit := a.int(30, "limit")
	fmt.Printf("发现 %d 个仓库，按目录聚合如下:\n\n", len(res.Repos))
	for i, rw := range rows {
		if i >= limit {
			break
		}
		fmt.Printf("  %3d  %s\n", rw.n, rw.root)
	}
	if a.has("apply") {
		added := 0
		for i, rw := range rows {
			if i >= limit {
				break
			}
			if rw.n < a.int(2, "min") {
				continue
			}
			if ok, _ := config.AddRoot(ctx.cfg, rw.root); ok {
				added++
			}
		}
		if err := config.Save(ctx.cfgPath, ctx.cfg); err != nil {
			fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
			return 1
		}
		fmt.Printf("\n已追加 %d 个扫描根。\n", added)
	} else {
		fmt.Println("\n觉得合适的目录，用 igit config add-root <目录> 加进去；")
		fmt.Println("或者重跑本命令加 --apply --min=2 一次性把含 2 个以上仓库的目录全加上。")
	}
	return 0
}

// ancestorUnder 取 path 在某个 root 之下第 n 层的祖先目录。
func ancestorUnder(roots []string, path string, n int) string {
	base := ""
	for _, r := range roots {
		if util.HasPrefixPath(path, r) && len(r) > len(base) {
			base = r
		}
	}
	cleaned := filepath.Clean(path)
	if base == "" {
		return filepath.Dir(cleaned)
	}
	rel, err := filepath.Rel(base, cleaned)
	if err != nil {
		return filepath.Dir(cleaned)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > n {
		parts = parts[:n]
	} else if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return base
	}
	return filepath.Join(base, filepath.FromSlash(strings.Join(parts, "/")))
}

func fixedDrives() []string {
	out := []string{}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			p := string(c) + `:\`
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				out = append(out, p)
			}
		}
		return out
	}
	out = append(out, "/")
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, home)
	}
	return out
}
