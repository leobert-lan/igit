package util

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// NormalizePath 把路径归一化成稳定的仓库 ID。
// Windows 文件系统大小写不敏感，所以统一小写；分隔符统一为 /。
func NormalizePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	s := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		s = strings.ToLower(s)
	}
	s = strings.ReplaceAll(s, `\`, "/")
	return strings.TrimSuffix(s, "/")
}

// LongPath 在 Windows 上为超长路径加 \\?\ 前缀，绕开 MAX_PATH 限制。
func LongPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\`) {
		return p
	}
	if !filepath.IsAbs(p) || len(p) < 200 {
		return p
	}
	return `\\?\` + p
}

// MatchName 判断目录名是否命中排除表，支持 * ? 通配。
func MatchName(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, pat := range patterns {
		p := strings.ToLower(strings.TrimSpace(pat))
		if p == "" {
			continue
		}
		if p == lower {
			return true
		}
		if strings.ContainsAny(p, "*?[") {
			if ok, err := filepath.Match(p, lower); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// HasPrefixPath 判断 target 是否落在 prefix 之下（或就是 prefix）。
func HasPrefixPath(target, prefix string) bool {
	t := NormalizePath(target)
	p := NormalizePath(prefix)
	if t == p {
		return true
	}
	return strings.HasPrefix(t, strings.TrimSuffix(p, "/")+"/")
}

// HasAnyPrefixDir 返回 target 命中的最长（最具体）的前缀目录，没有则返回空串。
func HasAnyPrefixDir(target string, prefixes []string) string {
	best := ""
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if HasPrefixPath(target, p) && len(p) > len(best) {
			best = p
		}
	}
	return best
}

// HasAnyPrefix 判断 target 是否落在任一前缀目录之下。
func HasAnyPrefix(target string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if HasPrefixPath(target, p) {
			return true
		}
	}
	return false
}

// Age 返回 "3 天前" 这种人类可读的时间差。
func Age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " 分钟前"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + " 小时前"
	case d < 30*24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + " 天前"
	case d < 365*24*time.Hour:
		return itoa(int(d/(30*24*time.Hour))) + " 个月前"
	default:
		return itoa(int(d/(365*24*time.Hour))) + " 年前"
	}
}

// HumanSize 返回可读的文件体积。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa(int(n)) + " B"
	}
	div, exp := int64(unit), 0
	for i := n / unit; i >= unit && exp < 5; i /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSpace(FormatFloat(float64(n)/float64(div))) + " " + string("KMGTP"[exp]) + "iB"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func FormatFloat(f float64) string {
	s := strings.TrimRight(strings.TrimRight(formatNum(f, 1), "0"), ".")
	return s
}

func formatNum(f float64, prec int) string {
	i := int64(f)
	frac := f - float64(i)
	d := 1.0
	for k := 0; k < prec; k++ {
		d *= 10
	}
	fr := int64(frac*d + 0.5)
	if float64(fr) >= d {
		i++
		fr = 0
	}
	s := itoa(int(i))
	if prec == 0 {
		return s
	}
	tail := itoa(int(fr))
	for len(tail) < prec {
		tail = "0" + tail
	}
	return s + "." + tail
}

// PadRight 按显示宽度补齐空格（粗略按 rune 数计，够用）。
func PadRight(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// PadRightDisplay 按显示宽度补齐空格：中文占两列，超出时至少留两格空隙，
// 免得数字贴到名字上。
func PadRightDisplay(s string, width int) string {
	w := DisplayWidth(s)
	if w >= width {
		return s + "  "
	}
	return s + strings.Repeat(" ", width-w)
}

// DisplayWidth 粗略的显示宽度：CJK 与全角记两列，其余记一列。
func DisplayWidth(s string) int {
	n := 0
	for _, r := range s {
		if r > 0x2E7F {
			n += 2
			continue
		}
		n++
	}
	return n
}

// Truncate 截断到指定宽度，超出部分用 … 收尾。
func Truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// SortedKeys 让 map 输出顺序稳定，利于 diff。
func SortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
