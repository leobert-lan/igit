package render

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grscan/internal/state"
	"grscan/internal/util"
)

// View 控制列表输出的形态。
type View struct {
	Roots     []string
	Width     int
	Full      bool
	ShowGroup bool
	ShowPath  bool
}

func DefaultView(roots []string) View {
	return View{Roots: roots, Width: 120, ShowGroup: true, ShowPath: true}
}

// Marker 用一位字符表示处理状态，纯 ASCII，控制台不会乱码。
func Marker(r *state.Repo) string {
	switch {
	case r.Ignored:
		return "I"
	case r.Status == state.StatusActive:
		return "A"
	case r.Status == state.StatusDone:
		return "D"
	case r.Status == state.StatusArchived:
		return "Z"
	default:
		return "N"
	}
}

func StatusCN(r *state.Repo) string {
	switch {
	case r.Ignored:
		return "已忽略"
	case r.Status == state.StatusActive:
		return "跟进中"
	case r.Status == state.StatusDone:
		return "已处理"
	case r.Status == state.StatusArchived:
		return "已归档"
	default:
		return "待处理"
	}
}

func KindCN(r *state.Repo) string {
	switch r.Kind {
	case "bare":
		return "裸仓"
	case "worktree":
		return "工作树"
	case "submodule":
		return "子模块"
	default:
		return "普通"
	}
}

// ShortPath 把路径压缩成相对于扫描根的形式，长路径看着轻松些。
func ShortPath(path string, roots []string) string {
	cleaned := filepath.Clean(path)
	bestRoot, bestLen := "", 0
	for _, root := range roots {
		r := filepath.Clean(root)
		if len(r) <= bestLen {
			continue
		}
		if util.HasPrefixPath(cleaned, r) {
			bestRoot, bestLen = r, len(r)
		}
	}
	if bestRoot == "" {
		return cleaned
	}
	rel := strings.TrimPrefix(util.NormalizePath(cleaned), strings.TrimSuffix(util.NormalizePath(bestRoot), "/")+"/")
	return filepath.ToSlash(filepath.Base(bestRoot)) + "/" + rel
}

// ChangeLabel 汇总未提交改动。clean 时返回 "-"。
func ChangeLabel(r *state.Repo) string {
	total := r.Staged + r.Unstaged
	switch {
	case total == 0 && r.Untracked == 0:
		return "-"
	case r.Untracked == 0:
		return itoa(total)
	case total == 0:
		return itoa(r.Untracked) + "?"
	default:
		return itoa(total) + "+" + itoa(r.Untracked) + "?"
	}
}

// SyncLabel 领先/落后上游，没有上游就明确说出来。
func SyncLabel(r *state.Repo) string {
	if !r.HasUpstream {
		return "-"
	}
	if r.Ahead == 0 && r.Behind == 0 {
		return "同步"
	}
	s := ""
	if r.Ahead > 0 {
		s += "^" + itoa(r.Ahead)
	}
	if r.Behind > 0 {
		s += "v" + itoa(r.Behind)
	}
	return s
}

func CommitTime(r *state.Repo) time.Time {
	for _, s := range []string{r.CommitAt, r.LastSeen, r.FirstSeen} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func CommitAge(r *state.Repo) string {
	t := CommitTime(r)
	if t.IsZero() {
		return "-"
	}
	return util.Age(t)
}

func TagsLabel(r *state.Repo) string {
	if len(r.Tags) == 0 {
		return ""
	}
	cp := append([]string{}, r.Tags...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

// Columns 计算每一列的宽度，最后留给路径。
func (v View) columns() []int {
	w := v.Width
	if w < 60 {
		w = 60
	}
	name := 26
	branch := 18
	chg := 7
	sync := 8
	last := 10
	group := 12
	fixed := 2 + name + branch + chg + sync + last + group + 12 // 12 是分隔符预留
	path := w - fixed
	if path < 24 {
		path = 24
	}
	return []int{2, name, branch, chg, sync, last, group, path}
}

func (v View) Header() string {
	c := v.columns()
	return pad("", c[0]) +
		pad("NAME", c[1]) +
		pad("BRANCH", c[2]) +
		pad("CHG", c[3]) +
		pad("SYNC", c[4]) +
		pad("LAST", c[5]) +
		pad("GROUP", c[6]) +
		pad("PATH", c[7])
}

// Row 渲染一行。
func (v View) Row(r *state.Repo) string {
	c := v.columns()
	group := r.Group
	if group == "" {
		group = "-"
	}
	p := r.Path
	if r.Missing {
		p = "[已删除] " + p
	}
	if v.Full {
		// 明确要完整路径时不截断，宁可让行换行
		tail := pad(p, 1)
		head := pad(Marker(r), c[0]) +
			pad(util.Truncate(r.Name, c[1]), c[1]) +
			pad(util.Truncate(branchLabel(r), c[2]), c[2]) +
			pad(ChangeLabel(r), c[3]) +
			pad(SyncLabel(r), c[4]) +
			pad(CommitAge(r), c[5]) +
			pad(util.Truncate(group, c[6]), c[6])
		return head + tail
	}
	p = util.Truncate(ShortPath(p, v.Roots), c[7])
	return pad(Marker(r), c[0]) +
		pad(util.Truncate(r.Name, c[1]), c[1]) +
		pad(util.Truncate(branchLabel(r), c[2]), c[2]) +
		pad(ChangeLabel(r), c[3]) +
		pad(SyncLabel(r), c[4]) +
		pad(CommitAge(r), c[5]) +
		pad(util.Truncate(group, c[6]), c[6]) +
		pad(util.Truncate(p, c[7]), c[7])
}

func branchLabel(r *state.Repo) string {
	if r.Detached {
		if r.Branch != "" {
			return r.Branch + " (分离)"
		}
		return "(detached)"
	}
	b := r.Branch
	if b == "" {
		return "-"
	}
	if r.Dirty {
		return b + " *"
	}
	return b
}

// BranchLabel 导出给 tree / export 复用，避免同一份显示逻辑散落多处。
func BranchLabel(r *state.Repo) string { return branchLabel(r) }

// FoldRow 折叠行：一个被折叠的目录压成一行，只交代规模和大体状态。
// total 是该目录下清单里的仓库总数（不受当前筛选影响），
// 与 len(repos) 不同时要说出来，免得误以为"这目录就这么几个"。
func (v View) FoldRow(dir string, repos []*state.Repo, total int) string {
	c := v.columns()
	dirty, pending := 0, 0
	latest := time.Time{}
	for _, r := range repos {
		if r.Dirty {
			dirty++
		}
		if !r.Ignored && (r.Status == "" || r.Status == state.StatusNew) {
			pending++
		}
		if t := CommitTime(r); t.After(latest) {
			latest = t
		}
	}
	age := "-"
	if !latest.IsZero() {
		age = util.Age(latest)
	}
	summary := fmt.Sprintf("%s  · %d 个仓库", dir, len(repos))
	if total > len(repos) {
		summary += fmt.Sprintf("（该目录共 %d）", total)
	}
	if dirty > 0 {
		summary += fmt.Sprintf(" · %d 有改动", dirty)
	}
	if pending > 0 && pending != len(repos) {
		summary += fmt.Sprintf(" · %d 待处理", pending)
	}
	return pad("F", c[0]) +
		pad(util.Truncate("[已折叠]", c[1]), c[1]) +
		pad("", c[2]) +
		pad("", c[3]) +
		pad("", c[4]) +
		pad(age, c[5]) +
		pad("", c[6]) +
		summary
}

func pad(s string, width int) string {
	n := len([]rune(s))
	out := s
	if n < width {
		out += strings.Repeat(" ", width-n)
	}
	return out + "  "
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
