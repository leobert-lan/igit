package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grscan/internal/util"
)

const CurrentVersion = 1

// 处理状态。空值等同于 new。
const (
	StatusNew      = "new"
	StatusActive   = "active"
	StatusDone     = "done"
	StatusArchived = "archived"
)

var StatusList = []string{StatusNew, StatusActive, StatusDone, StatusArchived}

// Repo 一条仓库记录。成段划分：
// 上半部分是扫描出来的客观事实（每次扫描会覆盖），
// 下半部分是人工处理的结论（扫描不会动，只能通过 set/triage 改）。
type Repo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind"` // repo | bare | worktree | submodule

	Bare        bool              `json:"bare"`
	Branch      string            `json:"branch,omitempty"`
	Detached    bool              `json:"detached,omitempty"`
	Remotes     map[string]string `json:"remotes,omitempty"`
	PrimaryURL  string            `json:"primary_url,omitempty"`
	Commit      string            `json:"commit,omitempty"`
	CommitAt    string            `json:"commit_at,omitempty"`
	Author      string            `json:"author,omitempty"`
	Subject     string            `json:"subject,omitempty"`
	Dirty       bool              `json:"dirty"`
	Staged      int               `json:"staged,omitempty"`
	Unstaged    int               `json:"unstaged,omitempty"`
	Untracked   int               `json:"untracked,omitempty"`
	Ahead       int               `json:"ahead,omitempty"`
	Behind      int               `json:"behind,omitempty"`
	HasUpstream bool              `json:"has_upstream,omitempty"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	DiskUsageAt string            `json:"disk_usage_at,omitempty"`
	ScanIssue   string            `json:"scan_issue,omitempty"`

	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Missing   bool   `json:"missing,omitempty"`

	Group      string   `json:"group,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Status     string   `json:"status,omitempty"`
	Ignored    bool     `json:"ignored,omitempty"`
	Note       string   `json:"note,omitempty"`
	ReviewedAt string   `json:"reviewed_at,omitempty"`
}

// ScanRecord 每次扫描留一条流水，方便回头看历史。
type ScanRecord struct {
	At         string   `json:"at"`
	Roots      []string `json:"roots"`
	Found      int      `json:"found"`
	New        int      `json:"new"`
	Missing    int      `json:"missing"`
	SkippedIgn int      `json:"skipped_ignored,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	Dirs       int      `json:"dirs_visited,omitempty"`
	Fast       bool     `json:"fast,omitempty"`
}

// DirRule 目录级规则。Path 是目录前缀，命中即整棵生效。
type DirRule struct {
	Path string `json:"path"`
	At   string `json:"at"`
	Note string `json:"note,omitempty"`
}

type State struct {
	Version   int          `json:"version"`
	UpdatedAt string       `json:"updated_at"`
	Repos     []*Repo      `json:"repos"`
	Scans     []ScanRecord `json:"scans"`

	// Hidden：彻底看不见。扫描时整棵跳过，结果里完全不出现。
	Hidden []DirRule `json:"hidden,omitempty"`
	// Folded：折叠。知道这里有一堆仓库，但不展开细节，只显示汇总。
	Folded []DirRule `json:"folded,omitempty"`

	path string `json:"-"`
}

// ---------------------------------------------------------------- 目录级规则

func normalizeDirRule(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}

func (s *State) addRule(list *[]DirRule, raw, note string) (string, bool) {
	dir := normalizeDirRule(raw)
	for _, r := range *list {
		if strings.EqualFold(r.Path, dir) {
			return dir, false
		}
	}
	*list = append(*list, DirRule{Path: dir, At: time.Now().Format(time.RFC3339), Note: note})
	return dir, true
}

func (s *State) removeRule(list *[]DirRule, raw string) (string, bool) {
	dir := normalizeDirRule(raw)
	kept := make([]DirRule, 0, len(*list))
	removed := false
	for _, r := range *list {
		if strings.EqualFold(r.Path, dir) {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	*list = kept
	return dir, removed
}

func (s *State) Hide(raw, note string) (string, bool) { return s.addRule(&s.Hidden, raw, note) }
func (s *State) Unhide(raw string) (string, bool)     { return s.removeRule(&s.Hidden, raw) }
func (s *State) Fold(raw, note string) (string, bool) { return s.addRule(&s.Folded, raw, note) }
func (s *State) Unfold(raw string) (string, bool)     { return s.removeRule(&s.Folded, raw) }

// HiddenPaths 返回所有被隐藏的目录前缀（供扫描与过滤使用）。
func (s *State) HiddenPaths() []string {
	out := make([]string, 0, len(s.Hidden))
	for _, r := range s.Hidden {
		out = append(out, r.Path)
	}
	return out
}

func (s *State) FoldedPaths() []string {
	out := make([]string, 0, len(s.Folded))
	for _, r := range s.Folded {
		out = append(out, r.Path)
	}
	return out
}

// IsHidden 判断仓库是否落在被隐藏的目录下。
func (s *State) IsHidden(p string) bool {
	return underAnyRoot(p, s.HiddenPaths())
}

// IsFolded 返回命中的折叠目录，没有则返回空串。
func (s *State) IsFolded(p string) string {
	for _, root := range s.FoldedPaths() {
		if util.HasPrefixPath(p, root) {
			return root
		}
	}
	return ""
}

// Load 读取状态文件，不存在返回空状态。
func Load(statePath string) (*State, error) {
	s := &State{Version: CurrentVersion, Repos: []*Repo{}, Scans: []ScanRecord{}, path: statePath}
	b, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var parsed State
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, err
	}
	if parsed.Repos == nil {
		parsed.Repos = []*Repo{}
	}
	if parsed.Version == 0 {
		parsed.Version = CurrentVersion
	}
	parsed.path = statePath
	return &parsed, nil
}

// Save 原子写盘，按 path 排序保证 diff 友好。
func (s *State) Save() error {
	s.Version = CurrentVersion
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	sort.Slice(s.Repos, func(i, j int) bool { return s.Repos[i].ID < s.Repos[j].ID })
	p := s.path
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *State) Path() string { return s.path }

func (s *State) index(id string) int {
	for i, r := range s.Repos {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// Upsert 合并一批扫描结果：事实部分整体覆盖，人工结论原样保留。
// roots 是本次实际扫过的根目录——只有落在这些目录下的旧记录才会被判定为丢失，
// 避免单次只扫一个盘时把别的盘的仓库误标成 missing。
// ignoredIDs 同理：主动忽略的仓库在扫描时会被整棵跳过，不能算作丢失。
// 返回新增数量。
func (s *State) Upsert(found []*Repo, seen map[string]bool, roots, ignoredIDs []string) (added int) {
	now := time.Now().Format(time.RFC3339)
	existing := map[string]*Repo{}
	for _, r := range s.Repos {
		existing[r.ID] = r
	}
	for _, f := range found {
		if prev, ok := existing[f.ID]; ok {
			f.Group = prev.Group
			f.Tags = prev.Tags
			f.Status = prev.Status
			f.Ignored = prev.Ignored
			f.Note = prev.Note
			f.ReviewedAt = prev.ReviewedAt
			f.FirstSeen = prev.FirstSeen
			if prev.SizeBytes > 0 && f.SizeBytes == 0 {
				f.SizeBytes = prev.SizeBytes
				f.DiskUsageAt = prev.DiskUsageAt
			}
			*prev = *f
		} else {
			f.FirstSeen = now
			f.Status = StatusNew
			s.Repos = append(s.Repos, f)
			added++
		}
	}
	// 本次没扫到的：只把落在扫描范围内的旧记录标记为丢失，等人工确认后再 prune
	for _, r := range s.Repos {
		if seen[r.ID] {
			r.LastSeen = now
			r.Missing = false
			continue
		}
		if len(roots) == 0 || underAnyRoot(r.Path, roots) {
			if r.Ignored || underAnyRoot(r.Path, ignoredIDs) {
				r.Missing = false // 历史遗留的标记也要清掉，别让已归档的仓库反复报警
				continue
			}
			r.Missing = true
		}
	}
	return added
}

func underAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if util.HasPrefixPath(path, root) {
			return true
		}
	}
	return false
}

func (s *State) CountMissing() int {
	n := 0
	for _, r := range s.Repos {
		if r.Missing {
			n++
		}
	}
	return n
}

// Find 支持三种定位方式：完整 ID / ID 或名字的片段 / 精确名字。
// 不做 `%1` 这类序号，因为 list 输出本身不稳定，容易误伤。
func (s *State) Find(q string) []*Repo {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	lower := strings.ToLower(strings.ReplaceAll(q, `\`, "/"))
	var exact, partial []*Repo
	for _, r := range s.Repos {
		if strings.EqualFold(r.ID, lower) || strings.EqualFold(r.Name, q) {
			exact = append(exact, r)
			continue
		}
		if strings.Contains(r.ID, lower) {
			partial = append(partial, r)
			continue
		}
		if strings.Contains(strings.ToLower(r.Path), lower) {
			partial = append(partial, r)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func (s *State) Remove(id string) bool {
	i := s.index(id)
	if i < 0 {
		return false
	}
	s.Repos = append(s.Repos[:i], s.Repos[i+1:]...)
	return true
}

// PruneMissing 删掉磁盘上已不存在、且不是 ignored 的记录。
func (s *State) PruneMissing() []*Repo {
	kept := make([]*Repo, 0, len(s.Repos))
	var dropped []*Repo
	for _, r := range s.Repos {
		if r.Missing && !r.Ignored {
			dropped = append(dropped, r)
			continue
		}
		kept = append(kept, r)
	}
	s.Repos = kept
	return dropped
}

func (s *State) SortBy(f SortField, desc bool) {
	less := func(a, b *Repo) bool {
		switch f {
		case SortCommit:
			return pick(a.CommitAt, a.LastSeen) > pick(b.CommitAt, b.LastSeen)
		case SortName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case SortGroup:
			if a.Group == b.Group {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.Group < b.Group
		case SortStatus:
			if a.Status == b.Status {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.Status < b.Status
		case SortSize:
			return a.SizeBytes > b.SizeBytes
		default: // SortPath
			return a.ID < b.ID
		}
	}
	sort.SliceStable(s.Repos, func(i, j int) bool {
		if desc {
			return !less(s.Repos[i], s.Repos[j])
		}
		return less(s.Repos[i], s.Repos[j])
	})
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type SortField string

const (
	SortPath   SortField = "path"
	SortCommit SortField = "commit"
	SortName   SortField = "name"
	SortGroup  SortField = "group"
	SortStatus SortField = "status"
	SortSize   SortField = "size"
)

func ParseSortField(s string) SortField {
	switch strings.ToLower(s) {
	case "commit", "date", "last":
		return SortCommit
	case "name":
		return SortName
	case "group":
		return SortGroup
	case "status":
		return SortStatus
	case "size":
		return SortSize
	default:
		return SortPath
	}
}

// NextStatus 给 TUI 里按 s 键循环状态用。
func NextStatus(cur string) string {
	for i, st := range StatusList {
		if st == cur {
			return StatusList[(i+1)%len(StatusList)]
		}
	}
	return StatusActive
}

func Canonicalize(r *Repo) {
	if r.ID == "" {
		r.ID = util.NormalizePath(r.Path)
	}
}
