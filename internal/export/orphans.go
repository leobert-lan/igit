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
)

// DirOrphanGroup 聚合在同一个父目录下的游离仓库集合
type DirOrphanGroup struct {
	ParentDir string
	Repos     []*state.Repo
	Dirty     int
	NoRemote  int
	Ahead     int
	Missing   int
}

// GenerateOrphansTodo 生成 00_orphans_todo.md：游离仓库待整理地图
// 依物理父目录聚合，评估风险与优先级，指导用户下一步去哪个目录下集中整理。
// 注意：被 hide（隐藏）、ignore（忽略）或已归档的仓库，明确不属于日常游离资产，坚决不骚扰用户。
func GenerateOrphansTodo(w io.Writer, repos []*state.Repo, st *state.State) error {
	var totalRepos = len(repos)
	var orphans []*state.Repo
	var totallyWild = 0 // 既无 Group 又无 Tag
	var ungrouped = 0   // 无 Group，有 Tag
	var dirtyCount = 0
	var noRemoteCount = 0
	var aheadCount = 0

	for _, r := range repos {
		// 1. 处于 hide 隐藏目录下的仓库：用户已表态不打算管理，绝不作为游离态催促用户
		if st != nil && st.IsHidden(r.Path) {
			continue
		}
		// 2. 明确忽略或归档的仓库：亦不属于未治理的游离资产
		if r.Ignored {
			continue
		}

		if strings.TrimSpace(r.Group) == "" {
			orphans = append(orphans, r)
			if len(r.Tags) == 0 {
				totallyWild++
			} else {
				ungrouped++
			}
			if r.Dirty {
				dirtyCount++
			}
			if r.PrimaryURL == "" {
				noRemoteCount++
			}
			if r.Ahead > 0 {
				aheadCount++
			}
		}
	}

	var b strings.Builder
	b.WriteString("# 游离仓库待整理地图 (Orphans Todo)\n\n")
	b.WriteString(fmt.Sprintf("> 生成时间: %s · 全量仓库: %d · **游离待整理: %d** (占比 %.1f%%)\n\n",
		time.Now().Format("2006-01-02 15:04"),
		totalRepos,
		len(orphans),
		float64(len(orphans))/float64(max(totalRepos, 1))*100.0,
	))

	if len(orphans) == 0 {
		b.WriteString("🎉 **太棒了！当前没有发现任何游离仓库，所有仓库都已纳入 Group 治理体系中。**\n\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	// 1. 梳理进度与风险看板
	b.WriteString("## 1. 整理进度与风险概览\n\n")
	b.WriteString("| 指标分类 | 数量 | 占比 | 说明 |\n")
	b.WriteString("|---|:---:|:---:|---|\n")
	b.WriteString(fmt.Sprintf("| **完全游离 (Wild)** | %d | %.1f%% | 既未分配 Group，也未打任何 Tag |\n",
		totallyWild, float64(totallyWild)/float64(len(orphans))*100.0))
	b.WriteString(fmt.Sprintf("| **未分组 (Ungrouped)** | %d | %.1f%% | 带有临时 Tag，但未明确归入业务 Group |\n",
		ungrouped, float64(ungrouped)/float64(len(orphans))*100.0))
	b.WriteString(fmt.Sprintf("| ⚠️ **含未提交改动 (Dirty)** | %d | - | 存在未暂存/未跟踪改动，整理清理时须防丢失 |\n", dirtyCount))
	b.WriteString(fmt.Sprintf("| ⚠️ **纯本地无远端 (No Remote)** | %d | - | 无远程仓库，本地即唯一孤本 |\n", noRemoteCount))
	b.WriteString(fmt.Sprintf("| ⚠️ **本地领先远端 (Ahead)** | %d | - | 本地有未推送到远端的提交 |\n", aheadCount))
	b.WriteString("\n")

	// 2. 按物理父目录聚合分析
	dirMap := map[string]*DirOrphanGroup{}
	for _, r := range orphans {
		pDir := filepath.Dir(r.Path)
		g, ok := dirMap[pDir]
		if !ok {
			g = &DirOrphanGroup{ParentDir: pDir}
			dirMap[pDir] = g
		}
		g.Repos = append(g.Repos, r)
		if r.Dirty {
			g.Dirty++
		}
		if r.PrimaryURL == "" {
			g.NoRemote++
		}
		if r.Ahead > 0 {
			g.Ahead++
		}
		if r.Missing {
			g.Missing++
		}
	}

	var dirList []*DirOrphanGroup
	for _, g := range dirMap {
		dirList = append(dirList, g)
	}

	// 排序：游离数量多者靠前，数量相同时脏改动多者靠前
	sort.Slice(dirList, func(i, j int) bool {
		if len(dirList[i].Repos) != len(dirList[j].Repos) {
			return len(dirList[i].Repos) > len(dirList[j].Repos)
		}
		return dirList[i].Dirty > dirList[j].Dirty
	})

	b.WriteString("## 2. 待整理目录热度排行榜 (按物理父目录聚合)\n\n")
	b.WriteString("> 依据各目录下堆积的游离仓库密度与改动风险，规划您下一步前往整理的目录路径。\n\n")
	b.WriteString("| 物理父目录路径 | 游离仓库数 | 脏改动 | 无远端 | 整理优先级 | 直达建议 |\n")
	b.WriteString("|---|:---:|:---:|:---:|:---:|---|\n")

	for _, g := range dirList {
		priority := "🟢 一般"
		switch {
		case g.Dirty > 0 && g.NoRemote > 0:
			priority = "🔴 极高 (脏改动+无远端)"
		case g.Dirty > 0:
			priority = "🔴 高 (含未提交)"
		case g.NoRemote > 0:
			priority = "🟡 高 (无远程孤本)"
		case len(g.Repos) >= 5:
			priority = "🟡 中 (聚集较多)"
		}

		cleanPath := filepath.ToSlash(g.ParentDir)
		b.WriteString(fmt.Sprintf("| `%s` | **%d** | %s | %s | %s | `igit cd \"file:%s\"` |\n",
			cleanPath,
			len(g.Repos),
			renderNum(g.Dirty),
			renderNum(g.NoRemote),
			priority,
			cleanPath,
		))
	}
	b.WriteString("\n")

	// 3. 逐目录展开明细与治理指引
	b.WriteString("## 3. 逐目录游离明细与整理指引\n\n")
	for idx, g := range dirList {
		cleanPath := filepath.ToSlash(g.ParentDir)
		b.WriteString(fmt.Sprintf("### %d. 📁 `%s` (共 %d 个游离仓库)\n\n", idx+1, cleanPath, len(g.Repos)))
		b.WriteString(fmt.Sprintf("- **直达作用域**: `igit cd \"file:%s\"`\n", cleanPath))
		b.WriteString(fmt.Sprintf("- **本目录风险**: 脏改动 %d 个 · 无远端 %d 个 · 领先上游 %d 个\n\n", g.Dirty, g.NoRemote, g.Ahead))

		b.WriteString("| 仓库名称 | 分支 | 改动 | 同步 | 最近提交 | 标签 | 快捷指派建议 |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")

		sort.Slice(g.Repos, func(i, j int) bool {
			return g.Repos[i].Name < g.Repos[j].Name
		})

		for _, r := range g.Repos {
			tagsStr := render.TagsLabel(r)
			if tagsStr == "" {
				tagsStr = "-"
			}
			suggestCmd := fmt.Sprintf("`igit set %s --group=<组名>`", r.Name)
			b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s |\n",
				escapeCell(r.Name),
				escapeCell(render.BranchLabel(r)),
				render.ChangeLabel(r),
				render.SyncLabel(r),
				render.CommitAge(r),
				escapeCell(tagsStr),
				suggestCmd,
			))
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func renderNum(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}
