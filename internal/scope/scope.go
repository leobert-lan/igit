// Package scope 实现导航式作用域：一棵虚拟树，节点由元信息推导而来，
// 序列成一个字符串；它同时决定"你在哪"和"你能敲什么"。
//
// 入串的只有路径语义；节点里"有什么"永远现算，所以仓库增删不会让 scope 过时。
package scope

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grscan/internal/state"
	"grscan/internal/util"
)

// 维度。repo 可省略：字符串里只写名字，解析时查清单发现它是仓库就切到仓库节点。
const (
	DimFile  = "file"
	DimGroup = "group"
	DimTag   = "tag"
	DimRepo  = "repo"
)

// 来源。
const (
	SourceExplicit = "explicit" // 显式 cd 进来，持久化
	SourceCWD      = "cwd"      // 当前目录推断，零状态
	SourceRoot     = "root"     // 显式回到虚拟根
)

// 节点类型。
const (
	KindRoot       = "root"
	KindCollection = "collection"
	KindRepo       = "repo"
)

// Seg 是 scope 串里的一段。
type Seg struct {
	Dim   string // file | group | tag | repo
	Ident string // 目录路径 / 分组名 / tag 名 / 仓库名

	// Explicit 表示这个维度是敲出来的（写了 `repo:`），不是光秃秃一个名字推出来的。
	// 只有它为真才是"绝对定位"：裸名字代表在当前层里往下走一步，会被叠到当前 scope 后面。
	// repo 段最需要区分 —— 不写前缀时是下钻，写了就是"我要看这一个，不管现在在哪"。
	Explicit bool
}

// Scope 是一个有序的段序列。顺序即来路，决定 .. 退到哪。
type Scope struct {
	Segs   []Seg
	Source string
}

// Item 是 ls 列出的一个子项。
type Item struct {
	Facet   string // file | group | tag | repo，供分区渲染
	Label   string // 显示名
	Target  string // 可以 cd 进去的 scope 字符串
	Count   int    // 集合类子项带的数量
	Summary string // 仓库类子项的一句话摘要
}

// Totals 是节点内的汇总数字。
type Totals struct {
	Repos      int
	Dirty      int
	Ahead      int
	NoUpstream int
	Missing    int
}

// Resolved 是每次现算出来的结果，不进字符串。
type Resolved struct {
	Scope     Scope
	Kind      string // root | collection | repo
	Repos     []*state.Repo
	Children  []Item
	Totals    Totals
	Freshness string

	// Self 是这个目录自身也是仓库时的那一个（否则 nil）。
	// 它既是"这里本身"，又是别的仓库的父目录 —— 不指定维度就没法确定看哪个视角，
	// 所以默认给你集合视角（里面有什么），自身状态单独标出来，想看它自己用 repo:。
	Self *state.Repo
}

// ---------------------------------------------------------------- 序列化

// Parse 把字符串还原成 Scope。空串与 "/" = 虚拟根。
// 分隔符 ":" 与 "#" 都接受；输出统一用 ":"。
func Parse(s string) (Scope, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "/")
	if s == "" {
		return Scope{Source: SourceRoot}, nil
	}
	var segs []Seg
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == '/' {
			i++
		}
		if i >= len(s) {
			break
		}
		dim, ident, sepLen, ok := dimPrefix(s[i:])
		if ok {
			identStart := i + len(dim) + sepLen
			i = identStart + len(ident)
			// file 段的标识是路径，Git Bash 风格的 / 属于路径而不是段分隔，
			// 只有紧跟另一个维度段时才算新段。
			if dim == DimFile {
				for i < len(s) && s[i] == '/' {
					if _, _, _, isSeg := dimPrefix(s[i+1:]); isSeg {
						break
					}
					i++
					if k := strings.IndexByte(s[i:], '/'); k >= 0 {
						i += k
					} else {
						i = len(s)
					}
				}
				ident = s[identStart:i]
			}
			ident = normalizeIdent(ident, dim == DimFile)
			if ident == "" {
				return Scope{}, fmt.Errorf("第 %d 段缺标识", len(segs)+1)
			}
			segs = append(segs, Seg{Dim: dim, Ident: ident, Explicit: true})
			continue
		}
		// 没有维度前缀 = 在父范围内匹配仓库
		end := len(s)
		if k := strings.IndexByte(s[i:], '/'); k >= 0 {
			end = i + k
		}
		name := strings.TrimSpace(s[i:end])
		if name == "" {
			return Scope{}, fmt.Errorf("第 %d 段是空的", len(segs)+1)
		}
		segs = append(segs, Seg{Dim: DimRepo, Ident: name}) // Explicit 保持 false = 相对下钻
		i = end
	}
	if len(segs) == 0 {
		return Scope{}, fmt.Errorf("没能解析出任何段: %s", s)
	}
	return Scope{Segs: segs}, nil
}

// dimPrefix 认出 "file:" / "tag#" 这类前缀，标识只取到下一个 / 之前。
func dimPrefix(s string) (dim, ident string, sepLen int, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != ':' && s[i] != '#' {
			continue
		}
		d := strings.ToLower(s[:i])
		switch d {
		case DimFile, DimGroup, DimTag, DimRepo:
			rest := s[i+1:]
			end := len(rest)
			if k := strings.IndexByte(rest, '/'); k >= 0 {
				end = k
			}
			return d, rest[:end], 1, true
		}
		return "", "", 0, false
	}
	return "", "", 0, false
}

func normalizeIdent(s string, isPath bool) string {
	s = strings.TrimSpace(s)
	if !isPath || s == "" {
		return s
	}
	return filepath.Clean(s)
}

// String 输出规范形式：统一用 ":"，repo 段不带前缀。
func (s Scope) String() string {
	if len(s.Segs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.Segs))
	for _, g := range s.Segs {
		if g.Dim == DimRepo {
			parts = append(parts, g.Ident)
			continue
		}
		parts = append(parts, g.Dim+":"+g.Ident)
	}
	return strings.Join(parts, "/")
}

func (s Scope) IsRoot() bool { return len(s.Segs) == 0 }

// Parent 先段内降一级，没级可降就去整段。
func (s Scope) Parent() Scope {
	if len(s.Segs) == 0 {
		return s
	}
	last := s.Segs[len(s.Segs)-1]
	out := make([]Seg, len(s.Segs))
	copy(out, s.Segs)
	if last.Dim == DimFile {
		// 目录往上升一级，但不越过盘符根 —— 否则 cd .. 会从 D:\work 直接跳到整个 D 盘，
		// 那是根下列举的粒度之外的一层，导航树就断了。
		if p := filepath.Dir(last.Ident); p != last.Ident && p != "." && p != "" && !isVolumeRoot(p) {
			out[len(out)-1] = Seg{Dim: DimFile, Ident: p}
			return Scope{Segs: out, Source: s.Source}
		}
	}
	return Scope{Segs: out[:len(out)-1], Source: s.Source}
}

func (s Scope) Child(seg Seg) Scope {
	out := make([]Seg, len(s.Segs), len(s.Segs)+1)
	copy(out, s.Segs)
	return Scope{Segs: append(out, seg), Source: s.Source}
}

// DescendFile 沿 file 段往下钻一层。同维度下钻在目录上就是路径变长，
// 叠一段会写成 file:A/file:A\B 这种既难看又难退的串。
func (s Scope) DescendFile(sub string) Scope {
	out := make([]Seg, len(s.Segs))
	copy(out, s.Segs)
	last := out[len(out)-1]
	out[len(out)-1] = Seg{Dim: DimFile, Ident: filepath.Join(last.Ident, sub)}
	return Scope{Segs: out, Source: s.Source}
}

// LastIsFile 判断最后一段是不是目录维度（决定裸名字该当子目录还是仓库）。
func (s Scope) LastIsFile() bool {
	if len(s.Segs) == 0 {
		return false
	}
	return s.Segs[len(s.Segs)-1].Dim == DimFile
}

// ---------------------------------------------------------------- 解析

// Resolve 现算：节点类型 + 仓库集合 + 子项 + 汇总。
// 被 hide 的仓库在任何节点下都不出现。
func Resolve(sc Scope, st *state.State) (*Resolved, error) {
	cur := Visible(st)
	last := Seg{}
	have := false
	for _, g := range sc.Segs {
		next := make([]*state.Repo, 0, len(cur))
		switch g.Dim {
		case DimFile:
			for _, r := range cur {
				if util.HasPrefixPath(r.Path, g.Ident) {
					next = append(next, r)
				}
			}
		case DimGroup:
			for _, r := range cur {
				if r.Group != "" && strings.EqualFold(r.Group, g.Ident) {
					next = append(next, r)
				}
			}
		case DimTag:
			for _, r := range cur {
				if hasTag(r, g.Ident) {
					next = append(next, r)
				}
			}
		default:
			// 仓库是叶子，标识又经常写成完整路径。
			// 先在当前集合里**精确**匹配（下钻/交集语义），找不到再去全集找（绝对定位）。
			// 不用当前层的模糊匹配兜底：父目录和子仓库同名的很多，模糊命中会挑错。
			hit := pickExact(cur, g.Ident)
			if hit == nil {
				hit = pickRepo(Visible(st), g.Ident)
			}
			if hit == nil {
				return nil, fmt.Errorf("找不到仓库 %s", g.Ident)
			}
			next = append(next, hit)
		}
		if len(next) == 0 {
			return nil, fmt.Errorf("%s 没有匹配到任何仓库", g.Dim+":"+g.Ident)
		}
		cur = next
		last = g
		have = true
	}

	// 目录自身是不是仓库。是的话它同时是"这里本身"和"别人的父目录"，
	// 两个视角都要留住，不能替用户挑一个。
	var self *state.Repo
	if have && last.Dim == DimFile {
		self = repoAt(cur, last.Ident)
	}

	kind := KindCollection
	switch {
	case !have:
		kind = KindRoot
	case last.Dim == DimRepo:
		kind = KindRepo // repo: = 明确要看它自己
	case last.Dim == DimFile && self != nil && len(cur) == 1:
		kind = KindRepo // 目录里只有它自己，没有嵌套，不会歧义
	}
	// 自身是仓库且里面还套着别的仓库：这是集合界面，自身从集合里摘出来单独报，
	// 否则 ls 会列出一个指向自己的子项（file:A 里有个 file:A），而且它会被统计两次。
	if kind != KindRepo && self != nil {
		rest := make([]*state.Repo, 0, len(cur))
		for _, r := range cur {
			if r != self {
				rest = append(rest, r)
			}
		}
		cur = rest
	}

	res := &Resolved{Scope: sc, Kind: kind, Repos: cur, Self: self, Freshness: Freshness(st)}
	res.Totals = totalsOf(cur)
	res.Children = childrenOf(sc, cur, kind)
	return res, nil
}

// Visible 是 scope 的起点：清单全集减去被隐藏的目录。
func Visible(st *state.State) []*state.Repo {
	out := make([]*state.Repo, 0, len(st.Repos))
	for _, r := range st.Repos {
		if st.IsHidden(r.Path) {
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

// pickExact 只做精确匹配：路径完全相同，或名字完全相同（重名取路径最短的）。
// 用于"在上一层里找"这种语义 —— 模糊命中在这里只会挑错。
func pickExact(in []*state.Repo, name string) *state.Repo {
	if name == "" {
		return nil
	}
	want := util.NormalizePath(name)
	var byName *state.Repo
	for _, r := range in {
		if util.NormalizePath(r.Path) == want {
			return r
		}
		if strings.EqualFold(r.Name, name) && (byName == nil || len(r.Path) < len(byName.Path)) {
			byName = r
		}
	}
	return byName
}

// pickRepo 在给定集合里定位仓库：先按完整路径（`repo:D:\opensource` 这种写法），
// 再按名字，最后按路径片段模糊匹配；重名时取路径最短的那个（更像是直接子项）。
func pickRepo(in []*state.Repo, name string) *state.Repo {
	if name == "" {
		return nil
	}
	// 标识里带分隔符或盘符，当路径处理
	if strings.ContainsAny(name, `\/`) || (len(name) > 1 && name[1] == ':') {
		want := util.NormalizePath(name)
		for _, r := range in {
			if util.NormalizePath(r.Path) == want {
				return r
			}
		}
	}
	var exact, partial []*state.Repo
	lower := strings.ToLower(name)
	for _, r := range in {
		if strings.EqualFold(r.Name, name) {
			exact = append(exact, r)
			continue
		}
		if strings.Contains(strings.ToLower(r.ID), lower) {
			partial = append(partial, r)
		}
	}
	if len(exact) > 0 {
		sort.SliceStable(exact, func(i, j int) bool { return len(exact[i].Path) < len(exact[j].Path) })
		return exact[0]
	}
	if len(partial) > 0 {
		sort.SliceStable(partial, func(i, j int) bool { return len(partial[i].Path) < len(partial[j].Path) })
		return partial[0]
	}
	return nil
}

// repoAt 找出路径正好等于这里的那个仓库（目录自身是仓库时才有）。
func repoAt(in []*state.Repo, path string) *state.Repo {
	want := util.NormalizePath(path)
	for _, r := range in {
		if util.NormalizePath(r.Path) == want {
			return r
		}
	}
	return nil
}

func totalsOf(in []*state.Repo) Totals {
	var t Totals
	t.Repos = len(in)
	for _, r := range in {
		if r.Dirty {
			t.Dirty++
		}
		if r.Ahead > 0 {
			t.Ahead++
		}
		if !r.HasUpstream {
			t.NoUpstream++
		}
		if r.Missing {
			t.Missing++
		}
	}
	return t
}

// Freshness 返回"距上次扫描多久"，没扫描过返回空串。
func Freshness(st *state.State) string {
	if len(st.Scans) == 0 {
		return ""
	}
	t, err := time.Parse(time.RFC3339, st.Scans[len(st.Scans)-1].At)
	if err != nil {
		return ""
	}
	return util.Age(t)
}

// ---------------------------------------------------------------- 子项

func childrenOf(sc Scope, repos []*state.Repo, kind string) []Item {
	switch kind {
	case KindRoot:
		return rootChildren(repos)
	case KindRepo:
		return nil
	}
	last := sc.Segs[len(sc.Segs)-1]
	if last.Dim == DimFile {
		return dirChildren(sc, repos, last.Ident)
	}
	return repoChildren(sc, repos)
}

// rootChildren 列出三个维度入口，各带数量。
func rootChildren(repos []*state.Repo) []Item {
	out := make([]Item, 0, 16)
	out = append(out, facetItems(DimFile, "按目录", dirFacet(repos))...)
	out = append(out, facetItems(DimGroup, "按分组", groupFacet(repos))...)
	out = append(out, facetItems(DimTag, "按标签", tagFacet(repos))...)
	return out
}

func facetItems(dim, label string, in []facet) []Item {
	out := make([]Item, 0, len(in)+1)
	for _, f := range in {
		out = append(out, Item{
			Facet:  dim,
			Label:  dim + ":" + f.name,
			Target: dim + ":" + f.name,
			Count:  f.count,
		})
	}
	if len(out) == 0 {
		out = append(out, Item{Facet: dim, Label: label + "（还没有）", Count: 0})
	}
	return out
}

type facet struct {
	name  string
	count int
}

// dirFacet 把仓库归到盘符下的第一层目录，避免根下被几十个深目录铺满。
func dirFacet(repos []*state.Repo) []facet {
	m := map[string]int{}
	for _, r := range repos {
		m[topDir(r.Path)]++
	}
	return sortedFacets(m)
}

func groupFacet(repos []*state.Repo) []facet {
	m := map[string]int{}
	for _, r := range repos {
		if r.Group == "" {
			continue
		}
		fold := r.Group
		for k := range m {
			if strings.EqualFold(k, r.Group) {
				fold = k
				break
			}
		}
		m[fold]++
	}
	return sortedFacets(m)
}

func tagFacet(repos []*state.Repo) []facet {
	m := map[string]int{}
	for _, r := range repos {
		for _, t := range r.Tags {
			fold := t
			for k := range m {
				if strings.EqualFold(k, t) {
					fold = k
					break
				}
			}
			m[fold]++
		}
	}
	return sortedFacets(m)
}

func sortedFacets(m map[string]int) []facet {
	out := make([]facet, 0, len(m))
	for k, v := range m {
		out = append(out, facet{name: k, count: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// dirChildren 集合节点下的 file 段：直属子目录（下钻）+ 直属仓库（进入）。
func dirChildren(sc Scope, repos []*state.Repo, dir string) []Item {
	base := util.NormalizePath(dir)
	sub := map[string]int{}
	var leaves []*state.Repo
	for _, r := range repos {
		rp := util.NormalizePath(r.Path)
		if rp == base {
			leaves = append(leaves, r)
			continue
		}
		rel := strings.TrimPrefix(rp, base+"/")
		parts := strings.Split(rel, "/")
		if len(parts) == 1 {
			leaves = append(leaves, r)
			continue
		}
		sub[parts[0]]++
	}
	out := make([]Item, 0, len(sub)+len(leaves))
	for _, it := range sortedFacets(sub) {
		out = append(out, Item{
			Facet:  DimFile,
			Label:  it.name + "/",
			Target: sc.DescendFile(it.name).String(),
			Count:  it.count,
		})
	}
	out = append(out, repoChildren(sc, leaves)...)
	return out
}

func repoChildren(sc Scope, repos []*state.Repo) []Item {
	out := make([]Item, 0, len(repos))
	in := make([]*state.Repo, len(repos))
	copy(in, repos)
	sort.SliceStable(in, func(i, j int) bool { return strings.ToLower(in[i].Name) < strings.ToLower(in[j].Name) })
	for _, r := range in {
		out = append(out, Item{
			Facet:   DimRepo,
			Label:   r.Name,
			Target:  sc.Child(Seg{Dim: DimRepo, Ident: r.Name}).String(),
			Count:   1,
			Summary: Summary(r),
		})
	}
	return out
}

// isVolumeRoot 判断路径是不是只剩盘符（D:\ 或 /），这种层不值得当一级作用域。
func isVolumeRoot(p string) bool {
	if p == "" || p == "." {
		return false
	}
	p = filepath.Clean(p)
	if p == string(filepath.Separator) {
		return true
	}
	vol := filepath.VolumeName(p)
	return vol != "" && p == vol+string(filepath.Separator)
}

// topDir 取路径在盘符/根之后的第一层。
func topDir(p string) string {
	p = filepath.Clean(p)
	vol := filepath.VolumeName(p)
	rest := strings.TrimLeft(strings.TrimPrefix(p, vol), `/\`)
	if rest == "" {
		if vol == "" {
			return string(filepath.Separator)
		}
		return vol + string(filepath.Separator)
	}
	parts := strings.FieldsFunc(rest, func(c rune) bool { return c == '/' || c == '\\' })
	if vol == "" {
		return string(filepath.Separator) + parts[0]
	}
	return vol + string(filepath.Separator) + parts[0]
}

// Summary 给仓库子项的一句话摘要。
func Summary(r *state.Repo) string {
	var b []string
	if r.Branch != "" {
		b = append(b, r.Branch)
	}
	if r.Ahead > 0 {
		b = append(b, fmt.Sprintf("领先 %d", r.Ahead))
	}
	if r.Behind > 0 {
		b = append(b, fmt.Sprintf("落后 %d", r.Behind))
	}
	if r.Dirty {
		b = append(b, "脏")
	}
	if !r.HasUpstream && r.PrimaryURL != "" {
		b = append(b, "无上游")
	}
	if r.Missing {
		b = append(b, "已丢失")
	}
	return strings.Join(b, " · ")
}
