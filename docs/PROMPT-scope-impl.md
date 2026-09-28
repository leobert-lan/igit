# 实现提示词：grscan Scope（导航式作用域）

> 用法：把本文**从下一行开始**整段复制到新会话里作为第一条消息。
> 本文自包含，不依赖任何历史讨论。设计定稿文档在 `docs/SCOPE.md`，可对照阅读（但以本文为准）。

---

## 你是谁 / 要做什么

你在给一个叫 **grscan** 的 Go 命令行工具加一个叫 **Scope** 的核心机制。

grscan 是 Windows 上的本地 git 仓库扫描整理工具：扫出散落各处的 git 仓库（用户清单里有 200+ 个），
然后按自己的判断分组、打标签、标状态。它当前是**纯只读**的 —— 只采集事实，从不改动任何仓库。
这个性质必须保持。

代码在 `C:\Users\Lenovo\WorkBuddy\scan_git`，Go module 名 `grscan`，产物 `grscan.exe`。
第三方依赖只有 `bubbletea` 和 `lipgloss`（交互面板用），其余全是标准库。

**这次要做的事**：把 grscan 从"扫一遍、出一份清单"的一次性扫描器，
变成一个有作用域、可以走进去、在里面移动、越走越细的工作台。

---

## 绝对约束（不可协商）

1. **永不写入用户仓库。** 不执行任何会改变 git 仓库状态的命令：不 push、不 pull、不 fetch、
   不 commit、不 reset、不 clean、不 rebase、不删分支、不改 git config。
   需要这些操作时，输出建议命令让用户自己敲，或者用 `open` 把他送到能操作的界面前。
2. **唯一允许写入的文件**是 grscan 自己的状态文件 `state.json`（`~/.grscan/state.json`）。
3. **Windows 优先**，路径分隔符是 `\`，输出用简体中文。
4. **不要引入新依赖。** 需要交互就复用现有 bubbletea 面板。
5. **不要破坏现有命令的行为。** 现有 `scan` / `list` / `tree` / `triage` / `set` / `export` / `hide` / `fold`
   等对"全集"的默认行为必须保持；Scope 是叠加在它们之上的约束，不是替换。
6. 每次改动后跑 `go build ./...` 和 `go test ./...`，必须全绿。

---

## 设计规格

### 3.1 Scope 是什么

**一棵虚拟的树，节点由元信息推导而来，序列成一个字符串；它同时决定"你在哪"和"你能敲什么"。**

虚拟根下挂三类维度节点：`file:`（磁盘目录）、`group:`（人工分组）、`tag:`（打的标签）。
三条路径都能走到同一个仓库 —— 这不是三种过滤方式，是三种**走法**。

### 3.2 元信息（先穷举，序列化是它的投影）

**入串（持久，必须能原样还原）**

| 元信息 | 说明 |
|---|---|
| 段序列 | 有序。顺序即来路，决定 `..` 退到哪 |
| 每段的维度 | `file` / `group` / `tag`（后续可扩展 `state` / `remote`） |
| 每段的标识 | 目录路径 / 分组名 / tag 名 / 仓库名 |
| 来源 | `explicit`（显式 cd）/ `cwd`（当前目录推断）/ `root`（默认全集） |

**不入串（实时，每次解析现算）**

| 元信息 | 说明 |
|---|---|
| 节点类型 | 集合节点还是仓库节点 —— 查清单推导 |
| 仓库集合 | 这个 scope 圈住哪些仓库 |
| 子项清单 | 下一层有哪些目录 / 分组 / tag / 仓库 |
| 汇总数字 | 数量、脏、未推送、无上游等 |
| 数据新鲜度 | 距上次扫描多久 |

**"无损失"的准确含义**：字符串只需还原"路径语义"，不需要还原"这儿有什么"。
后者永远现算，所以仓库增删不会让 scope 过时。

### 3.3 序列化语法

```
scope  := 空                       虚拟根（全集）
        | 段 ( "/" 段 )*

段     := 维度 分隔符 标识
维度   := "file" | "group" | "tag" | "repo"（repo 可省略）
分隔符 := ":"                      输出统一用冒号；解析时 ":" 和 "#" 都接受
```

**为什么默认 `:` 而不是 `#`**：`#` 在 bash 和 PowerShell 里都是注释符，
`grscan cd tag#待推送` 会被 shell 截断，必须加引号。冒号不需要转义。
解析器两种都收，输出统一冒号 —— 用户手写习惯不用改。

**例子**

| 字符串 | 含义 |
|---|---|
| *(空)* | 虚拟根，全集 |
| `tag:待推送` | tag 集合节点 |
| `tag:待推送/scan_git` | 仓库节点（无前缀段 = 在父范围内匹配仓库） |
| `file:D:\repos` | 该目录下全部仓库（集合节点） |
| `file:D:\repos\foo` | 若 foo 本身是仓库 → 仓库节点 |
| `group:工作` | 分组集合节点 |
| `group:工作/tag:待推送` | **交集**：工作分组里打了待推送的 |
| `file:D:\repos/tag:待推送` | **交集**：该目录下待推送的 |

**同维度嵌套 = 下钻，跨维度嵌套 = 交集**。两种语义统一成"在上一步结果里再筛"，规则不用特判。
这是语法白送的能力，实现时不要为它写特例代码。

**仓库节点不需要自己的前缀**：字符串里只写名字，解析时查清单发现它是仓库就切到仓库节点模式，
可用命令跟着变。类型不入串，是推导出来的。

### 3.4 节点类型与可用命令

| 节点 | `ls` | `list` | `show` | `log` | `check` | `triage` | `set` | `open` | `export` |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| 虚拟根 | ✓ | ✓ | — | — | ✓ | ✓ | — | — | ✓ |
| 集合节点 | ✓ | ✓ | — | — | ✓ | ✓ | ✓ | — | ✓ |
| 仓库节点 | — | — | ✓ | ✓ | ✓ | — | ✓ | ✓ | ✓ |

- 命令按层级收敛是好事（在根下误操作不到单个仓库），代价是用户看不见自己能做什么。
  所以 **`grscan` 不带参数 = 打印当前 scope 的摘要和可用动作**（见 3.7 输出样式）。
  `grscan help` 保持打印完整用法。
- `ls` = 列当前 scope 的**子项**（导航语义，列出所有子项，不受"只看待处理"过滤）。
- `list` = 列当前 scope 内的**仓库**，沿用现有表格渲染和过滤参数，只是在 scope 圈定的集合内再筛。
- 原设想的 `g show` / `g log` / `g go` 子命令组**不做** —— 走进仓库节点后命令自然可用，
  不需要第二层命名空间。同理**不做**仓库短名/别名机制，导航代替了记忆。

**导航命令沿用 shell 隐喻**（零学习成本）：

```cmd
grscan cd tag:待推送      进入（持久化 focus）
grscan cd ..              上一层
grscan cd /               回虚拟根（清除 focus）
grscan cd -               回到上一个 scope（可选，P0 之后做）
grscan ls                 这个 scope 下有什么
grscan pwd                我在哪，输出 scope 字符串
```

### 3.5 两个入口与失效规则

用户有两个进入方式，汇合到同一个状态：

- **入口 A（从清单进入）**：`grscan cd <scope>`，cwd 没动 → 状态必须持久化
- **入口 B（已在目录里）**：靠 cwd 推断，零状态

grscan 是短生命周期进程，改不了宿主 shell 的 cwd，所以**持久化 focus 是必需项**。

**存储**：`state.json` 新增字段

```json
"focus": {
  "scope": "tag:待推送",
  "at": "2026-09-23T16:00:00+08:00",
  "source": "explicit"
}
```

**解析优先级**（每次命令开头执行一次）

1. 读 focus。若不存在或 `source == "root"` → 走第 3 步
2. focus 存在：
   - 若 scope 以 `file:` 开头 → 检查 cwd 是否落在该路径下（`util.HasPrefixPath`）。
     **不在** → 判定为"出界"，清除 focus，打印一行提示，走第 3 步
   - 若 scope 以 `group:` / `tag:` 开头 → 这是纯逻辑维度，与 cwd 无关，**不做出界检查**
3. cwd 推断：生成 `file:<cwd>`，节点类型由解析决定
   - cwd 本身就是仓库 → 仓库节点
   - cwd 子树下有仓库 → 集合节点
   - 都没有 → **向上找最近的祖先仓库** → 仓库节点
   - 还找不到 → 走第 4 步
4. **报错，绝不静默退回全局**：打印"当前目录不在扫描范围内"并给建议
   （先 `scan`，或 `config add-root`，或 `cd /` 看全集）。
   你以为在看局部其实在看全局，这比报错危险得多。

**为什么"向下为主"而不是"向上"**：向上只能锁死在一个仓库，永远看不到一批。
向下能同时表达两种粒度 —— 在 `D:\repos` 看到 90 个，`cd` 进 `foo` 就只剩它一个，
粒度靠 cd 调节，不需要任何额外命令。

### 3.6 边界规则（写第一行代码前必须定死）

| # | 情况 | 规则 |
|---|---|---|
| 1 | 路径里的 `\` 与段分隔 `/` | 不冲突。但 Git Bash 风格 `D:/repos` 先规范化成 `D:\repos` |
| 2 | 分组名 / tag 名含 `/` | 会撕裂段。**禁止**，创建时校验并拒绝（在 `set` 和 TUI 的 G/T 输入处校验） |
| 3 | 大小写 | 路径沿用 Windows 不区分（`EqualFold`/统一小写比对）；分组与 tag 沿用现有 `EqualFold` 不区分 |
| 4 | `..` 退到哪 | 段内先降一级；没级可降就去整段。`tag:待推送/scan_git` → `tag:待推送` |
| 5 | 一个都没匹配到 | 见 3.5 第 4 步。绝不静默升级到全局 |
| 6 | 仓库被删 / 移出清单 | 解析失败 → 回退到父节点并打印原因，不崩不 panic |
| 7 | 名字重名 | 维度前缀已隔离命名空间：`group:工作` 与 `file:工作` 不冲突 |
| 8 | 空分组 / 空 tag | 保留节点，显示 0 个并提示可清理 |

另有两条：

- **作用域必须回显**：每条受 scope 影响的命令，输出第一行打印当前 scope，否则用户永远在猜。
- **隐藏目录仍然生效**：`state.IsHidden` 过滤对所有 scope 一视同仁，被 hide 的仓库在任何节点下都不出现。

### 3.7 输出样式（要克制，不要堆字）

`grscan` 无参：

```
作用域  tag:待推送 · 12 个仓库
  数据来自 3 小时前的扫描（grscan scan 刷新）

  可用
    ls            看这批的详细清单
    check         体检：该推的 / 落后的 / 脏的
    triage        进入面板逐个打标
    cd /          回到全集
```

`grscan ls`（集合节点）：

```
作用域  tag:待推送 · 12 个仓库

  待推送的      12 个
    领先远端    9 个
    工作区脏    3 个
    无上游      2 个

  子项
    scan_git            main  领先 3
    grscan-demo         main  领先 1 · 脏
    ...
```

`grscan show`（仓库节点）—— 只能有一个仓库，且**必须重新读一次真实状态**，
不能信 state.json 里的快照（快照会骗人：早上扫的，下午改了，缓存还写着"干净"）：

```
D:\repos\scan_git

  分支    main        领先 origin/main 3，落后 0
  改动    2 个已改，1 个未跟踪
  最近    a3f8c21  4 小时前  修复扫描时 .git 为文件的判定
  远端    origin  https://github.com/xxx/scan_git.git

  → 下一步
      git -C D:\repos\scan_git push origin HEAD:main
      grscan open --in=gui        打开 git gui 自己操作
```

正常输出保持干净；**只在状态异常或操作被拦截时才给"人话解释 + 下一步建议"**。不要每步都解释。

---

## 代码现状（改动落点）

```
main.go                      命令分发（switch raw[0]）、parseArgs、filterRepos
internal/state/state.go      Repo 结构、State 结构、Find / Upsert / Hide / Fold / SortBy
internal/config/config.go    Config 结构、Roots / ExcludeNames
internal/scan/scan.go        scan.Run(Options) (*Result, error)
internal/gitinfo/gitinfo.go  Inspect(dir, kind, gitExe, withStatus) *state.Repo、ResolveGit
internal/render/render.go    render.View{Roots, Width, Full}、Header() / Row(r) / FoldRow(dir, repos, n)
internal/tree/tree.go        Build(repos []*state.Repo) []*Node、Groups(nodes) []Group、Render
internal/tui/tui.go          Run(st *state.State, roots []string, query, statusPreset string, save func() error) error
internal/util/util.go        NormalizePath、HasPrefixPath(target, prefix)、HasAnyPrefixDir
internal/export/              CSV / JSON / Markdown*
```

**关键字段**（`state.Repo`）：`ID`（= 规范化路径）、`Path`、`Name`、`Kind`、`Branch`、
`Dirty`/`Staged`/`Unstaged`/`Untracked`、`Ahead`/`Behind`/`HasUpstream`、`Remotes`/`PrimaryURL`、
`Commit`/`CommitAt`/`Subject`、`Missing`、`Group`、`Tags []string`、`Status`、`Ignored`、`Note`。
`state.State` 有 `Repos`、`Scans`、`Hidden`、`Folded`。

**可复用的现成能力**

| 需要 | 已有 |
|---|---|
| 判断路径是否落在某目录下 | `util.HasPrefixPath` |
| 模糊匹配仓库 | `state.Find(q)`（完整 ID / 名字精确 / ID 片段 / 路径片段，返回精确优先） |
| group / tag / dirty / status 过滤 | `main.go` 的 `filterRepos(st, a)` |
| 目录树与子目录枚举 | `tree.Build` + `tree.Groups` |
| 状态文件原子写盘 | `state.Save()` |
| 实时读仓库真实状态 | `gitinfo.Inspect(dir, kind, gitExe, withStatus)` |
| 表格渲染 | `render.View` |

**需要改造的地方（重点）**

1. **`tui.Run` 现在吃整个 `*state.State`**，没有"子集"参数。
   要做 P2 的"triage 限定在 scope 内"，必须先给它加一个可选的仓库子集参数
   （例如 `Run(st, roots, query, statusPreset string, subset []*state.Repo, save func() error)`），
   **不要**为了限定范围去改 `st.Repos`（会污染状态并可能被保存回磁盘）。
2. **`ls` 现在是 `list` 的别名**（`main.go` 里 `case "list", "ls":`）。要拆开：`ls` 走导航语义。
3. `main.go` 的 `parseArgs` 把 `-` 开头的 token 当参数。`..` 和 `/` 不以 `-` 开头，是位置参数，没问题。
4. 所有受 scope 影响的命令都要在 `run()` 开头解析一次 scope，并把结果放进 `appCtx`。

---

## 实施分期

每期结束都要能编译、能跑、不破坏现有命令。做完一期告诉我进展，我会验收。

### P0 导航内核

新建 `internal/scope/scope.go`（纯逻辑，不依赖 TUI，方便单测）：

```go
type Seg struct {
    Dim   string // "file" | "group" | "tag" | "repo"
    Ident string
}

type Scope struct {
    Segs   []Seg
    Source string // "explicit" | "cwd" | "root"
}

func Parse(s string) (Scope, error)          // 字符串 → Scope
func (s Scope) String() string               // Scope → 字符串（用 ":"）
func (s Scope) Parent() Scope                // 段内降级，无级可降去整段
func (s Scope) Child(seg Seg) Scope
func Resolve(sc Scope, st *state.State) (*Resolved, error)  // 现算：节点类型 + 仓库集合 + 子项
```

`Resolved` 至少包含：`Kind`（root/collection/repo）、`Repos []*state.Repo`、`Children []Item`（名字 + 摘要）、`Freshness`。

`main.go` 新增 `cd` / `ls` / `pwd` 三个子命令；`state.State` 加 `Focus` 字段；
`grscan` 无参改为打印 scope 摘要 + 可用动作。

**新增单测** `internal/scope/scope_test.go`，至少覆盖：
- `Parse` / `String` 往返一致（含 `file:D:\repos\foo` 这种带反斜杠的）
- `:` 与 `#` 两种分隔符都能解析
- `Parent()` 两条规则（段内降级、去整段）
- 跨维度嵌套 = 交集（`group:工作/tag:待推送`）
- 仓库节点推导（`file:D:\repos\foo` 且 foo 是仓库）

**P0 验收**：`grscan cd tag:待推送` → `grscan pwd` 输出 `tag:待推送`；
`grscan ls` 列出这批仓库；`grscan cd ..` 回根；`grscan cd /` 后 `list --all` 仍是 204 个。
现有 `list` / `tree` / `triage` / `export` 行为不变。

### P1 仓库节点

`show` / `log` / `branches` / `remote`，以及 `open` 的传送门扩展。

- `show` 用 `gitinfo.Inspect` **现读一次**，不用快照
- `log` 做**一行一条的最近 20 条**（sha / 相对时间 / 作者 / subject）。
  **不要**做 tig 那种带分支图可下钻的视图 —— 那等于在 Windows 控制台里重造 tig，不值
- `open --in=explorer|gui|tig|term`，默认 `explorer`。
  启动前**先探测目标是否存在**（`exec.LookPath`），没装就直说"没找到 tig"，
  而不是让用户敲了个没反应的命令。`--in=term` 优先 `wt -d <dir>`，没有则回退 PowerShell

### P2 集合节点动作

`check` 限定在 scope 内，按"需要动作"分类输出：

```
作用域  tag:待推送 · 12 个仓库
  该推送的      9 个    领先远端，工作区干净
  落后的        2 个    先 pull，否则 push 会被拒
  没配上游的    1 个    分支无 upstream，push 会直接失败
  数据来自 3 小时前的扫描 · grscan scan 刷新
```

`triage` 限定在 scope 内 —— 先给 `tui.Run` 加子集参数，再接进去。

### P3 扩展维度（先不做，P0-P2 稳定后再说）

`state:`（new/active/done/archived）和 `remote:`（按远端 URL 聚合，其实就是现在的 `dupes` 变成可导航）。
维度做成可注册的，加这两个时不用改结构。

---

## 交付要求

1. 改完跑 `go build ./...`、`go vet ./...`、`go test ./...`，全绿
2. 重新编译产物：`go build -trimpath -ldflags="-s -w" -o grscan.exe .`（或双击 `build.cmd`）
3. 更新 `README.md` 的命令一览表和 `docs/USAGE.md`，加 Scope 一节
4. 端到端自己验一遍：从根 `cd` 进 tag → `ls` → 进仓库 → `show` → `cd ..` → `cd /`，
   每一步都把实际输出贴给我看
5. 遇到本文没覆盖的歧义，**停下来问我，不要自己拍板**。特别是涉及写操作和状态持久化的地方

## 三个已知未决项（我会在此期间定，你也可以提建议）

1. 分隔符最终定 `:` 还是 `#`（当前默认 `:`）
2. `open` 的默认落点：资源管理器 / git gui / tig
3. 是否需要在 `cd` 之外再加一个 `focus` 命令做显式查看与清除（当前设计是 `pwd` 查看、`cd /` 清除）
