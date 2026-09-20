package tree

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"grscan/internal/render"
	"grscan/internal/state"
	"grscan/internal/util"
)

// Node 目录树上的一个节点。Children 为空即为叶子。
type Node struct {
	Name     string
	Dir      string
	Children map[string]*Node
	Repos    []*state.Repo
}

func (n *Node) IsLeaf() bool { return len(n.Children) == 0 }

// Count 子树内仓库总数（含本节点直接挂载的）。
func (n *Node) Count() int {
	total := len(n.Repos)
	for _, c := range n.Children {
		total += c.Count()
	}
	return total
}

// Build 把仓库列表按磁盘路径搭成一棵树，返回每个盘一个根节点。
func Build(repos []*state.Repo) []*Node {
	byVolume := map[string]*Node{}
	order := []string{}

	ensure := func(vol string) *Node {
		if n, ok := byVolume[vol]; ok {
			return n
		}
		n := &Node{Name: vol, Dir: vol, Children: map[string]*Node{}}
		byVolume[vol] = n
		order = append(order, vol)
		return n
	}

	for _, r := range repos {
		parts := splitPath(r.Path)
		if len(parts) == 0 {
			continue
		}
		root := ensure(parts[0])
		cur := root
		acc := parts[0]
		for _, part := range parts[1:] {
			acc = filepath.Join(acc, part)
			child, ok := cur.Children[part]
			if !ok {
				child = &Node{Name: part, Dir: acc, Children: map[string]*Node{}}
				cur.Children[part] = child
			}
			cur = child
		}
		cur.Repos = append(cur.Repos, r)
	}

	out := make([]*Node, 0, len(order))
	for _, vol := range order {
		out = append(out, byVolume[vol])
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	for _, n := range out {
		collapse(n)
	}
	return out
}

// collapse 把"只有一个子节点且自身不挂仓库"的链条合并成一层，
// 免得树里面出现一长串没意义的一对一嵌套。
func collapse(n *Node) {
	if n.IsLeaf() {
		return
	}
	for {
		if len(n.Repos) != 0 || len(n.Children) != 1 {
			break
		}
		var only *Node
		for _, c := range n.Children {
			only = c
		}
		if only == nil {
			break
		}
		n.Name = filepath.Join(n.Name, only.Name)
		n.Dir = only.Dir
		n.Repos = only.Repos
		n.Children = only.Children
		if n.IsLeaf() {
			return
		}
	}
	kids := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		kids = append(kids, c)
	}
	sort.Slice(kids, func(i, j int) bool { return strings.ToLower(kids[i].Name) < strings.ToLower(kids[j].Name) })
	for _, c := range kids {
		collapse(c)
	}
}

func splitPath(p string) []string {
	cleaned := filepath.Clean(p)
	vol := filepath.VolumeName(cleaned)
	if vol == "" {
		parts := strings.Split(strings.Trim(cleaned, "/\\"), string(filepath.Separator))
		out := []string{string(filepath.Separator)}
		for _, s := range parts {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(cleaned, vol), `\`)
	out := []string{vol + `\`}
	if rest == "" {
		return out
	}
	for _, s := range strings.Split(rest, `\`) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Opt 控制树的渲染方式。
type Opt struct {
	Depth    int      // 0 = 不限；>0 时超出深度的子树折叠成一行摘要
	Brief    bool     // 只列目录与数量，不逐个列仓库
	ShowAll  bool     // 叶子上是否显示完整路径
	MaxWidth int      // 0 = 不截断
	Folded   []string // 这些目录下只给汇总，不展开（用户显式折叠的目录）
}

// Render 生成整棵树的文本行。
func Render(nodes []*Node, opt Opt) []string {
	out := []string{}
	for _, n := range nodes {
		renderNode(n, "", 0, true, opt, &out)
	}
	return out
}

func isFolded(n *Node, opt Opt) bool {
	return util.HasAnyPrefix(n.Dir, opt.Folded)
}

func renderNode(n *Node, prefix string, depth int, isLast bool, opt Opt, out *[]string) {
	var branch string
	switch {
	case prefix == "":
		branch = ""
	case isLast:
		branch = "└── "
	default:
		branch = "├── "
	}

	label := n.Name
	if !n.IsLeaf() && !strings.HasSuffix(label, `\`) && !strings.HasSuffix(label, "/") {
		label += string(filepath.Separator)
	}
	dirLine := prefix + branch + label

	if n.IsLeaf() {
		// 叶子节点：目录名 + 挂在它下面的仓库
		if opt.Brief {
			*out = append(*out, fmt.Sprintf("%s  · %d 个仓库", dirLine, n.Count()))
			return
		}
		*out = append(*out, dirLine)
		kids := sortedRepos(n.Repos)
		for i, r := range kids {
			last := i == len(kids)-1
			mark := "├── "
			if last {
				mark = "└── "
			}
			line := childPrefix(prefix, isLast) + mark + repoLabel(r, opt)
			*out = append(*out, line)
		}
		return
	}

	if isFolded(n, opt) {
		// 用户显式折叠：只留一行汇总，unfold 才会展开
		tail := ""
		if dirty := countDirty(n); dirty > 0 {
			tail += fmt.Sprintf(" · %d 有改动", dirty)
		}
		*out = append(*out, fmt.Sprintf("%s  · %d 个仓库%s（已折叠，grscan unfold 展开）", dirLine, n.Count(), tail))
		return
	}

	if opt.Depth > 0 && depth >= opt.Depth {
		*out = append(*out, fmt.Sprintf("%s  · %d 个仓库（用 --depth 展开）", dirLine, n.Count()))
		return
	}

	*out = append(*out, dirLine)
	if len(n.Repos) > 0 {
		if opt.Brief {
			// brief 只砍掉具体仓库条目，目录结构照常展开
			*out = append(*out, fmt.Sprintf("%s%s· %d 个仓库", childPrefix(prefix, isLast), "└── ", len(n.Repos)))
		} else {
			kids := sortedRepos(n.Repos)
			for i, r := range kids {
				mark := "├── "
				if i == len(kids)-1 && len(n.Children) == 0 {
					mark = "└── "
				}
				*out = append(*out, childPrefix(prefix, isLast)+mark+repoLabel(r, opt))
			}
		}
	}

	cs := sortedNodes(n.Children)
	for i, c := range cs {
		last := i == len(cs)-1
		renderNode(c, childPrefix(prefix, isLast), depth+1, last, opt, out)
	}
}

func countDirty(n *Node) int {
	total := 0
	var walk func(*Node)
	walk = func(x *Node) {
		for _, r := range x.Repos {
			if r.Dirty {
				total++
			}
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return total
}

func childPrefix(prefix string, isLast bool) string {
	switch {
	case prefix == "":
		return "    "
	case isLast:
		return prefix + "    "
	default:
		return prefix + "│   "
	}
}

func repoLabel(r *state.Repo, opt Opt) string {
	parts := []string{r.Name, render.BranchLabel(r)}
	parts = append(parts, render.ChangeLabel(r))
	parts = append(parts, render.CommitAge(r))
	if r.Group != "" {
		parts = append(parts, "["+r.Group+"]")
	}
	parts = append(parts, render.StatusCN(r))
	if opt.ShowAll {
		parts = append(parts, r.Path)
	}
	line := strings.Join(parts, "  ")
	if opt.MaxWidth > 0 {
		line = util.Truncate(line, opt.MaxWidth)
	}
	return line
}

func sortedRepos(in []*state.Repo) []*state.Repo {
	out := append([]*state.Repo{}, in...)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func sortedNodes(in map[string]*Node) []*Node {
	out := make([]*Node, 0, len(in))
	for _, n := range in {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// DefaultSectionSize 分节时单个目录容纳仓库的上限，超过就继续下钻到子目录。
const DefaultSectionSize = 25

// Sections 把树切成若干"规模合适"的节，供 Markdown 报告分节。
// 规则：某个子树仓库数不超过 maxPer 就整节输出；否则继续往下钻。
// 这样既不会出现"一个目录一节、共两百节"的灾难，也不会把整个盘塞进一张表。
func Sections(nodes []*Node, maxPer int) []Group {
	if maxPer <= 0 {
		maxPer = DefaultSectionSize
	}
	out := []Group{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Count() == 0 {
			return
		}
		biggest := 0
		for _, c := range n.Children {
			if c.Count() > biggest {
				biggest = c.Count()
			}
		}
		// 下钻的前提是"拆开有意义"：子节点里还有成规模的。
		// 如果底下全是清一色的单仓库目录，硬拆只会多出一堆标题，
		// 不如整节列出来。
		if n.Count() <= maxPer || biggest <= 1 || len(n.Children) == 0 {
			out = append(out, Group{Dir: n.Dir, Count: n.Count(), Repos: collectRepos(n)})
			return
		}
		if len(n.Repos) > 0 {
			out = append(out, Group{Dir: n.Dir, Count: len(n.Repos), Repos: sortedRepos(n.Repos)})
		}
		for _, c := range sortedNodes(n.Children) {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// collectRepos 收集整棵子树的仓库，按路径排序。
func collectRepos(n *Node) []*state.Repo {
	out := []*state.Repo{}
	var walk func(*Node)
	walk = func(x *Node) {
		out = append(out, x.Repos...)
		for _, c := range sortedNodes(x.Children) {
			walk(c)
		}
	}
	walk(n)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

// Group 是"某个目录 + 它容纳的仓库"，供 Markdown 报告按目录分节。
type Group struct {
	Dir   string
	Count int
	Repos []*state.Repo
}

// Groups 深度优先列出所有直接挂着仓库的目录。
func Groups(nodes []*Node) []Group {
	out := []Group{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if len(n.Repos) > 0 {
			out = append(out, Group{Dir: n.Dir, Count: len(n.Repos), Repos: sortedRepos(n.Repos)})
		}
		for _, c := range sortedNodes(n.Children) {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}
