package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"grscan/internal/security"
)

const CurrentVersion = 1

// Config 是可手改的配置文件内容 ~/.igit/config.json
type Config struct {
	Version      int      `json:"version"`
	Roots        []string `json:"roots"`
	ExcludeNames []string `json:"exclude_names"`
	ExcludePaths []string `json:"exclude_paths"`
	MaxDepth     int      `json:"max_depth"`
	Jobs         int      `json:"jobs"`
	Fast         bool     `json:"fast"`
	Deep         bool     `json:"deep"`
	GitExe       string   `json:"git_exe"`

	// TigExe：tig 的完整路径。留空则从 PATH 里找。
	TigExe string `json:"tig_exe"`
	// TermCmd：拉起 tig 的终端命令模板，留空用系统默认方式。
	// 占位符 {dir} = 仓库目录，{cmd} = 完整的 tig 命令行（路径已按需加引号）。
	// 例：wt.exe -d "{dir}" {cmd}   或   wt.exe -w new -d "{dir}" {cmd}
	TermCmd string `json:"term_cmd"`

	// HostUsers：域名到默认 HTTPS 用户名的映射记忆（例如 gitlab.corp.com -> zhangsan）
	HostUsers map[string]string `json:"host_users,omitempty"`
	// HostTokens：域名到加密 Token 的映射 (AES-GCM 加密)
	HostTokens map[string]string `json:"host_tokens,omitempty"`
	// RepoTokens：仓库级远端专属加密 Token，key 为 "repo_id#remote_name"
	RepoTokens map[string]string `json:"repo_tokens,omitempty"`

	// ExportDir：导出文件的目标存储目录，留空默认为数据目录下的 exports
	ExportDir string `json:"export_dir,omitempty"`
	// AutoOpenExport：导出后是否自动打开目标目录，默认 true
	AutoOpenExport *bool `json:"auto_open_export,omitempty"`
}

func (c *Config) ShouldAutoOpenExport() bool {
	if c == nil || c.AutoOpenExport == nil {
		return true
	}
	return *c.AutoOpenExport
}

func (c *Config) ResolvedExportDir(home string) string {
	if c != nil && strings.TrimSpace(c.ExportDir) != "" {
		return c.ExportDir
	}
	if home != "" {
		return filepath.Join(home, "exports")
	}
	h, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(h, ".igit", "exports")
	}
	return "exports"
}

// DefaultExcludeNames 默认跳过的噪音目录名（命中即不再下钻）。
// 全部可配，改配置文件即可。
var DefaultExcludeNames = []string{
	"node_modules",
	"vendor",
	".cache",
	"__pycache__",
	"$Recycle.Bin",
	"System Volume Information",
	"Windows",
	"Program Files",
	"Program Files (x86)",
	"AppData",
	"Temp",
	"Cache",
	".gradle",
	".m2",
	".terraform",
}

func Default() *Config {
	c := &Config{
		Version:      CurrentVersion,
		Roots:        guessRoots(),
		ExcludeNames: append([]string{}, DefaultExcludeNames...),
		ExcludePaths: []string{},
		MaxDepth:     10,
		Jobs:         defaultJobs(),
		GitExe:       "",
		HostUsers:    map[string]string{},
		HostTokens:   map[string]string{},
		RepoTokens:   map[string]string{},
	}
	return c
}

func defaultJobs() int {
	n := runtime.NumCPU()
	if n < 4 {
		n = 4
	}
	if n > 16 {
		n = 16
	}
	return n
}

// guessRoots 收集一些常见的放代码的位置，只保留真实存在的。
func guessRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}
	}
	cands := []string{
		filepath.Join(home, "IdeaProjects"),
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "source"),
		filepath.Join(home, "sources"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "code"),
		filepath.Join(home, "dev"),
		filepath.Join(home, "go", "src"),
	}
	out := []string{}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// ResolveHome 确定数据目录：命令行 > 环境变量 IGIT_HOME / GRSCAN_HOME > ~/.igit (兼容 ~/.grscan)
func ResolveHome(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := strings.TrimSpace(os.Getenv("IGIT_HOME")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GRSCAN_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".igit"
	}
	igitDir := filepath.Join(home, ".igit")
	if _, err := os.Stat(igitDir); err == nil {
		return igitDir
	}
	grscanDir := filepath.Join(home, ".grscan")
	if _, err := os.Stat(grscanDir); err == nil {
		return grscanDir
	}
	return igitDir
}

func Paths(home string) (configPath, statePath string) {
	return filepath.Join(home, "config.json"), filepath.Join(home, "state.json")
}

// Load 读取配置；不存在则生成默认配置并落盘。
func Load(configPath string) (*Config, error) {
	b, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			c := Default()
			if werr := Save(configPath, c); werr == nil {
				return c, nil
			}
			return c, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = 10
	}
	if c.Jobs <= 0 {
		c.Jobs = defaultJobs()
	}
	if len(c.ExcludeNames) == 0 && len(c.Roots) == 0 {
		c.ExcludeNames = append([]string{}, DefaultExcludeNames...)
	}
	if c.HostUsers == nil {
		c.HostUsers = map[string]string{}
	}
	if c.HostTokens == nil {
		c.HostTokens = map[string]string{}
	}
	if c.RepoTokens == nil {
		c.RepoTokens = map[string]string{}
	}
	return &c, nil
}

// Save 原子写入（先写临时文件再重命名），避免中断写出半截 JSON。
func Save(configPath string, c *Config) error {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

// AddRoot 往配置里追加一个扫描根（去重）。
func AddRoot(c *Config, root string) (bool, string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, root
	}
	for _, r := range c.Roots {
		if sameDir(r, abs) {
			return false, abs
		}
	}
	c.Roots = append(c.Roots, abs)
	return true, abs
}

func RemoveRoot(c *Config, root string) (bool, string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, root
	}
	kept := c.Roots[:0]
	removed := false
	for _, r := range c.Roots {
		if sameDir(r, abs) {
			removed = true
			continue
		}
		kept = append(kept, r)
	}
	c.Roots = kept
	return removed, abs
}

func sameDir(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}


func (c *Config) GetHostToken(host string) string {
	if c == nil || c.HostTokens == nil {
		return ""
	}
	enc, ok := c.HostTokens[strings.ToLower(strings.TrimSpace(host))]
	if !ok || enc == "" {
		return ""
	}
	dec, err := security.Decrypt(enc)
	if err != nil {
		return ""
	}
	return dec
}

func (c *Config) SetHostToken(host, token string) error {
	if c.HostTokens == nil {
		c.HostTokens = map[string]string{}
	}
	h := strings.ToLower(strings.TrimSpace(host))
	if token == "" {
		delete(c.HostTokens, h)
		return nil
	}
	enc, err := security.Encrypt(token)
	if err != nil {
		return err
	}
	c.HostTokens[h] = enc
	return nil
}

func (c *Config) GetRepoToken(repoID, remoteName string) string {
	if c == nil || c.RepoTokens == nil {
		return ""
	}
	k := fmt.Sprintf("%s#%s", repoID, strings.ToLower(strings.TrimSpace(remoteName)))
	enc, ok := c.RepoTokens[k]
	if !ok || enc == "" {
		return ""
	}
	dec, err := security.Decrypt(enc)
	if err != nil {
		return ""
	}
	return dec
}

func (c *Config) SetRepoToken(repoID, remoteName, token string) error {
	if c.RepoTokens == nil {
		c.RepoTokens = map[string]string{}
	}
	k := fmt.Sprintf("%s#%s", repoID, strings.ToLower(strings.TrimSpace(remoteName)))
	if token == "" {
		delete(c.RepoTokens, k)
		return nil
	}
	enc, err := security.Encrypt(token)
	if err != nil {
		return err
	}
	c.RepoTokens[k] = enc
	return nil
}

// ResolveToken 优先取仓库级远端 Token，未配则回退到 Host 级 Token。
func (c *Config) ResolveToken(repoID, remoteName, host string) string {
	if t := c.GetRepoToken(repoID, remoteName); t != "" {
		return t
	}
	if host != "" {
		if t := c.GetHostToken(host); t != "" {
			return t
		}
	}
	return ""
}
