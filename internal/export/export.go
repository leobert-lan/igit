package export

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"time"

	"grscan/internal/render"
	"grscan/internal/state"
)

// CSV 导出，带 UTF-8 BOM，Excel 双击直接打开不乱码。
func CSV(w io.Writer, repos []*state.Repo) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	header := []string{
		"name", "path", "kind", "branch", "dirty", "staged", "unstaged", "untracked",
		"ahead", "behind", "remote", "last_commit", "last_author", "last_subject",
		"group", "tags", "status", "ignored", "note", "first_seen", "last_seen", "missing",
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range repos {
		row := []string{
			r.Name, r.Path, render.KindCN(r), r.Branch, boolStr(r.Dirty),
			itoa(r.Staged), itoa(r.Unstaged), itoa(r.Untracked),
			itoa(r.Ahead), itoa(r.Behind), r.PrimaryURL, humanTime(r.CommitAt),
			r.Author, r.Subject,
			r.Group, joinTags(r), effectiveStatus(r), boolStr(r.Ignored),
			r.Note, humanTime(r.FirstSeen), humanTime(r.LastSeen), boolStr(r.Missing),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	return cw.Error()
}

// JSON 导出完整记录，可作为迁移/备份格式。
func JSON(w io.Writer, repos []*state.Repo) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(repos)
}

// Markdown 系列导出在 markdown.go：MarkdownTree / MarkdownFlat / MarkdownOutline。

func effectiveStatus(r *state.Repo) string {
	return render.StatusCN(r)
}

func joinTags(r *state.Repo) string {
	return render.TagsLabel(r)
}

func humanTime(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2006-01-02 15:04:05")
	}
	return s
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
