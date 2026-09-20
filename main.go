package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"grscan/internal/config"
	"grscan/internal/export"
	"grscan/internal/gitinfo"
	"grscan/internal/render"
	"grscan/internal/scan"
	"grscan/internal/state"
	"grscan/internal/tree"
	"grscan/internal/tui"
	"grscan/internal/util"
)

const version = "0.1.0"

const usageText = `grscan - 本地 git 仓库扫描与整理工具

用法:
  grscan [--home <dir>] <命令> [选项]

数据命令:
  scan                     扫描配置的根目录，并入状态文件
  list  (ls)               列出仓库（默认只看"待处理"），路径默认显示完整
  tree                     按磁盘目录树展开仓库，看空间分布
  triage (tui)             交互式面板，逐个判别/分组/归档
  set <关键字>             给匹配的仓库设置分组/状态/标签/备注/忽略
  dupes                    按远端地址找重复克隆
  stats                    统计概览
  open <关键字>            在资源管理器里打开目录
  rm <关键字>              从清单里删除记录
  prune                    清理磁盘上已不存在且未忽略的记录

目录级规则:
  hide <目录>              整个目录不再出现，扫描也整棵跳过
  unhide <目录>            取消隐藏
  fold <目录>              整个目录收拢成一行，只显示数量
  unfold <目录>            取消折叠
  dirs                     列出当前所有隐藏/折叠规则

配置命令:
  config show              查看当前配置与数据目录
  config init              重建默认配置
  config discover          探查各盘，推荐可能含仓库的目录
  config add-root <dir>    添加扫描根
  config rm-root <dir>     移除扫描根
  config add-exclude <n>   添加跳过的目录名（支持 * 通配）
  config rm-exclude <n>    移除跳过的目录名
  config set k=v           直接改配置（jobs / max-depth / fast / deep / git-exe）

导出:
  export --format=md|outline|csv|json [-o 文件]
      md       按目录分节的报告（默认）：概览 + 值得先看的几类 + 目录明细
      outline  只有目录树骨架，看清分布格局
      csv      带 BOM，Excel 直接打开
      json     原始结构化数据，喂给别的脚本或模型

通用选项:
  --home <dir>  指定数据目录（也可用环境变量 GRSCAN_HOME），默认 ~/.grscan

list / tree 共用选项:
  --all        全部
  --ignored    已忽略的
  --missing    磁盘上找不到的
  --dirty      有未提交改动的
  --clean      没有未提交改动的
  --unpushed   有未推送提交的
  --no-remote  没配远端的
  --group=X    按分组名过滤
  --tag=X      按标签过滤
  --status=X   new|active|done|archived
  --query=X    路径/名字包含 X
  --sort=path|commit|name|group|status|size   --desc
  --limit=N    最多显示 N 条
  --width=N    表格宽度（默认 120）

目录级规则示例:
  grscan hide D:\repos\ryxl_rd        这块彻底不看，扫描也不进
  grscan fold D:\repos\ryxl_rd        只保留一行"多少仓库"，不展开细节
  grscan dirs                         看看现在都设了哪些规则

list 额外选项:
  --short      压缩路径显示（默认显示完整路径）
  --json       输出 JSON
  --full       与默认一致，保留兼容

tree 额外选项:
  --brief      只看目录骨架与数量，不逐个列仓库
  --depth=N    展开到第 N 层，超出折叠成一行摘要

scan 常用选项:
  --root=<dir> 临时指定扫描根，可重复；不给则用配置里的
  --fast       不调用 git，只读 .git 目录，快很多但拿不到改动
  --deep       命中仓库后仍继续向下的 possibility（默认不下钻）
  --size       统计仓库体积（会慢不少）
  --jobs=N     并发数
  --max-depth=N  目录最大下钻深度
`

type appCtx struct {
	home    string
	cfg     *config.Config
	cfgPath string
	stPath  string
	state   *state.State
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(raw []string) int {
	raw, homeFlag := takeGlobalHome(raw)
	if len(raw) == 0 {
		fmt.Print(usageText)
		return 0
	}
	switch raw[0] {
	case "-h", "--help", "help":
		fmt.Print(usageText)
		return 0
	case "-v", "--version", "version":
		fmt.Printf("grscan %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return 0
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
	a := parseArgs(raw[1:])

	switch raw[0] {
	case "scan":
		return cmdScan(ctx, a)
	case "list", "ls":
		return cmdList(ctx, a)
	case "tree":
		return cmdTree(ctx, a)
	case "triage", "tui":
		return cmdTriage(ctx, a)
	case "set":
		return cmdSet(ctx, a)
	case "dupes":
		return cmdDupes(ctx, a)
	case "stats":
		return cmdStats(ctx, a)
	case "open":
		return cmdOpen(ctx, a)
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
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", raw[0])
		fmt.Print(usageText)
		return 2
	}
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

// ---------------------------------------------------------------- scan

func cmdScan(ctx *appCtx, a args) int {
	roots := a.multi["root"]
	if len(roots) == 0 {
		roots = ctx.cfg.Roots
	}
	roots = normalizeRoots(roots)
	if len(roots) == 0 {
		fmt.Println("还没有配置扫描根目录。")
		fmt.Println("  先跑  grscan config discover  探查一下，再  grscan config add-root <目录>")
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

	pending := pendingCount(ctx.state)
	fmt.Printf("扫描完成，用时 %s\n", roundDuration(res.Duration))
	fmt.Printf("  遍历目录   %d\n", res.Dirs)
	fmt.Printf("  扫到仓库   %d（新增 %d）\n", len(res.Repos), added)
	if res.SkippedIgnore > 0 {
		fmt.Printf("  已忽略跳过 %d\n", res.SkippedIgnore)
	}
	if missing > 0 {
		fmt.Printf("  磁盘已无   %d（运行 grscan prune 清理）\n", missing)
	}
	fmt.Printf("  清单总数   %d，其中待处理 %d\n", len(ctx.state.Repos), pending)
	fmt.Println()
	fmt.Printf("状态文件: %s\n", ctx.state.Path())
	if pending > 0 {
		fmt.Println("下一步: grscan triage   逐个判别；或 grscan list 看清单")
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

func pendingCount(st *state.State) int {
	n := 0
	for _, r := range st.Repos {
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
	selected := filterRepos(ctx.state, a)
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
	selected := filterRepos(ctx.state, a)
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

func filterRepos(st *state.State, a args) []*state.Repo {
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
	for _, r := range st.Repos {
		// 被 hide 的目录：任何视图都不出现，--all 也不破例
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
		fmt.Println("用法: grscan set <关键字> [--group=X] [--status=X] [--tag=a,b] [--untag=a,b] [--note=X] [--ignore|--unignore] [--yes]")
		fmt.Println("关键字可以是完整路径、路径片段或仓库名。")
		return 2
	}
	targets := map[string]*state.Repo{}
	for _, q := range a.pos {
		hits := ctx.state.Find(q)
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
		fmt.Printf("确认修改这 %d 个? [y/N] ", len(list))
		if !confirm() {
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

func confirm() bool {
	rd := bufio.NewReader(os.Stdin)
	line, _ := rd.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// ---------------------------------------------------------------- 其它命令

func cmdDupes(ctx *appCtx, a args) int {
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
	byStatus := map[string]int{}
	byGroup := map[string]int{}
	byKind := map[string]int{}
	dirty, noRemote, ahead, sizeSum := 0, 0, 0, int64(0)
	sized := 0
	for _, r := range st.Repos {
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
	fmt.Printf("  清单总数     %d\n", len(st.Repos))
	fmt.Printf("  待处理       %d\n", pendingCount(st))
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

func cmdOpen(ctx *appCtx, a args) int {
	if len(a.pos) == 0 {
		fmt.Println("用法: grscan open <关键字>")
		return 2
	}
	hits := ctx.state.Find(a.pos[0])
	if len(hits) == 0 {
		fmt.Println("没有匹配到仓库。")
		return 1
	}
	if len(hits) > 1 {
		fmt.Printf("匹配到 %d 个，打开第一个: %s\n", len(hits), hits[0].Path)
	}
	return openInExplorer(hits[0].Path)
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

func cmdRm(ctx *appCtx, a args) int {
	if len(a.pos) == 0 {
		fmt.Println("用法: grscan rm <关键字>   （只从清单移除，不动磁盘）")
		return 2
	}
	hits := ctx.state.Find(a.pos[0])
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

func requireDirArg(a args, verb string) (string, bool) {
	if len(a.pos) == 0 {
		fmt.Printf("用法: grscan %s <目录>\n", verb)
		fmt.Println("  目录可以是绝对路径，也可以相对当前目录")
		return "", false
	}
	abs, err := filepath.Abs(strings.Trim(strings.TrimSpace(a.pos[0]), `"`))
	if err != nil {
		fmt.Printf("无法解析路径: %s\n", a.pos[0])
		return "", false
	}
	return abs, true
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
	dir, ok := requireDirArg(a, verb)
	if !ok {
		return 2
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
	} else {
		fmt.Printf("已取消隐藏 %s\n", dir)
		fmt.Printf("  该目录下 %d 个仓库恢复显示（下次 scan 才会重新采集）。\n", affectedCount(ctx.state, dir))
	}
	return 0
}

func cmdFold(ctx *appCtx, a args, fold bool) int {
	verb := "fold"
	if !fold {
		verb = "unfold"
	}
	dir, ok := requireDirArg(a, verb)
	if !ok {
		return 2
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
	return 0
}

func cmdDirs(ctx *appCtx, a args) int {
	st := ctx.state
	if len(st.Hidden) == 0 && len(st.Folded) == 0 {
		fmt.Println("还没有设置任何目录规则。")
		fmt.Println("  grscan hide <目录>   整个目录不出现、也不再扫描")
		fmt.Println("  grscan fold <目录>   整个目录收拢成一行，只显示数量")
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
	repos := filterRepos(ctx.state, args{kv: fkv, pos: nil})
	if len(a.pos) > 0 {
		repos = nil
		seen := map[string]bool{}
		for _, q := range a.pos {
			for _, r := range ctx.state.Find(q) {
				if !seen[r.ID] {
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
	if err := tui.Run(ctx.state, ctx.cfg.Roots, initial, preset, save); err != nil {
		fmt.Fprintf(os.Stderr, "面板启动失败: %v\n", err)
		return 1
	}
	fmt.Println()
	fmt.Printf("待处理还剩 %d 个。\n", pendingCount(ctx.state))
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
	fmt.Printf("仓库记录  %d 条（待处理 %d）\n", len(ctx.state.Repos), pendingCount(ctx.state))
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
		fmt.Println("用法: grscan config add-root <目录>")
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
		fmt.Println("用法: grscan config add-exclude <目录名>")
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
		fmt.Println("没有找到可用磁盘。可以 grscan config discover --root=D:\\ 自己指定。")
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
		fmt.Println("\n觉得合适的目录，用 grscan config add-root <目录> 加进去；")
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
