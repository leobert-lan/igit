package export

import (
	"grscan/internal/config"
	"grscan/internal/scope"
	"grscan/internal/state"
)

// Context 封装导出的上下文信息
type Context struct {
	Home     string          // igit home 数据目录
	Config   *config.Config  // 全局配置
	State    *state.State    // 完整状态
	AllRepos []*state.Repo   // 所有已知仓库
	ScopeRes *scope.Resolved // 当前作用域解析结果（nil 表示根）
	Folded   []string        // 折叠目录
}

// Options 导出配置选项
type Options struct {
	OutputDir     string // 输出目标目录
	ExportOrphans bool   // 00_orphans_todo.md（游离仓库待整理地图）
	ExportSummary bool   // 01_global_summary.md（全局概览与健康统计）
	ExportTree    bool   // 02_global_tree.md（全局物理目录树）
	ExportScope   bool   // 03_scope_xxx.md（当前作用域专属报告）
	ExportCSV     bool   // repos.csv（全量资产表格）
	ExportJSON    bool   // repos.json（完整数据备份）
	AutoOpen      bool   // 导出完成后是否自动打开目标目录
}

// Result 导出操作结果
type Result struct {
	BatchDir   string   // 本次导出的文件夹
	Files      []string // 产生的文件相对/绝对路径
	TotalRepos int      // 涉及的仓库总数
}
