package export

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grscan/internal/scope"
	"grscan/internal/state"
)

func mockRepos() []*state.Repo {
	now := time.Now().Format(time.RFC3339)
	old := time.Now().AddDate(-2, 0, 0).Format(time.RFC3339)

	return []*state.Repo{
		{
			ID:         "repo-1",
			Name:       "backend-api",
			Path:       "C:/code/corp/backend-api",
			Branch:     "main",
			Group:      "backend",
			Tags:       []string{"go", "core"},
			Dirty:      true,
			Staged:     2,
			Unstaged:   1,
			PrimaryURL: "git@github.com:corp/backend-api.git",
			CommitAt:   now,
			Status:     state.StatusActive,
		},
		{
			ID:         "repo-2",
			Name:       "frontend-web",
			Path:       "C:/code/corp/frontend-web",
			Branch:     "dev",
			Group:      "frontend",
			Tags:       []string{"vue"},
			Ahead:      3,
			PrimaryURL: "git@github.com:corp/frontend-web.git",
			CommitAt:   now,
			Status:     state.StatusDone,
		},
		{
			ID:         "repo-3",
			Name:       "stray-tool-1",
			Path:       "C:/code/temp/stray-tool-1",
			Branch:     "master",
			Group:      "", // 游离仓库
			Tags:       nil,
			Dirty:      true,
			Untracked:  5,
			PrimaryURL: "", // 无远端
			CommitAt:   old,
			Status:     state.StatusNew,
		},
		{
			ID:         "repo-4",
			Name:       "stray-tool-2",
			Path:       "C:/code/temp/stray-tool-2",
			Branch:     "master",
			Group:      "", // 游离仓库
			Tags:       []string{"script"},
			PrimaryURL: "https://github.com/someone/stray.git",
			CommitAt:   now,
			Status:     state.StatusNew,
		},
	}
}

func TestGenerateOrphansTodo(t *testing.T) {
	repos := mockRepos()
	var buf bytes.Buffer
	st := &state.State{Repos: repos}
	err := GenerateOrphansTodo(&buf, repos, st)
	if err != nil {
		t.Fatalf("GenerateOrphansTodo failed: %v", err)
	}
	content := buf.String()

	// 应该包含游离仓库标题
	if !strings.Contains(content, "# 游离仓库待整理地图") {
		t.Errorf("expected title in orphans todo")
	}

	// 应该统计出 2 个游离仓库
	if !strings.Contains(content, "游离待整理: 2") {
		t.Errorf("expected 2 orphans, got: %s", content)
	}

	// 测试 hide 过滤：把 C:/code/temp hide 掉后，游离待办应该直接清零
	st.Hide("C:/code/temp", "不想管理的目录")
	buf.Reset()
	if err := GenerateOrphansTodo(&buf, repos, st); err != nil {
		t.Fatalf("GenerateOrphansTodo with hide failed: %v", err)
	}
	content2 := buf.String()
	if !strings.Contains(content2, "游离待整理: 0") {
		t.Errorf("expected 0 orphans after hide, got: %s", content2)
	}
}

func TestGenerateGlobalSummary(t *testing.T) {
	repos := mockRepos()
	st := &state.State{
		Repos:  repos,
		Groups: []string{"backend", "frontend", "infra"},
	}
	var buf bytes.Buffer
	err := GenerateGlobalSummary(&buf, repos, st)
	if err != nil {
		t.Fatalf("GenerateGlobalSummary failed: %v", err)
	}
	content := buf.String()

	if !strings.Contains(content, "# 全局资产与健康统计总览") {
		t.Errorf("expected summary title")
	}
	if !strings.Contains(content, "工作区改动 (Dirty)") {
		t.Errorf("expected dirty status")
	}
	if !strings.Contains(content, "backend") || !strings.Contains(content, "frontend") {
		t.Errorf("expected groups in matrix")
	}
	if !strings.Contains(content, "#go") || !strings.Contains(content, "#vue") {
		t.Errorf("expected tags in matrix")
	}
}

func TestGenerateGlobalTree(t *testing.T) {
	repos := mockRepos()
	var buf bytes.Buffer
	err := GenerateGlobalTree(&buf, repos, nil)
	if err != nil {
		t.Fatalf("GenerateGlobalTree failed: %v", err)
	}
	content := buf.String()

	if !strings.Contains(content, "# 全局物理目录树与路径清单") {
		t.Errorf("expected tree title")
	}
	if !strings.Contains(content, "backend-api") {
		t.Errorf("expected repo in tree")
	}
	if !strings.Contains(content, "C:/code/corp/backend-api") {
		t.Errorf("expected path mapping")
	}
}

func TestGenerateScopeReport(t *testing.T) {
	repos := mockRepos()

	// 1. Group 作用域
	groupScope := scope.Scope{
		Segs: []scope.Seg{
			{Dim: scope.DimGroup, Ident: "backend", Explicit: true},
		},
	}
	groupRes := &scope.Resolved{
		Scope:    groupScope,
		NodeType: scope.NodeGroup,
		Repos:    repos[:1], // backend-api
	}

	var buf bytes.Buffer
	fileName, err := GenerateScopeReport(&buf, groupRes, repos, nil)
	if err != nil {
		t.Fatalf("GenerateScopeReport group failed: %v", err)
	}
	if fileName != "03_scope_group_backend.md" {
		t.Errorf("unexpected fileName: %s", fileName)
	}
	if !strings.Contains(buf.String(), "分组专项报告: 【backend】") {
		t.Errorf("unexpected group report content: %s", buf.String())
	}

	// 2. Tag 作用域
	tagScope := scope.Scope{
		Segs: []scope.Seg{
			{Dim: scope.DimTag, Ident: "vue", Explicit: true},
		},
	}
	tagRes := &scope.Resolved{
		Scope:    tagScope,
		NodeType: scope.NodeTag,
		Repos:    repos[1:2], // frontend-web
	}
	buf.Reset()
	fileName, err = GenerateScopeReport(&buf, tagRes, repos, nil)
	if err != nil {
		t.Fatalf("GenerateScopeReport tag failed: %v", err)
	}
	if fileName != "03_scope_tag_vue.md" {
		t.Errorf("unexpected fileName: %s", fileName)
	}
	if !strings.Contains(buf.String(), "标签专项报告: 【#vue】") {
		t.Errorf("unexpected tag report content: %s", buf.String())
	}

	// 3. Repo 作用域
	repoScope := scope.Scope{
		Segs: []scope.Seg{
			{Dim: scope.DimRepo, Ident: "backend-api", Explicit: true},
		},
	}
	repoRes := &scope.Resolved{
		Scope:    repoScope,
		NodeType: scope.NodeRepo,
		Repos:    repos[:1],
	}
	buf.Reset()
	fileName, err = GenerateScopeReport(&buf, repoRes, repos, nil)
	if err != nil {
		t.Fatalf("GenerateScopeReport repo failed: %v", err)
	}
	if fileName != "03_scope_repo_backend-api.md" {
		t.Errorf("unexpected fileName: %s", fileName)
	}
	if !strings.Contains(buf.String(), "仓库专项档案: `backend-api`") {
		t.Errorf("unexpected repo report content: %s", buf.String())
	}
}

func TestExecute(t *testing.T) {
	repos := mockRepos()
	tmpDir, err := os.MkdirTemp("", "export_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := &Context{
		Home:     tmpDir,
		AllRepos: repos,
		State:    &state.State{Repos: repos},
	}

	opts := Options{
		OutputDir:     tmpDir,
		ExportOrphans: true,
		ExportSummary: true,
		ExportTree:    true,
		ExportScope:   true,
		ExportCSV:     true,
		ExportJSON:    true,
		AutoOpen:      false, // 测试中不调用外部窗口
	}

	res, err := Execute(ctx, opts)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(res.Files) != 6 {
		t.Errorf("expected 6 files, got %d", len(res.Files))
	}

	// 检查各文件是否确实存在且非空
	for _, f := range res.Files {
		fi, err := os.Stat(f)
		if err != nil {
			t.Errorf("file not found: %s", f)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("file is empty: %s", f)
		}
	}

	// 检查文件名是否包含预期的批次目录和报告文件
	baseNames := map[string]bool{}
	for _, f := range res.Files {
		baseNames[filepath.Base(f)] = true
	}

	expected := []string{
		"00_orphans_todo.md",
		"01_global_summary.md",
		"02_global_tree.md",
		"03_scope_root.md",
		"repos.csv",
		"repos.json",
	}
	for _, exp := range expected {
		if !baseNames[exp] {
			t.Errorf("missing expected file: %s", exp)
		}
	}
}
