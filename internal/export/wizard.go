package export

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OpenDirectory 跨平台在系统资源管理器/访达中打开文件夹
func OpenDirectory(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", abs)
	case "darwin":
		cmd = exec.Command("open", abs)
	default:
		cmd = exec.Command("xdg-open", abs)
	}
	return cmd.Start()
}

// Execute 执行批量导出
func Execute(ctx *Context, opts Options) (*Result, error) {
	batchName := fmt.Sprintf("export_%s", time.Now().Format("2006-01-02_150405"))
	batchDir := filepath.Join(opts.OutputDir, batchName)
	if err := os.MkdirAll(batchDir, 0755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %w", err)
	}

	res := &Result{
		BatchDir:   batchDir,
		TotalRepos: len(ctx.AllRepos),
	}

	writeFile := func(name string, gen func(*os.File) error) error {
		p := filepath.Join(batchDir, name)
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := gen(f); err != nil {
			return err
		}
		res.Files = append(res.Files, p)
		return nil
	}

	// 1. 游离仓库待整理地图
	if opts.ExportOrphans {
		if err := writeFile("00_orphans_todo.md", func(f *os.File) error {
			return GenerateOrphansTodo(f, ctx.AllRepos, ctx.State)
		}); err != nil {
			return nil, err
		}
	}

	// 2. 全局概览与健康统计
	if opts.ExportSummary {
		if err := writeFile("01_global_summary.md", func(f *os.File) error {
			return GenerateGlobalSummary(f, ctx.AllRepos, ctx.State)
		}); err != nil {
			return nil, err
		}
	}

	// 3. 全局物理目录树
	if opts.ExportTree {
		if err := writeFile("02_global_tree.md", func(f *os.File) error {
			return GenerateGlobalTree(f, ctx.AllRepos, ctx.Folded)
		}); err != nil {
			return nil, err
		}
	}

	// 4. 当前作用域专项报告
	if opts.ExportScope {
		scopeFile := ScopeFileName(ctx.ScopeRes)
		if err := writeFile(scopeFile, func(f *os.File) error {
			_, err := GenerateScopeReport(f, ctx.ScopeRes, ctx.AllRepos, ctx.Folded)
			return err
		}); err != nil {
			return nil, err
		}
	}

	// 5. CSV 表格
	if opts.ExportCSV {
		if err := writeFile("repos.csv", func(f *os.File) error {
			return CSV(f, ctx.AllRepos)
		}); err != nil {
			return nil, err
		}
	}

	// 6. JSON 机器可读
	if opts.ExportJSON {
		if err := writeFile("repos.json", func(f *os.File) error {
			return JSON(f, ctx.AllRepos)
		}); err != nil {
			return nil, err
		}
	}

	// 7. 自动打开
	if opts.AutoOpen {
		_ = OpenDirectory(batchDir)
	}

	return res, nil
}

// ---------------------------------------------------------------- 交互式向导模型 (BubbleTea)

type wizardItem struct {
	ID      string
	Title   string
	File    string
	Desc    string
	Checked bool
}

type wizardModel struct {
	ctx       *Context
	items     []wizardItem
	autoOpen  bool
	cursor    int
	confirmed bool
	cancelled bool
	outputDir string
	scopeDesc string
	quitting  bool
}

func initialWizardModel(ctx *Context, defaultOutDir string) wizardModel {
	scopeName := "全局根 (Root /)"
	scopeFile := ScopeFileName(ctx.ScopeRes)
	if ctx.ScopeRes != nil && !ctx.ScopeRes.Scope.IsRoot() {
		scopeName = ctx.ScopeRes.Scope.String()
	}

	autoOpen := true
	if ctx.Config != nil {
		autoOpen = ctx.Config.ShouldAutoOpenExport()
	}

	items := []wizardItem{
		{
			ID:      "orphans",
			Title:   "游离仓库待整理地图",
			File:    "00_orphans_todo.md",
			Desc:    "依物理父目录聚合，评估改动风险，指导下一步去哪整理",
			Checked: true,
		},
		{
			ID:      "summary",
			Title:   "全局概览与健康统计",
			File:    "01_global_summary.md",
			Desc:    "健康统计(是否脏/超前/落后/无远程/缺失)、Group/Tag 矩阵",
			Checked: true,
		},
		{
			ID:      "tree",
			Title:   "全局物理目录树",
			File:    "02_global_tree.md",
			Desc:    "全局物理层级树状骨架与完整路径清单",
			Checked: true,
		},
		{
			ID:      "scope",
			Title:   "当前作用域专项报告",
			File:    scopeFile,
			Desc:    "根据当前所在上下文(Group/Tag/Dir/Repo)特化的深度报告",
			Checked: true,
		},
		{
			ID:      "csv",
			Title:   "全量资产表格 (CSV)",
			File:    "repos.csv",
			Desc:    "带 UTF-8 BOM，Excel 双击即看无乱码",
			Checked: false,
		},
		{
			ID:      "json",
			Title:   "全量完整数据 (JSON)",
			File:    "repos.json",
			Desc:    "完整机器可读数据，供自动化脚本或备份消费",
			Checked: false,
		},
	}

	return wizardModel{
		ctx:       ctx,
		items:     items,
		autoOpen:  autoOpen,
		cursor:    0,
		outputDir: defaultOutDir,
		scopeDesc: scopeName,
	}
}

func (m wizardModel) Init() tea.Cmd {
	return nil
}

func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(m.items) // 最后一个是指向 autoOpen 开关
			}
		case "down", "j":
			if m.cursor < len(m.items) {
				m.cursor++
			} else {
				m.cursor = 0
			}
		case " ":
			if m.cursor < len(m.items) {
				m.items[m.cursor].Checked = !m.items[m.cursor].Checked
			} else {
				m.autoOpen = !m.autoOpen
			}
		case "o", "O":
			m.autoOpen = !m.autoOpen
		case "a", "A":
			// 全选 / 全不选
			allChecked := true
			for _, it := range m.items {
				if !it.Checked {
					allChecked = false
					break
				}
			}
			for i := range m.items {
				m.items[i].Checked = !allChecked
			}
		case "enter":
			m.confirmed = true
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m wizardModel) View() string {
	if m.quitting {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	subStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(1, 2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("📦 igit 数据资产导出向导") + "\n")
	b.WriteString(subStyle.Render(fmt.Sprintf("当前作用域: %s · 目标目录: %s", m.scopeDesc, m.outputDir)) + "\n\n")

	for i, it := range m.items {
		cursor := "  "
		if m.cursor == i {
			cursor = cursorStyle.Render("▸ ")
		}

		check := "[ ]"
		if it.Checked {
			check = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("[✓]")
		}

		titleText := it.Title
		if m.cursor == i {
			titleText = cursorStyle.Render(it.Title)
		}

		b.WriteString(fmt.Sprintf("%s%s %s %s\n", cursor, check, titleText, fileStyle.Render("("+it.File+")")))
		b.WriteString(fmt.Sprintf("      %s\n", dimStyle.Render(it.Desc)))
	}

	// 自动打开项
	cursor := "  "
	if m.cursor == len(m.items) {
		cursor = cursorStyle.Render("▸ ")
	}
	openCheck := "[ ]"
	if m.autoOpen {
		openCheck = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("[✓]")
	}
	openLabel := "导出完成后自动打开文件夹 (Auto-Open)"
	if m.cursor == len(m.items) {
		openLabel = cursorStyle.Render(openLabel)
	}
	b.WriteString("\n" + fmt.Sprintf("%s%s %s\n", cursor, openCheck, openLabel))

	// 操作帮助
	help := subStyle.Render("\n[↑/↓ 或 j/k] 移动  [空格] 勾选/取消  [a] 全选/全不选  [Enter] 确认导出  [Esc/q] 取消")
	b.WriteString(help)

	return boxStyle.Render(b.String()) + "\n"
}

// RunWizard 启动终端交互向导，返回执行结果
func RunWizard(ctx *Context, defaultOutDir string) (*Result, error) {
	model := initialWizardModel(ctx, defaultOutDir)
	p := tea.NewProgram(model)
	m, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("向导执行失败: %w", err)
	}

	finalModel := m.(wizardModel)
	if finalModel.cancelled || !finalModel.confirmed {
		return nil, nil // 用户取消
	}

	opts := Options{
		OutputDir: defaultOutDir,
		AutoOpen:  finalModel.autoOpen,
	}

	for _, it := range finalModel.items {
		if !it.Checked {
			continue
		}
		switch it.ID {
		case "orphans":
			opts.ExportOrphans = true
		case "summary":
			opts.ExportSummary = true
		case "tree":
			opts.ExportTree = true
		case "scope":
			opts.ExportScope = true
		case "csv":
			opts.ExportCSV = true
		case "json":
			opts.ExportJSON = true
		}
	}

	// 执行导出
	return Execute(ctx, opts)
}
