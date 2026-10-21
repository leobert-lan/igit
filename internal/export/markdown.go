package export

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"grscan/internal/render"
	"grscan/internal/state"
	"grscan/internal/tree"
	"grscan/internal/util"
)

// MarkdownTree 按目录树组织成一份可读的报告：概览 → 按目录分节 → 线索。
// 带完整路径，方便贴进笔记、喂给 AI 或者直接打印出来琢磨。
func MarkdownTree(w io.Writer, repos []*state.Repo, folded []string) error {
	var b strings.Builder
	b.WriteString("# 本地 git 仓库清单\n\n")
	b.WriteString(fmt.Sprintf("> 生成于 %s · 共 **%d** 个仓库\n\n",
		time.Now().Format("2006-01-02 15:04"), len(repos)))

	b.WriteString(overview(repos))
	b.WriteString(signals(repos))
	b.WriteString(directorySections(repos, folded))

	_, err := io.WriteString(w, b.String())
	return err
}

// partitionFolded 把仓库分成"照常列表格"和"被折叠、只给汇总"两部分。
func partitionFolded(repos []*state.Repo, folded []string) (visible []*state.Repo, groups map[string][]*state.Repo) {
	groups = map[string][]*state.Repo{}
	for _, r := range repos {
		hit := ""
		for _, d := range folded {
			if util.HasPrefixPath(r.Path, d) {
				hit = d
				break
			}
		}
		if hit == "" {
			visible = append(visible, r)
			continue
		}
		groups[hit] = append(groups[hit], r)
	}
	return visible, groups
}

// MarkdownFlat 平铺表格版，老格式，适合直接粘表格的场景。
func MarkdownFlat(w io.Writer, repos []*state.Repo) error {
	if _, err := fmt.Fprintf(w, "| 仓库 | 分支 | 改动 | 最近提交 | 分组 | 路径 |\n|---|---|---|---|---|---|---|\n"); err != nil {
		return err
	}
	for _, r := range repos {
		if _, err := fmt.Fprintf(w, "| %s | %s | %s | %s | %s | `%s` |\n",
			escapeCell(r.Name), escapeCell(branchLabel(r)), render.ChangeLabel(r),
			render.CommitAge(r), escapeCell(orDash(r.Group)), escapeCell(r.Path)); err != nil {
			return err
		}
	}
	return nil
}

// MarkdownOutline 只输出目录树骨架，一眼看清仓库在磁盘上的分布格局。
func MarkdownOutline(w io.Writer, repos []*state.Repo, folded []string) error {
	nodes := tree.Build(repos)
	lines := tree.Render(nodes, tree.Opt{Depth: 3, Brief: true, Folded: folded})
	var b strings.Builder
	b.WriteString("```\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("```\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func overview(repos []*state.Repo) string {
	//byStatus := map[string]int{}
	groups := map[string]int{}
	dirty, noRemote, ahead, missing := 0, 0, 0, 0
	for _, r := range repos {
		//byStatus[render.StatusCN(r)]++
		groups[orDash(r.Group)]++
		if r.Dirty {
			dirty++
		}
		if r.PrimaryURL == "" {
			noRemote++
		}
		if r.Ahead > 0 {
			ahead++
		}
		if r.Missing {
			missing++
		}
	}

	var b strings.Builder
	b.WriteString("## 概览\n\n")
	//b.WriteString("| 状态 | 数量 |\n|---|---|\n")
	//order := []string{"待处理", "跟进中", "已处理", "已归档", "已忽略"}
	////seen := map[string]bool{}
	//for _, s := range order {
	//	if byStatus[s] > 0 {
	//		b.WriteString(fmt.Sprintf("| %s | %d |\n", s, byStatus[s]))
	//		seen[s] = true
	//	}
	//}
	//for s, n := range byStatus {
	//	if !seen[s] && n > 0 {
	//		b.WriteString(fmt.Sprintf("| %s | %d |\n", escapeCell(s), n))
	//	}
	//}
	//b.WriteString("\n")

	names := util.SortedKeys(groups)
	if len(names) > 0 && !(len(names) == 1 && names[0] == "-") {
		b.WriteString("按分组：")
		parts := []string{}
		for _, n := range names {
			if groups[n] == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s (%d)", n, groups[n]))
		}
		b.WriteString(strings.Join(parts, " · ") + "\n\n")
	}
	return b.String()
}

// signals 把"值得动手的理由"挑出来，省得自己在几百行里翻。
func signals(repos []*state.Repo) string {
	type bucket struct {
		title string
		why   string
		items []string
	}
	dirty := []string{}
	noRemote := []string{}
	unpushed := []string{}
	gone := []string{}
	stale := []string{}
	cutoff := time.Now().AddDate(-1, 0, 0)

	for _, r := range repos {
		label := fmt.Sprintf("`%s`", r.Path)
		if r.Dirty {
			dirty = append(dirty, fmt.Sprintf("%s（%s）", label, render.ChangeLabel(r)))
		}
		if r.PrimaryURL == "" {
			noRemote = append(noRemote, label)
		}
		if r.Ahead > 0 {
			unpushed = append(unpushed, fmt.Sprintf("%s（领先 %d）", label, r.Ahead))
		}
		if r.Missing {
			gone = append(gone, label)
		}
		if t := render.CommitTime(r); !t.IsZero() && t.Before(cutoff) {
			stale = append(stale, fmt.Sprintf("%s（%s）", label, render.CommitAge(r)))
		}
	}

	buckets := []bucket{
		{"磁盘上已找不到", "目录被删了，只剩清单里的一条记录", gone},
		{"有未提交改动", "可能有没存下来的东西，删之前看一眼", dirty},
		{"有未推送的提交", "本地领先远端，别的机器上没有", unpushed},
		{"没有配置远端", "纯本地仓库，删了就没了", noRemote},
		{"一年以上没提交", "大概率是过期的实验或临时克隆", stale},
	}

	var b strings.Builder
	empty := true
	for _, bk := range buckets {
		if len(bk.items) == 0 {
			continue
		}
		empty = false
	}
	if empty {
		return ""
	}

	b.WriteString("## 值得先看的几类\n\n")
	for _, bk := range buckets {
		if len(bk.items) == 0 {
			continue
		}
		sort.Strings(bk.items)
		b.WriteString(fmt.Sprintf("### %s（%d 个）\n\n", bk.title, len(bk.items)))
		b.WriteString(fmt.Sprintf("%s\n\n", bk.why))
		for _, it := range bk.items {
			b.WriteString("- " + it + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func directorySections(repos []*state.Repo, folded []string) string {
	nodes := tree.Build(repos)
	var b strings.Builder
	b.WriteString("## 按目录\n\n")

	// 先给一个全局骨架，方便定位
	b.WriteString("```\n")
	for _, l := range tree.Render(nodes, tree.Opt{Depth: 4, Brief: true, Folded: folded}) {
		b.WriteString(l + "\n")
	}
	b.WriteString("```\n\n")

	// 折叠目录先整体出节：每个折叠目录只出现一次，
	// 否则它会被拆进多个小节里反复刷同一句话
	rest, foldedGroups := partitionFolded(repos, folded)
	for _, d := range util.SortedKeys(foldedGroups) {
		rs := foldedGroups[d]
		b.WriteString(fmt.Sprintf("### `%s`\n\n", d))
		b.WriteString(fmt.Sprintf("%d 个仓库（已折叠，未逐条列出）\n\n", len(rs)))
		if dirty := countDirty(rs); dirty > 0 {
			b.WriteString(fmt.Sprintf("其中 %d 个有未提交改动。\n\n", dirty))
		}
		b.WriteString(fmt.Sprintf("展开看细节：`grscan unfold \"%s\"`\n\n", d))
	}
	if len(rest) == 0 {
		return b.String()
	}
	restNodes := tree.Build(rest)

	for _, g := range tree.Sections(restNodes, tree.DefaultSectionSize) {
		b.WriteString(fmt.Sprintf("### `%s`\n\n", g.Dir))
		b.WriteString(fmt.Sprintf("%d 个仓库\n\n", g.Count))
		visible := g.Repos
		b.WriteString("| 仓库 | 分支 | 改动 | 同步 | 最近提交 | 分组 | 标签 | 备注 |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
		for _, r := range visible {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				escapeCell(r.Name),
				escapeCell(branchLabel(r)),
				render.ChangeLabel(r),
				render.SyncLabel(r),
				render.CommitAge(r),
				escapeCell(orDash(r.Group)),
				escapeCell(orDash(render.TagsLabel(r))),
				escapeCell(orDash(r.Note)),
			))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func countDirty(repos []*state.Repo) int {
	n := 0
	for _, r := range repos {
		if r.Dirty {
			n++
		}
	}
	return n
}

// branchLabel 统一走 render，避免多处各写一遍同样的分支显示逻辑。
func branchLabel(r *state.Repo) string { return render.BranchLabel(r) }
