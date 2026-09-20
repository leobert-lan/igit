package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"grscan/internal/gitinfo"
	"grscan/internal/state"
	"grscan/internal/util"
)

type Options struct {
	Roots        []string
	ExcludeNames []string
	ExcludePaths []string
	SkipPrefixes []string // 被隐藏的目录：整棵跳过，连一次 ReadDir 都不做
	IgnoredIDs   map[string]bool
	MaxDepth     int
	Jobs         int
	Fast         bool
	Deep         bool
	Size         bool
	GitExe       string
	Progress     func(visited int, found int)
}

type Result struct {
	Repos         []*state.Repo
	Dirs          int
	SkippedIgnore int
	Duration      time.Duration
}

type walker struct {
	opts   Options
	found  chan candidate
	seen   map[string]bool
	mu     sync.Mutex
	dirs   int64
	emitN  int64
	skipIg int64
	stop   chan struct{}
}

type candidate struct {
	path string
	kind string
}

// Run 扫描所有根目录，返回发现的仓库列表。
func Run(opts Options) (*Result, error) {
	if opts.Jobs <= 0 {
		opts.Jobs = 8
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}
	if opts.IgnoredIDs == nil {
		opts.IgnoredIDs = map[string]bool{}
	}
	start := time.Now()

	w := &walker{
		opts:  opts,
		found: make(chan candidate, 256),
		seen:  map[string]bool{},
		stop:  make(chan struct{}),
	}

	var wg sync.WaitGroup
	var resultsMu sync.Mutex
	repos := make([]*state.Repo, 0, 64)

	for i := 0; i < opts.Jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range w.found {
				r := gitinfo.Inspect(c.path, c.kind, opts.GitExe, !opts.Fast)
				if opts.Size {
					r.SizeBytes = gitinfo.DirSize(c.path)
					r.DiskUsageAt = time.Now().Format(time.RFC3339)
				}
				resultsMu.Lock()
				repos = append(repos, r)
				resultsMu.Unlock()
			}
		}()
	}

	// 生产者：单协程 DFS，IO 命中的部分基本被 worker 掩盖
	go func() {
		defer close(w.found)
		for _, root := range opts.Roots {
			root = filepath.Clean(root)
			if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
				continue
			}
			w.visit(root, 0)
		}
	}()

	// 进度打点
	ticker := time.NewTicker(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ticker.C:
				if opts.Progress != nil {
					opts.Progress(int(atomic.LoadInt64(&w.dirs)), int(atomic.LoadInt64(&w.emitN)))
				}
			case <-w.stop:
				return
			}
		}
	}()

	wg.Wait() // 生产者结束时会 close(found)，worker 随之退出

	close(w.stop)
	<-done
	ticker.Stop()

	sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })

	return &Result{
		Repos:         repos,
		Dirs:          int(atomic.LoadInt64(&w.dirs)),
		SkippedIgnore: int(atomic.LoadInt64(&w.skipIg)),
		Duration:      time.Since(start),
	}, nil
}

func (w *walker) visit(dir string, depth int) {
	// 被隐藏的目录：进门之前就掉头，成本为零
	if util.HasAnyPrefix(dir, w.opts.SkipPrefixes) {
		return
	}
	entries, err := os.ReadDir(util.LongPath(dir))
	atomic.AddInt64(&w.dirs, 1)
	if err != nil {
		return
	}

	id := util.NormalizePath(dir)

	hasDotGit := false
	head, objects, refs := false, false, false
	for _, e := range entries {
		switch e.Name() {
		case ".git":
			hasDotGit = true
		case "HEAD":
			head = true
		case "objects":
			objects = e.IsDir()
		case "refs":
			refs = e.IsDir()
		}
	}

	isBare := head && objects && refs

	if hasDotGit || isBare {
		if w.opts.IgnoredIDs[id] {
			atomic.AddInt64(&w.skipIg, 1)
			return // 已归档/忽略的仓库不再下钻、不再读取
		}
		kind := gitinfo.KindRepo
		switch {
		case isBare:
			kind = gitinfo.KindBare
		case linkedWorktree(dir):
			kind = gitinfo.KindWorktree
		case isSubmodule(dir):
			kind = gitinfo.KindSubmodule
		}
		w.emit(dir, kind)
		if !w.opts.Deep {
			return
		}
	}

	if depth >= w.opts.MaxDepth {
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			continue // junction / 符号链接，避免重复计数和环路
		}
		name := e.Name()
		if name == ".git" {
			continue // 永远不要钻进 .git 内部，否则会被当成裸仓重复记录
		}
		if util.MatchName(name, w.opts.ExcludeNames) {
			continue
		}
		child := filepath.Join(dir, name)
		if w.excludedPath(child) {
			continue
		}
		w.visit(child, depth+1)
	}
}

func (w *walker) excludedPath(p string) bool {
	for _, prefix := range w.opts.ExcludePaths {
		if util.HasPrefixPath(p, prefix) {
			return true
		}
	}
	return false
}

func (w *walker) emit(path, kind string) {
	id := util.NormalizePath(path)
	w.mu.Lock()
	if w.seen[id] {
		w.mu.Unlock()
		return
	}
	w.seen[id] = true
	w.mu.Unlock()
	atomic.AddInt64(&w.emitN, 1)
	w.found <- candidate{path: path, kind: kind}
}

func linkedWorktree(dir string) bool {
	st, err := os.Lstat(util.LongPath(filepath.Join(dir, ".git")))
	if err != nil || st.IsDir() {
		return false
	}
	b, err := os.ReadFile(util.LongPath(filepath.Join(dir, ".git")))
	if err != nil {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(string(b)))
	return strings.Contains(s, "worktrees")
}

func isSubmodule(dir string) bool {
	st, err := os.Lstat(util.LongPath(filepath.Join(dir, ".git")))
	if err != nil || st.IsDir() {
		return false
	}
	return true
}
