// Package pick 回答一个问题：这条命令要作用于哪个仓库。
//
// 这件事以前是每个命令自己做的 —— tig 和 open 直接到全清单里模糊搜，命中一堆就取第一个。
// 结果是作用域退化成提示符上的一句装饰：你明明站在某个目录里，命令却打开别的盘上的仓库。
// 本包把它收成一处，规则只有三条：
//
//  1. 候选池来自当前作用域，池外的仓库不存在；
//  2. 关键字只在池内解析，池内没有就是没有，不回退全集；
//  3. 命中 0 个或多个就停下来列编号候选，绝不静默取第一个。
//
// 第 3 条最要紧：开窗口、删记录这类动作做错了代价太高，宁可多问一句。
package pick

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
	"grscan/internal/scope"
	"grscan/internal/state"
)

// Opt 定宾语要的上下文。
type Opt struct {
	Cmd   string        // 命令名，只进提示文案
	Label string        // 作用域的名字，打给人看
	Pool  []*state.Repo // 作用域圈定的候选；nil 表示没限定，用全集
	All   []*state.Repo // Pool 为 nil 时的全集
	In    io.Reader     // 列完候选后从这儿读编号；nil 表示不交互
	Out   io.Writer
}

// maxList 一次最多列几个候选。全集作用域下可能是几百个，全列出来反而没法看。
const maxList = 20

// Repo 在当前作用域内定出唯一的宾语。
// 返回 nil 表示没定成 —— 原因和候选已经写进 Out，调用方原样返回退出码即可。
func Repo(o Opt, kw string) *state.Repo {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	pool := o.Pool
	if pool == nil {
		pool = o.All
	}
	kw = strings.TrimSpace(kw)

	if kw == "" {
		switch len(pool) {
		case 0:
			fmt.Fprintf(out, "%s：作用域 %s 里一个仓库都没有\n", o.Cmd, o.Label)
			return nil
		case 1:
			return pool[0] // 就一个，不用问
		}
		fmt.Fprintf(out, "%s：作用域 %s 里有 %d 个仓库，得给一个关键字\n", o.Cmd, o.Label, len(pool))
		return choose(o, out, pool)
	}

	hits := state.FindIn(pool, kw)
	switch len(hits) {
	case 1:
		return hits[0]
	case 0:
		fmt.Fprintf(out, "%s：作用域 %s 里没有匹配 %q 的仓库，不回去全集找\n", o.Cmd, o.Label, kw)
		list(out, pool)
		return nil
	default:
		fmt.Fprintf(out, "%s：作用域 %s 里匹配 %q 的有 %d 个\n", o.Cmd, o.Label, kw, len(hits))
		return choose(o, out, hits)
	}
}

func isStdinTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// choose 列编号候选，能交互就等一个编号。支持 Bubble Tea 交互方向键选择与管道回退。
func choose(o Opt, out io.Writer, cands []*state.Repo) *state.Repo {
	sorted := make([]*state.Repo, len(cands))
	copy(sorted, cands)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Path) < len(sorted[j].Path) })

	show, more := sorted, 0
	if len(show) > maxList {
		show, more = sorted[:maxList], len(sorted)-maxList
	}
	if len(show) == 0 {
		return nil
	}
	if o.In == nil {
		for i, r := range show {
			fmt.Fprintf(out, "  %2d) %s   %s\n", i+1, r.Path, scope.Summary(r))
		}
		if more > 0 {
			fmt.Fprintf(out, "  … 还有 %d 个，换个更精确的关键字\n", more)
		}
		fmt.Fprintf(out, "  重敲一个更精确的关键字（作用域 %s）\n", o.Label)
		return nil
	}

	items := make([]MenuItem, len(show))
	for i, r := range show {
		items[i] = MenuItem{
			Key:    strconv.Itoa(i + 1),
			Label:  r.Name,
			Detail: fmt.Sprintf("(%s)   %s", r.Path, scope.Summary(r)),
			Value:  r,
		}
	}

	isInteractive := isStdinTerminal() && o.In == os.Stdin
	var menuIn io.Reader
	if !isInteractive {
		menuIn = o.In
	}

	title := fmt.Sprintf("\n%s：作用域 %s 匹配到多个仓库，请选择目标 (方向键移动，Enter 确认):", o.Cmd, o.Label)
	chosen, cancelled := Menu(MenuOpt{
		Title:       title,
		Prompt:      "  输入编号选择，回车取消: ",
		Items:       items,
		In:          menuIn,
		Out:         out,
		Interactive: isInteractive,
	})
	if cancelled || chosen == nil {
		fmt.Fprintln(out, "已取消")
		return nil
	}
	if repo, ok := chosen.Value.(*state.Repo); ok {
		return repo
	}
	return nil
}

// list 打印编号候选，返回实际打印出来的那几个（顺序即编号）。
// 短路径排前面 —— 更像是这一层直属的东西。
func list(out io.Writer, cands []*state.Repo) []*state.Repo {
	sorted := make([]*state.Repo, len(cands))
	copy(sorted, cands)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Path) < len(sorted[j].Path) })

	show, more := sorted, 0
	if len(show) > maxList {
		show, more = sorted[:maxList], len(sorted)-maxList
	}
	for i, r := range show {
		fmt.Fprintf(out, "  %2d) %s   %s\n", i+1, r.Path, scope.Summary(r))
	}
	if more > 0 {
		fmt.Fprintf(out, "  … 还有 %d 个，换个更精确的关键字\n", more)
	}
	return show
}
