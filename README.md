# grscan — 本地 git 仓库扫描与整理工具

Windows 上扫出散落各处的 git 仓库，然后按自己的判断分组、标记、归档，处理完的不再打扰你。

单个 exe，无运行时依赖。数据就是两个 JSON 文件，随手可迁。

---

## 1. 代码在哪

```
C:\Users\Lenovo\WorkBuddy\scan_git\
├── go.mod
├── go.sum
├── main.go                    命令分发、CLI 子命令
├── internal\config\config.go  配置读写 + 默认扫描根猜测
├── internal\state\state.go    仓库清单 / 分组 / 状态 / 忽略 / 备注，原子写盘
├── internal\gitinfo\gitinfo.go 直接读 .git 拿元信息，必要时回退 git 命令
├── internal\scan\scan.go      并发目录遍历，定位 .git
├── internal\tui\tui.go        交互面板
├── internal\tui\tui_test.go   面板单元测试
├── internal\render\render.go  列表渲染
├── internal\tree\tree.go      目录树构建与渲染
├── internal\export\export.go  CSV / JSON 导出
├── internal\export\markdown.go Markdown 报告（按目录分节）
├── internal\util\util.go      路径归一化、体积与时长格式化
├── build.cmd                  重新编译
└── grscan.exe                 已编译好的产物
```

第三方依赖只有两个：`bubbletea`、`lipgloss`（交互面板用）。其余全是标准库。

---

## 2. 编译

```bash
cd /c/Users/Lenovo/WorkBuddy/scan_git
go build -trimpath -ldflags="-s -w" -o grscan.exe .
```

- 产物约 4.1 MB
- 也可以在资源管理器里双击 **`build.cmd`**
- 跑测试：`go test ./...`

---

## 3. 运行

建议先把它放到一个固定位置，避免路径变来变去：

```cmd
mkdir "%LOCALAPPDATA%\grscan"
copy grscan.exe "%LOCALAPPDATA%\grscan\"
```

之后用 `%LOCALAPPDATA%\grscan\grscan.exe <命令>` 调用。想省事就把这个目录加进 PATH。

### 第一次使用

```cmd
rem 1) 探查各盘，看哪些目录里仓库密集（只读两层，很快）
grscan config discover

rem 2) 把看着靠谱的目录加进扫描根，可重复执行
grscan config add-root D:\repos
grscan config add-root D:\work
grscan config add-root E:\jenkins

rem 3) 正式扫描
grscan scan

rem 4) 交互式判别处理
grscan triage
```

### 路径默认显示完整

`list` 会打出完整绝对路径，不做截断也不压缩。想要窄表格加 `--short`，
它会把路径压成相对扫描根的形式并截断。导出（csv/json/md）一律是完整路径。

### 按目录树看分布

清单是按名字排的，看不出仓库在磁盘上怎么聚集。`tree` 补上这个视角：

```cmd
grscan tree --brief --depth=2
```

```
D:\
    ├── repos\ryxl_rd\
    │   ├── algorithm\  · 9 个仓库（用 --depth 展开）
    │   ├── firmware\  · 40 个仓库（用 --depth 展开）
    │   └── software\  · 41 个仓库（用 --depth 展开）
    └── work\
        ├── ASWorkSpace\  · 69 个仓库（用 --depth 展开）
        ├── backup_code\  · 12 个仓库（用 --depth 展开）
        └── QtWorkSpace\  · 22 个仓库（用 --depth 展开）
```

- `--brief` 只看骨架和数量，不逐个列仓库
- `--depth=N` 展开到第 N 层，超出的折叠成一行摘要
- 只有一层子节点的目录会被自动合并，不会出现一条拖到底的无意义链
- 过滤参数和 `list` 完全一样，可以 `grscan tree --dirty` 看改动集中在哪

不带 `--brief` 时，叶子会把每个仓库连同分支、改动、时间、分组、状态和完整路径列出来。

### 目录级：hide 与 fold

逐个仓库打标记太慢，更多时候你是对**一整个目录**有态度。两条规则：

```cmd
grscan hide  D:\repos\ryxl_rd    :: 这块彻底不看
grscan fold  D:\repos\ryxl_rd    :: 这块只留一行，知道有多少就行
grscan dirs                      :: 看看现在设了什么
```

| | `hide` | `fold` |
|---|---|---|
| 结果里 | 完全不出现，`--all` 也不破例 | 收拢成一行，显示数量和待处理数 |
| 扫描时 | 整棵跳过，连一次目录读取都不做 | 照常扫（要知道数量和状态） |
| 新克隆进来的仓库 | 自动一并隐藏 | 自动一并折叠 |
| 导出 md | 不出现 | 一节汇总，不逐条列出 |
| 面板里 | 不出现 | 占一行，勾选它等于勾选底下全部 |

示例——fold 之后：

```
D:\
    ├── repos\ryxl_rd\  · 90 个仓库（已折叠，grscan unfold 展开）
    └── work\
        ├── ASWorkSpace\  · 69 个仓库（用 --depth 展开）
        └── backup_code\  · 12 个仓库（用 --depth 展开）
```

hide 之后则是整个 `ryxl_rd\` 连影子都不剩，总数从 204 掉到 114。

规则写在 `state.json` 的 `hidden` / `folded` 字段里，是一对"目录路径 + 时间 + 备注"，
可以手改，也可以加备注说明为什么：`grscan hide D:\tmp --note="临时克隆，不用管"`。

在面板里按 `e` 可以对光标所在目录**临时**折叠/展开，只影响本次会话，不会写进规则。

### 导出 Markdown

```cmd
grscan export --format=md -o repos.md
```

产出的是一份拿来琢磨的报告，不是一张流水账：

1. **概览** —— 各状态数量、分组分布
2. **值得先看的几类** —— 把动手的理由挑出来：
   磁盘上已找不到 / 有未提交改动 / 有未推送的提交 / 没有配置远端 / 一年以上没提交
3. **按目录** —— 先给一棵目录骨架定位，再按规模合适的目录分小节列表格

分节不会一个目录一节地铺满全文：超过 25 个仓库才会继续下钻，
而且如果底下全是单仓库目录就直接整节列出——拆开没有意义。

其他格式：

```cmd
grscan export --format=outline          # 只有目录树骨架
grscan export --format=csv -o repos.csv # 带 BOM，Excel 直接开
grscan export --format=json -o repos.json # 原始结构化数据
grscan export --format=md --flat -o table.md # 退回平铺表格
```

`--format=json` 输出的东西足够全，想拿它喂给别的脚本或者让 AI 接着分析，都行。

### 只导出一部分

默认导出全集（"给我一份完整清单"通常是这个意思），但给了过滤条件就只导那一批，
可用条件和 `list` 一致：

```cmd
grscan export --dirty      -o 未提交改动.md   :: 75 个
grscan export --no-remote  -o 纯本地.md       :: 20 个，删了就没了
grscan export --unpushed   -o 未推送.md
grscan export --kind=bare --format=csv -o 裸仓.csv
grscan export --query=ryxl -o ryxl.md         :: 路径含 ryxl 的
```

不给 `-o` 就打到标准输出，可以接管道：`grscan export --format=json | jq ...`

### 命令一览

| 命令 | 作用 |
|---|---|
| `scan` | 扫配置的根目录，结果并入清单 |
| `list`（`ls`） | 列仓库，默认只列**待处理**，路径默认完整显示 |
| `tree` | 按磁盘目录树展开，看仓库的空间分布 |
| `triage`（`tui`） | 交互面板，逐个判别 |
| `set <关键字>` | 批量设置分组/状态/标签/备注/忽略 |
| `dupes` | 按远端地址找重复克隆 |
| `stats` | 统计概览 |
| `open <关键字>` | 资源管理器打开 |
| `rm <关键字>` | 从清单移除（不动磁盘） |
| `prune` | 清理磁盘上已不存在且未忽略的记录 |
| `export` | 导出 md / outline / csv / json |
| `hide <目录>` | 整个目录不再出现，扫描也不进 |
| `unhide <目录>` | 取消隐藏 |
| `fold <目录>` | 整个目录收拢成一行，只显示数量 |
| `unfold <目录>` | 取消折叠 |
| `dirs` | 列出当前所有隐藏/折叠规则 |
| `config show` | 看当前配置和数据目录位置 |

### 常用参数

```cmd
grscan scan --root=D:\repos --fast     --fast 不起 git 进程，只读 .git，快很多
grscan scan --size                     统计仓库体积（会慢不少）
grscan list --all                      看全部
grscan list --short                    压缩路径显示（默认完整路径）
grscan list --group=工作 --sort=commit --desc
grscan list --dirty                    只看有未提交改动的
grscan list --missing                  只看磁盘上找不到的
grscan list --json                     结构化输出，方便喂给别的脚本
grscan tree --brief --depth=2          目录树骨架
grscan tree --status=new               仍未处理的都散落在哪些目录
grscan export --format=md -o repos.md  导出 Markdown 报告
grscan set myproject --group=归档 --status=done --yes
```

### 交互面板快捷键

底部有一行**常驻键位条**，列出当前能用的按键，不用记下面这张表。它会跟着状态变：

- 有勾选时整条换成"作用于已选项"的键（`d` / `G` / `Esc 取消选择`…）
- 列表为空时换成切换视图、清除过滤这类脱身动作
- 窗口变窄会自动丢掉靠后的键，重要的（移动、勾选、`d`、`q`）永远留着

完整版按 `?` 呼出：

```
对象   有勾选的行 = 作用于全部勾选行；没勾选 = 只作用于光标所在行
移动   ↑/k  ↓/j  PgUp/PgDn  Home/End
多选   空格 选择/取消     A 全选    a 取消选择   Esc 清空选择
处理   S 循环状态        d 已处理  w 跟进中  z 已归档  u 退回待处理
归类   G 分组           T 标签    N 备注    x 清除分组
忽略   I 忽略/取消忽略
折叠   e 折叠/展开光标所在目录（临时，不写盘）
视图   v 待处理/全部/已忽略      F 过滤路径   c 清除过滤
其它   o 资源管理器打开  r 查看详情  p 显示完整路径
保存   Ctrl+S           q 退出并保存        Ctrl+C 放弃改动退出
```

刚进去时还会给一句话引导——"没勾选时操作只作用于当前行 · 空格可勾选多个一起改 · ? 看全部快捷键"，
任何一次操作都会把它顶掉。`G` / `T` / `N` / `F` 进入输入状态时，
下方提示 "Enter 确认 · Esc 取消 —— 此刻字母键都是输入内容"。

对折叠起来的目录，勾选它一次等于勾选底下全部仓库，
按 `d` / `G` / `T` 会一次性作用到整组。

操作优先作用于已勾选的行，没勾选就作用于光标所在行。
按 `d` 之后该行立刻从"待处理"视图消失，适合快速清场。

> **注意**：`triage` 必须在真实终端里跑（PowerShell 或 Windows Terminal）。不要在 Git Bash 里管道重定向，也别指望脚本能驱动它——它是全屏交互程序。

---

## 4. 打包与迁移

没有安装过程，没有注册表，没有服务。

```
grscan.exe        单个文件，拷到哪台机器都能跑
config.json       配置
state.json        仓库清单 + 你的处理结论
```

迁移 = 把这三个文件一起拷走。默认位置 `C:\Users\<你>\.grscan\`。

换位置有三种方式，优先级从高到低：

```cmd
grscan --home D:\my\grscan scan
```

```cmd
set GRSCAN_HOME=D:\my\grscan
```

都不设就用 `~/.grscan`。

想恢复到初始状态，把 `~/.grscan` 整个删掉即可，下次运行会重新生成默认配置。

---

## 5. 配置

文件：`C:\Users\<你>\.grscan\config.json`

```json
{
  "version": 1,
  "roots": ["D:\\repos", "D:\\work"],
  "exclude_names": ["node_modules", "vendor", "$Recycle.Bin", "AppData"],
  "exclude_paths": ["D:\\work\\某个不想扫的子目录"],
  "max_depth": 10,
  "jobs": 16,
  "fast": false,
  "deep": false,
  "git_exe": ""
}
```

| 字段 | 说明 |
|---|---|
| `roots` | 扫描根目录。不递归到全盘，只扫这里列出的 |
| `exclude_names` | 命中目录名就整棵跳过，支持 `*`、`?` 通配 |
| `exclude_paths` | 按路径前缀跳过 |
| `max_depth` | 从根往下最多钻几层，默认 10 |
| `jobs` | 并发 worker 数，默认取 CPU 核数（上限 16） |
| `fast` | true 则完全不起 git 进程，拿不到未提交改动 |
| `deep` | 默认找到仓库就停止下钻；true 则继续找嵌套仓库 |
| `git_exe` | git 不在 PATH 时手动指定路径 |

改配置有两种方式，效果一样：

```cmd
grscan config add-root D:\repos
grscan config rm-root  D:\repos
grscan config add-exclude node_modules
grscan config set jobs=24 max-depth=6
```

或者直接编辑 `config.json`——它是纯文本，改完保存下条命令就生效。

> `AppData` 默认在排除列表里。如果你确实把仓库放到了 `AppData` 下面，跑 `grscan config rm-exclude AppData` 再扫。

---

## 6. 几个设计取舍

- **状态存在 JSON 而非 SQLite**：几千个仓库的量级完全够用，可以直接 diff、直接改、直接 git 管理。
- **扫描的事实 vs 你的结论分开存**：每次扫描覆盖前者，永远不动后者。所以你可以随时重扫，不会丢失已经打好的分组和状态。
- **忽略 ≠ 删除**：标了忽略的仓库，下次扫描会在父目录直接整棵跳过——既不上报，也不花时间。
- **目录级规则优先于逐个打标**：大多数人真正想表达的是"这块我不看"或"这块我知道有一堆就行"，
  而不是逐个仓库做决定。`hide` / `fold` 就是为这种粒度准备的，而且新克隆进来的仓库会自动继承。 删除**：标了忽略的仓库，下次扫描会在父目录直接整棵跳过——既不上报，也不花时间。
- **优先纯 Go 读 `.git`**：只有遇到打包对象（`git gc` 之后）或需要未提交改动时，才被迫起一次 git 进程。这是速度的主要来源。
- **同样的远端，多份克隆**：`dupes` 会按 remote 地址把它们并排列出，方便判断哪些是重复的。
- **清单视图 vs 空间视图**：`list` 回答"有哪些"，`tree` 回答"它们怎么分布"。两个视角缺一个，判断就容易偏。
- **导出优先给完整信息**：路径不压缩、截断， flaws/线索直接挑出来——导出的东西是要拿到别处继续想的，偷工减料没意义。
- **工具不替你决定处理流程**：分组、标签、备注、四个状态都是自由发挥的容器，
  至于"哪些该删、哪些该归档、哪些该推远端"，等你想法清楚了，
  用 `set --tag=... --note=...` 顺着思路往下走就行；md 报告里的"值得先看"只是线索，不是结论。
