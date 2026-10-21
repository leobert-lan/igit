package export

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grscan/internal/render"
	"grscan/internal/state"
	"grscan/internal/tree"
)

// GenerateGlobalTree 生成 02_global_tree.md：全局物理目录树与路径清单
func GenerateGlobalTree(w io.Writer, repos []*state.Repo, folded []string) error {
	var b strings.Builder
	b.WriteString("# 全局物理目录树与路径清单 (Global Tree)\n\n")
	b.WriteString(fmt.Sprintf("> 生成时间: %s · 仓库总数: **%d** 个\n\n",
		time.Now().Format("2006-01-02 15:04"), len(repos)))

	// 1. 文件系统层级树状结构
	b.WriteString("## 1. 物理层级目录树 (Directory Tree)\n\n")
	b.WriteString("```\n")
	nodes := tree.Build(repos)
	lines := tree.Render(nodes, tree.Opt{Depth: 6, Brief: true, Folded: folded})
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("```\n\n")

	// 2. 全量物理路径清单（按字母升序排列）
	b.WriteString("## 2. 完整物理路径对照表 (Path Mapping)\n\n")
	b.WriteString("| 仓库名称 | 物理绝对路径 | 分支 | 分组 | 标签 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")

	sortedRepos := append([]*state.Repo{}, repos...)
	sort.Slice(sortedRepos, func(i, j int) bool {
		return strings.ToLower(sortedRepos[i].Path) < strings.ToLower(sortedRepos[j].Path)
	})

	for _, r := range sortedRepos {
		cleanPath := filepath.ToSlash(r.Path)
		grp := r.Group
		if grp == "" {
			grp = "-"
		}
		tags := render.TagsLabel(r)
		if tags == "" {
			tags = "-"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |\n",
			escapeCell(r.Name),
			cleanPath,
			escapeCell(render.BranchLabel(r)),
			escapeCell(grp),
			escapeCell(tags),
		))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}
