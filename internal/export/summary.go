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
	"grscan/internal/util"
)

// GenerateGlobalSummary 生成 01_global_summary.md：全局概览与健康统计
func GenerateGlobalSummary(w io.Writer, repos []*state.Repo, st *state.State) error {
	var totalRepos = len(repos)
	var dirty = 0
	var ahead = 0
	var behind = 0
	var noRemote = 0
	var missing = 0
	var stale = 0
	cutoff := time.Now().AddDate(-1, 0, 0)

	//byStatus := map[string]int{}
	groupStats := map[string]*groupStat{}
	tagStats := map[string]*tagStat{}

	for _, r := range repos {
		//byStatus[render.StatusCN(r)]++

		if r.Dirty {
			dirty++
		}
		if r.Ahead > 0 {
			ahead++
		}
		if r.Behind > 0 {
			behind++
		}
		if r.PrimaryURL == "" {
			noRemote++
		}
		if r.Missing {
			missing++
		}
		if t := render.CommitTime(r); !t.IsZero() && t.Before(cutoff) {
			stale++
		}

		// Group 统计
		grp := r.Group
		if grp == "" {
			grp = "(未分组)"
		}
		gs, ok := groupStats[grp]
		if !ok {
			gs = &groupStat{Name: grp}
			groupStats[grp] = gs
		}
		gs.Count++
		if r.Dirty {
			gs.Dirty++
		}
		if r.Ahead > 0 {
			gs.Ahead++
		}

		// Tag 统计
		for _, tag := range r.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			ts, tok := tagStats[tag]
			if !tok {
				ts = &tagStat{Name: tag}
				tagStats[tag] = ts
			}
			ts.Count++
			if r.Dirty {
				ts.Dirty++
			}
		}
	}

	var b strings.Builder
	b.WriteString("# 全局资产与健康统计总览 (Global Summary)\n\n")
	b.WriteString(fmt.Sprintf("> 报告生成时间: %s · 全量收录仓库: **%d** 个\n\n",
		time.Now().Format("2006-01-02 15:04:05"), totalRepos))

	// 1. 全局资产健康大盘
	b.WriteString("## 1. 资产健康大盘 (Health Dashboard)\n\n")
	b.WriteString("| 健康维度 | 仓库数量 | 占比 | 状态含义与风险 |\n")
	b.WriteString("|---|:---:|:---:|---|\n")

	cleanCount := totalRepos - dirty
	b.WriteString(fmt.Sprintf("| 🟢 **工作区干净 (Clean)** | %d | %.1f%% | 工作区无暂存/未暂存修改 |\n",
		cleanCount, pct(cleanCount, totalRepos)))
	b.WriteString(fmt.Sprintf("| 🔴 **工作区改动 (Dirty)** | %d | %.1f%% | 包含未暂存或未跟踪文件，可能有未保存进展 |\n",
		dirty, pct(dirty, totalRepos)))
	b.WriteString(fmt.Sprintf("| 🟡 **本地领先 (Ahead)** | %d | %.1f%% | 本地 commit 领先上游，有未推代码 |\n",
		ahead, pct(ahead, totalRepos)))
	b.WriteString(fmt.Sprintf("| 🔵 **落后上游 (Behind)** | %d | %.1f%% | 上游有新 commit，需注意拉取同步 |\n",
		behind, pct(behind, totalRepos)))
	b.WriteString(fmt.Sprintf("| ⚪ **无远程源 (No Remote)** | %d | %.1f%% | 纯本地仓库，无远端备份 |\n",
		noRemote, pct(noRemote, totalRepos)))
	b.WriteString(fmt.Sprintf("| 💤 **长期停滞 (Stale)** | %d | %.1f%% | 超过 1 年没有新的提交记录 |\n",
		stale, pct(stale, totalRepos)))
	if missing > 0 {
		b.WriteString(fmt.Sprintf("| ❌ **磁盘丢失 (Missing)** | %d | %.1f%% | 记录在清单中但在磁盘已找不着 |\n",
			missing, pct(missing, totalRepos)))
	}
	b.WriteString("\n")

	// 2. 治理处理状态统计
	b.WriteString("## 2. 人工治理状态分布 (Triage Status)\n\n")
	b.WriteString("时时勤拂拭，不再提供自欺欺人的状态！")
	//order := []string{"待处理", "跟进中", "已处理", "已归档", "已忽略"}
	//for _, s := range order {
	//	cnt := byStatus[s]
	//	b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", s, cnt, pct(cnt, totalRepos)))
	//}
	//b.WriteString("\n")

	// 3. 组织分组统计 (Group Matrix)
	b.WriteString("## 3. 组织分组统计矩阵 (Group Matrix)\n\n")
	// 包括持久化在 state.Groups 中的空组
	var allGroupNames = map[string]bool{}
	for g := range groupStats {
		allGroupNames[g] = true
	}
	if st != nil {
		for _, g := range st.Groups {
			if _, ok := groupStats[g]; !ok {
				groupStats[g] = &groupStat{Name: g}
			}
			allGroupNames[g] = true
		}
	}

	var groupList []*groupStat
	for _, gs := range groupStats {
		groupList = append(groupList, gs)
	}
	sort.Slice(groupList, func(i, j int) bool {
		if groupList[i].Name == "(未分组)" {
			return false
		}
		if groupList[j].Name == "(未分组)" {
			return true
		}
		if groupList[i].Count != groupList[j].Count {
			return groupList[i].Count > groupList[j].Count
		}
		return groupList[i].Name < groupList[j].Name
	})

	b.WriteString(fmt.Sprintf("当前共定义了 **%d** 个业务分组：\n\n", len(groupList)))
	b.WriteString("| 分组名称 (Group) | 仓库数量 | 脏仓库数 | 领先上游数 | 快捷直达 |\n")
	b.WriteString("|---|:---:|:---:|:---:|---|\n")
	for _, gs := range groupList {
		scopeCmd := fmt.Sprintf("`igit cd \"group:%s\"`", gs.Name)
		if gs.Name == "(未分组)" {
			scopeCmd = "*(见 00_orphans_todo.md)*"
		}
		b.WriteString(fmt.Sprintf("| **%s** | %d | %s | %s | %s |\n",
			escapeCell(gs.Name),
			gs.Count,
			renderNum(gs.Dirty),
			renderNum(gs.Ahead),
			scopeCmd,
		))
	}
	b.WriteString("\n")

	// 4. 标签体系统计 (Tag Matrix)
	b.WriteString("## 4. 标签体系统计矩阵 (Tag Matrix)\n\n")
	var tagList []*tagStat
	for _, ts := range tagStats {
		tagList = append(tagList, ts)
	}
	sort.Slice(tagList, func(i, j int) bool {
		if tagList[i].Count != tagList[j].Count {
			return tagList[i].Count > tagList[j].Count
		}
		return tagList[i].Name < tagList[j].Name
	})

	if len(tagList) == 0 {
		b.WriteString("*(当前尚未为任何仓库配置 Tag 标签)*\n\n")
	} else {
		b.WriteString(fmt.Sprintf("当前共定义了 **%d** 个标签：\n\n", len(tagList)))
		b.WriteString("| 标签名称 (Tag) | 关联仓库数 | 包含脏改动 | 快捷直达 |\n")
		b.WriteString("|---|:---:|:---:|---|\n")
		for _, ts := range tagList {
			b.WriteString(fmt.Sprintf("| `#%s` | %d | %s | `igit cd \"tag:%s\"` |\n",
				escapeCell(ts.Name),
				ts.Count,
				renderNum(ts.Dirty),
				escapeCell(ts.Name),
			))
		}
		b.WriteString("\n")
	}

	// 5. 已隐藏资产配置概况 (Hidden Assets)
	if st != nil && len(st.Hidden) > 0 {
		b.WriteString("## 5. 已隐藏资产配置概况 (Hidden Assets)\n\n")
		b.WriteString("> 此处目录已被配置为 hide（免打扰），不纳入日常大盘与游离待办统计。\n\n")
		b.WriteString("| 已隐藏目录路径 | 已知仓库数 | 规则备注 | 解除隐藏建议 |\n")
		b.WriteString("|---|:---:|---|---|\n")

		for _, rule := range st.Hidden {
			count := 0
			for _, r := range repos {
				if util.HasPrefixPath(r.Path, rule.Path) {
					count++
				}
			}
			countStr := fmt.Sprintf("**%d 个**", count)
			if count == 0 {
				countStr = "未测算"
			}
			note := rule.Note
			if note == "" {
				note = "-"
			}
			cleanPath := filepath.ToSlash(rule.Path)
			unhideCmd := fmt.Sprintf("`igit unhide \"%s\"`", cleanPath)
			b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n",
				cleanPath, countStr, escapeCell(note), unhideCmd))
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

type groupStat struct {
	Name  string
	Count int
	Dirty int
	Ahead int
}

type tagStat struct {
	Name  string
	Count int
	Dirty int
}

func pct(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100.0
}
