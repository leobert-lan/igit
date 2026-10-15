package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		url     string
		user    string
		host    string
		isHTTPS bool
	}{
		{
			url:     "https://github.com/foo/bar.git",
			user:    "",
			host:    "github.com",
			isHTTPS: true,
		},
		{
			url:     "https://alice@gitlab.corp.com/team/repo.git",
			user:    "alice",
			host:    "gitlab.corp.com",
			isHTTPS: true,
		},
		{
			url:     "http://bob@192.168.1.100:8080/scm/test.git",
			user:    "bob",
			host:    "192.168.1.100",
			isHTTPS: true,
		},
		{
			url:     "git@github.com:foo/bar.git",
			user:    "git",
			host:    "github.com",
			isHTTPS: false,
		},
	}

	for _, tt := range tests {
		u, h, isH := ParseRemoteURL(tt.url)
		if u != tt.user || h != tt.host || isH != tt.isHTTPS {
			t.Errorf("ParseRemoteURL(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.url, u, h, isH, tt.user, tt.host, tt.isHTTPS)
		}
	}
}

func TestBuildRemoteURLWithUser(t *testing.T) {
	res, err := BuildRemoteURLWithUser("https://github.com/foo/bar.git", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "https://alice@github.com/foo/bar.git" {
		t.Errorf("got %q, want https://alice@github.com/foo/bar.git", res)
	}

	// replace user
	res, err = BuildRemoteURLWithUser("https://bob@gitlab.com/foo/bar.git", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "https://alice@gitlab.com/foo/bar.git" {
		t.Errorf("got %q, want https://alice@gitlab.com/foo/bar.git", res)
	}

	// remove user
	res, err = BuildRemoteURLWithUser("https://bob@gitlab.com/foo/bar.git", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "https://gitlab.com/foo/bar.git" {
		t.Errorf("got %q, want https://gitlab.com/foo/bar.git", res)
	}
}

func TestAppendIgnore(t *testing.T) {
	dir := t.TempDir()

	// 第一次添加，文件自动创建
	added, err := AppendIgnore(dir, []string{"__pycache__/", "*.pyc", ".env"})
	if err != nil {
		t.Fatalf("AppendIgnore failed: %v", err)
	}
	if len(added) != 3 {
		t.Errorf("expected 3 added, got %d", len(added))
	}

	// 再次添加重复项和新项
	added2, err := AppendIgnore(dir, []string{"__pycache__/", "dist/", ".env"})
	if err != nil {
		t.Fatalf("AppendIgnore failed: %v", err)
	}
	if len(added2) != 1 || added2[0] != "dist/" {
		t.Errorf("expected [dist/], got %v", added2)
	}

	lines, exists, err := ReadIgnore(dir)
	if err != nil || !exists {
		t.Fatalf("ReadIgnore failed: exists=%v, err=%v", exists, err)
	}
	expected := []string{"__pycache__/", "*.pyc", ".env", "dist/"}
	if len(lines) != len(expected) {
		t.Errorf("expected %d lines, got %d: %v", len(expected), len(lines), lines)
	}

	content, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	t.Logf("generated .gitignore:\n%s", string(content))
}
