package export

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grscan/internal/render"
	"grscan/internal/scope"
	"grscan/internal/state"
	"grscan/internal/tree"
)

// ScopeFileName 计算当前作用域专有报告的文件名
func ScopeFileName(res *scope.Resolved) string {
	if res == nil || res.Scope.IsRoot() {
		return "03_scope_root.md"
	}
	segs := res.Scope.Segs
	if len(segs) == 0 {
		return "03_scope_root.md"
	}
	last := segs[len(segs)-1]
	sanitized := sanitizeIdent(last.Ident)
	switch last.Dim {
	case scope.DimGroup:
		return fmt.Sprintf("03_scope_group_%s.md", sanitized)
	case scope.DimTag:
		return fmt.Sprintf("03_scope_tag_%s.md", sanitized)
	case scope.DimFile:
		base := filepath.Base(last.Ident)
		return fmt.Sprintf("03_scope_dir_%s.md", sanitizeIdent(base))
	case scope.DimRepo:
		return fmt.Sprintf("03_scope_repo_%s.md", sanitized)
	default:
		return fmt.Sprintf("03_scope_%s_%s.md", last.Dim, sanitized)
	}
}

// GenerateScopeReport 根据当前作用域动态生成专属报告
func GenerateScopeReport(w io.Writer, res *scope.Resolved, allRepos []*state.Repo, folded []string) (string, error) {
	var fileName = ScopeFileName(res)
	var b strings.Builder

	if res == nil || res.Scope.IsRoot() {
		return fileName, generateRootReport(&b, allRepos, folded, w)
	}

	segs := res.Scope.Segs
	last := segs[len(segs)-1]

	switch last.Dim {
	case scope.DimGroup:
		generateGroupReport(&b, last.Ident, res)
	case scope.DimTag:
		generateTagReport(&b, last.Ident, res)
	case scope.DimFile:
		generateDirReport(&b, last.Ident, res, folded)
	case scope.DimRepo:
		target := res.TargetRepo()
		if target == nil && len(res.Repos) > 0 {
			target = res.Repos[0]
		}
		generateRepoReport(&b, target, res)
	default:
		generateGenericScopeReport(&b, res)
	}

	_, err := io.WriteString(w, b.String())
	return fileName, err
}

func generateGroupReport(b *strings.Builder, groupName string, res *scope.Resolved) {
	b.WriteString(fmt.Sprintf("# 分组专项报告: 【%s】 (Group Scope)\n\n", groupName))
	b.WriteString(fmt.Sprintf("> 作用域: `group:%s` · 包含仓库: **%d** 个 · 生成时间: %s\n\n",
		groupName, len(res.Repos), time.Now().Format("2006-01-02 15:04")))

	// 统计指标
	dirty, ahead, behind, noRemote := 0, 0, 0, 0
	for _, r := range res.Repos {
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
	}

	b.WriteString("## 1. 组内健康度概览\n\n")
	b.WriteString("| 指标项 | 数量 | 状态评估 |\n|---|:---:|---|\n")
	b.WriteString(fmt.Sprintf("| 仓库总数 | **%d** | 当前分组纳入的仓库总数 |\n", len(res.Repos)))
	b.WriteString(fmt.Sprintf("| 存在改动 (Dirty) | %s | %s |\n", renderNum(dirty), riskText(dirty, "有未暂存/未跟踪代码")))
	b.WriteString(fmt.Sprintf("| 本地领先 (Ahead) | %s | %s |\n", renderNum(ahead), riskText(ahead, "存在未推送 commit")))
	b.WriteString(fmt.Sprintf("| 落后上游 (Behind) | %s | %s |\n", renderNum(behind), riskText(behind, "上游存在新提交")))
	b.WriteString(fmt.Sprintf("| 纯本地无远端 | %s | %s |\n", renderNum(noRemote), riskText(noRemote, "无远端同步")))
	b.WriteString("\n")

	b.WriteString("## 2. 组内仓库组织明细表\n\n")
	b.WriteString("| 仓库名称 | 分支 | 改动 | 同步 | 最近提交 | 作者 | 标签 | 备注说明 | 物理路径 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")

	sorted := append([]*state.Repo{}, res.Repos...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, r := range sorted {
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s | %s | `%s` |\n",
			escapeCell(r.Name),
			escapeCell(render.BranchLabel(r)),
			render.ChangeLabel(r),
			render.SyncLabel(r),
			render.CommitAge(r),
			escapeCell(orDash(r.Author)),
			escapeCell(orDash(render.TagsLabel(r))),
			escapeCell(orDash(r.Note)),
			filepath.ToSlash(r.Path),
		))
	}
	b.WriteString("\n")
}

func generateTagReport(b *strings.Builder, tagName string, res *scope.Resolved) {
	b.WriteString(fmt.Sprintf("# 标签专项报告: 【#%s】 (Tag Scope)\n\n", tagName))
	b.WriteString(fmt.Sprintf("> 作用域: `tag:%s` · 关联仓库: **%d** 个 · 生成时间: %s\n\n",
		tagName, len(res.Repos), time.Now().Format("2006-01-02 15:04")))

	b.WriteString("## 1. 标签关联仓库明细\n\n")
	b.WriteString("| 仓库名称 | 所属分组 | 分支 | 改动 | 同步 | 最近提交 | 路径 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")

	sorted := append([]*state.Repo{}, res.Repos...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, r := range sorted {
		grp := r.Group
		if grp == "" {
			grp = "(未分组)"
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | `%s` |\n",
			escapeCell(r.Name),
			escapeCell(grp),
			escapeCell(render.BranchLabel(r)),
			render.ChangeLabel(r),
			render.SyncLabel(r),
			render.CommitAge(r),
			filepath.ToSlash(r.Path),
		))
	}
	b.WriteString("\n")
}

func generateDirReport(b *strings.Builder, dirPath string, res *scope.Resolved, folded []string) {
	cleanDir := filepath.ToSlash(dirPath)
	b.WriteString(fmt.Sprintf("# 目录层级报告: 📁 `%s` (Dir Scope)\n\n", cleanDir))
	b.WriteString(fmt.Sprintf("> 作用域: `file:%s` · 包含仓库: **%d** 个 · 生成时间: %s\n\n",
		cleanDir, len(res.Repos), time.Now().Format("2006-01-02 15:04")))

	// 1. 本层级子项（Children）
	b.WriteString("## 1. 本层级导航结构 (Current Level Children)\n\n")
	if len(res.Children) == 0 {
		b.WriteString("*(当前目录下没有子层级或子仓库)*\n\n")
	} else {
		b.WriteString("| 类型 | 名称 / 标识 | 数量 / 摘要 | 直达命令 |\n|---|---|---|---|\n")
		for _, child := range res.Children {
			targetCmd := fmt.Sprintf("`igit cd \"%s\"`", child.Target)
			facetCN := child.Facet
			switch child.Facet {
			case scope.DimFile:
				facetCN = "📁 目录"
			case scope.DimRepo:
				facetCN = "📦 仓库"
			}
			summary := child.Summary
			if summary == "" && child.Count > 0 {
				summary = fmt.Sprintf("包含 %d 个仓库", child.Count)
			}
			b.WriteString(fmt.Sprintf("| %s | `%s` | %s | %s |\n",
				facetCN,
				escapeCell(child.Label),
				escapeCell(orDash(summary)),
				targetCmd,
			))
		}
		b.WriteString("\n")
	}

	// 2. 局部子树骨架
	b.WriteString("## 2. 局部目录树 (Subtree)\n\n")
	b.WriteString("```\n")
	nodes := tree.Build(res.Repos)
	lines := tree.Render(nodes, tree.Opt{Depth: 4, Brief: true, Folded: folded})
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("```\n\n")

	// 3. 本目录下的仓库明细
	b.WriteString("## 3. 本目录下仓库汇总\n\n")
	b.WriteString("| 仓库名称 | 分支 | 改动 | 同步 | 分组 | 标签 | 状态 | 相对路径 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range res.Repos {
		rel := strings.TrimPrefix(filepath.ToSlash(r.Path), cleanDir)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "."
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s | `%s` |\n",
			escapeCell(r.Name),
			escapeCell(render.BranchLabel(r)),
			render.ChangeLabel(r),
			render.SyncLabel(r),
			escapeCell(orDash(r.Group)),
			escapeCell(orDash(render.TagsLabel(r))),
			render.StatusCN(r),
			escapeCell(rel),
		))
	}
	b.WriteString("\n")
}

func generateRepoReport(b *strings.Builder, r *state.Repo, res *scope.Resolved) {
	if r == nil {
		b.WriteString("# 单仓报告: (未找到指定仓库)\n\n")
		return
	}
	b.WriteString(fmt.Sprintf("# 仓库专项档案: `%s` (Repo Scope)\n\n", r.Name))
	b.WriteString(fmt.Sprintf("> 作用域: `repo:%s` · 物理路径: `%s` · 生成时间: %s\n\n",
		r.Name, filepath.ToSlash(r.Path), time.Now().Format("2006-01-02 15:04")))

	// 1. 基础属性与组织信息
	b.WriteString("## 1. 基础属性与组织信息\n\n")
	b.WriteString("| 属性 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| **仓库名称** | `%s` |\n", r.Name))
	b.WriteString(fmt.Sprintf("| **物理路径** | `%s` |\n", filepath.ToSlash(r.Path)))
	b.WriteString(fmt.Sprintf("| **仓库类型** | %s (`%s`) |\n", render.KindCN(r), r.Kind))
	b.WriteString(fmt.Sprintf("| **当前分支** | `%s` (分离HEAD: %v) |\n", r.Branch, r.Detached))
	b.WriteString(fmt.Sprintf("| **所属分组** | %s |\n", orDash(r.Group)))
	b.WriteString(fmt.Sprintf("| **关联标签** | %s |\n", orDash(render.TagsLabel(r))))
	b.WriteString(fmt.Sprintf("| **治理状态** | %s |\n", render.StatusCN(r)))
	b.WriteString(fmt.Sprintf("| **人工备注** | %s |\n", orDash(r.Note)))
	b.WriteString(fmt.Sprintf("| **初次扫描** | %s |\n", humanTime(r.FirstSeen)))
	b.WriteString(fmt.Sprintf("| **最近更新** | %s |\n", humanTime(r.LastSeen)))
	b.WriteString("\n")

	// 2. 工作区与上游同步状态
	b.WriteString("## 2. 工作区改动与同步状态\n\n")
	b.WriteString("| 状态维度 | 数值 / 表现 | 说明 |\n|---|:---:|---|\n")
	b.WriteString(fmt.Sprintf("| **工作区 Dirty** | %v | 是否有未提交改动 |\n", r.Dirty))
	b.WriteString(fmt.Sprintf("| 暂存文件 (Staged) | %d | `git add` 后的文件数 |\n", r.Staged))
	b.WriteString(fmt.Sprintf("| 未暂存改动 (Unstaged) | %d | 修改但未暂存的文件数 |\n", r.Unstaged))
	b.WriteString(fmt.Sprintf("| 未跟踪文件 (Untracked) | %d | 全新未跟踪的文件数 |\n", r.Untracked))
	b.WriteString(fmt.Sprintf("| 领先上游 (Ahead) | %d | 尚未 push 到 remote 的提交数 |\n", r.Ahead))
	b.WriteString(fmt.Sprintf("| 落后上游 (Behind) | %d | 尚未 pull 到本地的提交数 |\n", r.Behind))
	b.WriteString("\n")

	// 3. 远程源信息
	b.WriteString("## 3. 远程源配置 (Remotes)\n\n")
	if len(r.Remotes) == 0 {
		b.WriteString("*(纯本地仓库，未配置任何远端 remote 镜像)*\n\n")
	} else {
		b.WriteString("| Remote 名称 | 仓库 URL 映射 |\n|---|---|\n")
		for name, url := range r.Remotes {
			b.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", name, url))
		}
		b.WriteString("\n")
	}

	// 4. 最近一次提交记录
	b.WriteString("## 4. HEAD 最近提交快照\n\n")
	b.WriteString(fmt.Sprintf("- **Commit**: `%s`\n", r.Commit))
	b.WriteString(fmt.Sprintf("- **作者**: %s\n", orDash(r.Author)))
	b.WriteString(fmt.Sprintf("- **提交时间**: %s (%s)\n", humanTime(r.CommitAt), render.CommitAge(r)))
	b.WriteString(fmt.Sprintf("- **说明 (Subject)**: %s\n\n", orDash(r.Subject)))
}

func generateGenericScopeReport(b *strings.Builder, res *scope.Resolved) {
	b.WriteString(fmt.Sprintf("# 作用域报告: %s\n\n", res.Scope.String()))
	b.WriteString(fmt.Sprintf("> 仓库数量: **%d** · 生成时间: %s\n\n", len(res.Repos), time.Now().Format("2006-01-02 15:04")))
	b.WriteString("| 仓库名称 | 分支 | 改动 | 路径 |\n|---|---|---|---|\n")
	for _, r := range res.Repos {
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | `%s` |\n",
			r.Name, render.BranchLabel(r), render.ChangeLabel(r), filepath.ToSlash(r.Path)))
	}
	b.WriteString("\n")
}

func generateRootReport(b *strings.Builder, repos []*state.Repo, folded []string, w io.Writer) error {
	b.WriteString("# 全局执行摘要 (Root Scope Summary)\n\n")
	b.WriteString(fmt.Sprintf("> 当前位于根作用域 (/) · 全量管理仓库: **%d** 个\n\n", len(repos)))
	b.WriteString("当前作用域即为全局视图。详细全量分析请参阅同目录下的：\n\n")
	b.WriteString("- `00_orphans_todo.md`: 游离仓库待整理地图 (依目录聚合)\n")
	b.WriteString("- `01_global_summary.md`: 全局资产与健康统计总览\n")
	b.WriteString("- `02_global_tree.md`: 全局物理目录树与路径清单\n")
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func sanitizeIdent(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func riskText(count int, risk string) string {
	if count > 0 {
		return fmt.Sprintf("⚠️ %s", risk)
	}
	return "正常"
}
