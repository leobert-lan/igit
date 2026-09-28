# grscan 使用手册

扫出机器上散落各处的 git 仓库，然后按你自己的判断分组、标记、归档——处理完的不再打扰你。

单个 exe，无运行时依赖。数据就是两个 JSON 文件，随手可迁。

> 这份手册按"功能"组织，覆盖全部命令与参数。
> 编译、目录结构、设计取舍见 [README](../README.md)。

---

## 目录

- [1. 一分钟上手](#1-一分钟上手)
- [2. 五个必须先懂的概念](#2-五个必须先懂的概念)
  - [2.6 作用域：你在哪，决定了你能敲什么](#26-作用域你在哪决定了你能敲什么)
- [3. 扫描：scan](#3-扫描scan)
- [4. 清单视图：list](#4-清单视图list)
- [5. 空间视图：tree](#5-空间视图tree)
- [6. 交互判别：triage](#6-交互判别triage)
- [7. 批量标记：set](#7-批量标记set)
- [8. 目录级规则：hide / fold](#8-目录级规则hide--fold)
- [9. 找重复与查统计：dupes / stats](#9-找重复与查统计dupes--stats)
- [10. 导出：export](#10-导出export)
- [11. 配置：config](#11-配置config)
- [12. 杂项：open / rm / prune](#12-杂项open--rm--prune)
- [13. 列的含义](#13-列的含义)
- [14. 典型工作流](#14-典型工作流)
- [15. 常见问题](#15-常见问题)
- [16. 命令速查](#16-命令速查)

---

## 1. 一分钟上手

```cmd
rem 1) 探查各盘，看哪些目录里仓库密集（只读两层，很快）
grscan config discover

rem 2) 把看着靠谱的目录加进扫描根，可重复执行
grscan config add-root D:\repos
grscan config add-root D:\work

rem 3) 正式扫描
grscan scan

rem 4) 交互式判别处理
grscan triage
```

之后日常只需要两步：`grscan scan` 更新事实，`grscan triage` 或 `grscan list` 看剩下的。

**建议先固定 exe 位置**，免得路径变来变去：

```cmd
mkdir "%LOCALAPPDATA%\grscan"
copy grscan.exe "%LOCALAPPDATA%\grscan\"
```

之后用 `%LOCALAPPDATA%\grscan\grscan.exe <命令>`，或把这个目录加进 PATH。

---

## 2. 五个必须先懂的概念

### 2.1 扫描的事实 vs 你的结论

每条记录分两半：

| | 内容 | 谁在改 |
|---|---|---|
| **事实** | 路径、分支、最近提交、改动数、远端地址、体积 | 每次 `scan` 整体覆盖 |
| **结论** | 分组、标签、状态、忽略、备注 | 只有 `set` / `triage` 能改 |

**所以可以随便重扫，已经打好的分组和状态不会丢。** 这是整个工具的地基。

### 2.2 四个状态 + 忽略

| 状态 | 含义 | 谁在用 |
|---|---|---|
| `new` 待处理 | 还没看过 | 默认视图只显示这一类 |
| `active` 跟进中 | 看了，还要再处理 | 自己定 |
| `done` 已处理 | 处理完了 | 自己定 |
| `archived` 已归档 | 留着但不看了 | 自己定 |
| `ignored` 已忽略 | 不当作仓库看待 | 下次扫描在父目录整棵跳过 |

状态是**自由容器**，工具不规定"已处理"具体意味着什么。四个状态怎么用由你定。

`--status` 参数认中文别名：`待处理 / 跟进中 / 已处理 / 已归档 / 已忽略`。

### 2.3 分组、标签、备注

- **分组**：一个仓库只能有一个（`G` 键 / `set --group=`），适合"项目""客户""归属团队"
- **标签**：可以有多个（`T` 键 / `set --tag=a,b`），适合"待删""要推远端""依赖老环境"
- **备注**：一句话说明（`N` 键 / `set --note=`），写清楚"为什么这么处理"

输入分组或标签时，面板底部会提示已有的值，避免同一个意思写出两种写法。

### 2.4 hide 与 fold：对一整块目录表态

逐个仓库打标记太慢，人真正的想法往往是"这块我不看"或者"这块我知道有一堆就行"。

| | `hide` 隐藏 | `fold` 折叠 |
|---|---|---|
| 结果里 | 完全不出现，`--all` 也不破例 | 收拢成一行，显示数量和待处理数 |
| 扫描时 | 整棵跳过，连一次目录读取都不做 | 照常扫（要知道数量和状态） |
| 新克隆进来的仓库 | 自动一并隐藏 | 自动一并折叠 |
| 导出 md | 不出现 | 一节汇总，不逐条列出 |
| 面板里 | 不出现 | 占一行，勾选它等于勾选底下全部 |

新克隆进来的仓库自动继承——这是目录级规则相对逐个打标最大的好处。

### 2.5 两个视角：清单 vs 空间

- `list` 回答"有哪些"（按名字/时间/分组排序的清单）
- `tree` 回答"它们怎么分布"（按磁盘目录展开）

判断"这块要不要整块处理"必须用 `tree`，光看清单看不出来仓库在磁盘上怎么聚集。

### 2.6 作用域：你在哪，决定了你能敲什么

清单一旦上了两百个，"看全部"就没意义了。作用域让你先**走进去**，
之后的命令都只在这批仓库里生效：

```cmd
grscan                 :: 不带命令 = 进会话，提示符带着当前作用域
grscan ls              :: 当前作用域下有什么
grscan cd tag:待推送   :: 走进去（只在本次会话里有效）
grscan cd ..           :: 上一层
grscan cd /            :: 回到全集
grscan pwd             :: 我在哪
grscan --in=file:D:\work list   :: 一次性指定范围，给脚本留的口子
```

会话长这样：

```
grscan / 190 > cd file:D:\work\ASWorkSpace\tmp\opensource
作用域  file:D:\work\ASWorkSpace\tmp\opensource · 8 个仓库
grscan file:D:\work\ASWorkSpace\tmp\opensource 8 > triage
grscan file:D:\work\ASWorkSpace\tmp\opensource 8 > exit
```

位置**不写盘**：每次进来都按当前目录重新推断，没有"上次停在哪"这回事。
要临时换一批看，`--in=<作用域>` 只对这一条命令生效。

作用域写成一串路径，虚拟根下挂三个维度：

| 写法 | 含义 |
|---|---|
| `file:D:\repos` | 目录维度 |
| `group:工作` | 分组维度 |
| `tag:待推送` | 标签维度 |
| `group:工作/tag:待推送` | **交集**：工作分组里待推送的 |
| `tag:待推送/scan_git` | 进到单个仓库 |

**同维度嵌套 = 往下钻，跨维度嵌套 = 交集**，两种语义统一成"在上一步结果里再筛"。
`:` 与 `#` 都认（`#` 在 shell 里是注释符，常被截断），输出统一用 `:`。
不带维度前缀的一段表示在当前作用域里往下走一步：
在目录下是进子目录，在分组/标签下是进某个仓库。

根下 `ls` 把三个维度的入口摆出来，各带数量：

```
作用域  全集 · 194 个仓库
  数据来自 7 小时前的扫描（grscan scan 刷新）

  按目录
    file:D:\work              93 个
    file:D:\repos             90 个
    file:C:\Users             9 个

  按分组
    group:外设库              3 个

  按标签
    按标签（还没有）
```

集合节点下，`ls` 先给汇总再列子项；子目录能继续下钻，仓库能直接进：

```
作用域  file:D:\work · 93 个仓库

  file:D:\work  93 个
    领先远端      8 个
    工作区脏      54 个
    无上游        18 个

  子项
    asworkspace/              55 个
    qtworkspace/              16 个
    vs/                       1 个
```

**走进去之后，作用域圈定这批命令的对象**

| 命令 | 在作用域内 |
|---|---|
| `triage`（`tui`） | 只在这批里判别/分组/打标签，标题栏钉着作用域 |
| `tree` / `stats` | 只统计这批 |
| `export` | 只导出这批 |
| `list` | 在这批里再套过滤条件 |

在扫描范围外的目录敲这些命令，它们退回全集但会先明说
（"（当前目录不在扫描范围内，这次按全集处理）"），绝不静默换口径。
`dupes` 是例外：找重复克隆要跨全集比对，不受作用域限制。

**一个目录既是仓库、又装着一堆仓库**

不指定维度就无法确定看哪个视角，所以两个视角都留着：

| 写法 | 意思 |
|---|---|
| `file:<路径>` | 看**里面**有什么（默认）。自身是仓库时摘出来单独报：`（这里本身也是个仓库：…）` |
| `repo:<名字或路径>` | 看**它自己**。带 `repo:` 前缀就是绝对定位，不管你当前站在哪一层 |
| 裸名字（`cd vs`） | 在**当前层**往下走一步 |

```
grscan file:D:\...\opensource 8 > cd repo:opensource
作用域  opensource · 1 个仓库
grscan opensource 1 > cd -
作用域  file:D:\...\opensource · 8 个仓库
```

`cd -` 回到来路：从集合进到"它自己"之后 `cd ..` 会一路退到全集，
而人多半只是想退回刚才那一层。

**Tab 补全**

站在哪一层，Tab 就补什么 —— 第一个词是当前界面能敲的命令，`cd` 后面是这一层的子项，
`file:` 后面直接读磁盘真实目录。嵌套节点上两个视角的候选一起冒出来，不用记语法。

**两个入口**

1. 会话里 `cd` 进去：位置只在会话内存活，`exit` 回到原点
2. 已经在目录里：不敲 `cd` 也能干活，作用域由当前目录推断（零状态）

推断顺序：当前目录本身就是仓库 → 仓库节点；子树里有仓库 → 集合节点；
都没有 → 向上找最近的祖先仓库；还找不到就**明确报错**，绝不静默退回全局。
（你以为在看局部其实在看全局，这比报错危险得多。）

**作用域圈定哪些命令**

| 命令 | 在这一批仓库里生效 |
|---|---|
| `ls` `list` `pwd` `triage` `tree` `stats` `export` | 是。定不出作用域时报错（`ls`/`list`/`pwd`）或明说"按全集处理" |
| `set` `open` `rm` `dupes` `scan` `config` | 否。按你给的关键字在全集里找，与作用域无关 |

`set` / `open` / `rm` 走的是"关键字匹配"而非"当前这批"，所以它们不受作用域限制；
要给整批仓库打标，用 `triage` 面板，或 `set` 配 `--query=`。

> `ls` 以前是 `list` 的缩写，现在拆成了导航命令。看仓库表格请用 `list`。

---

## 3. 扫描：scan

```cmd
grscan scan
grscan scan --root=D:\repos --root=E:\work    :: 临时指定，可重复，不写进配置
grscan scan --fast                            :: 不起 git 进程，只读 .git，快很多
grscan scan --size                            :: 统计仓库体积（会慢不少）
grscan scan --deep                            :: 命中仓库后仍继续找嵌套仓库
grscan scan --jobs=24                         :: 并发数
grscan scan --max-depth=6                     :: 目录最多下钻几层
```

输出示例：

```
扫描完成，用时 3.2s
  遍历目录   18420
  扫到仓库   199（新增 0）
  已忽略跳过 12
  磁盘已无   3（运行 grscan prune 清理）
  清单总数   199，其中待处理 199
```

要点：

- **`--fast` 拿不到未提交改动**。默认会起 git 进程问一次工作区状态，`--fast` 跳过这步，只读 `.git` 元数据。机器慢或仓库多时先 `--fast` 摸底，再完整扫一次。
- **`--size` 只在本次扫描统计体积**，之前统计过的值会保留（`scan` 不会把已有的体积清零）。
- **"磁盘已无"只在扫描范围内判定**。只扫 `D:\` 时不会把 `E:\` 上的旧记录误标成丢失；标了忽略的仓库也不算丢失。
- **`hidden` 目录在扫描时整棵跳过**，成本为零；`folded` 目录照常扫。
- 每次扫描会往 `state.json` 的 `scans` 里留一条流水（最多保留 50 条），可以回头看历史。

---

## 4. 清单视图：list

```cmd
grscan list                        :: 默认只看「待处理」
grscan list --all                  :: 全部（含已忽略、磁盘已无）
grscan list --dirty                :: 有未提交改动的
grscan list --clean                :: 没有未提交改动的
grscan list --unpushed             :: 有未推送提交的
grscan list --no-remote            :: 没配远端的（删了就没了）
grscan list --missing              :: 磁盘上找不到的
grscan list --ignored              :: 已忽略的
grscan list --group=工作           :: 按分组
grscan list --tag=待删             :: 按标签
grscan list --status=active        :: 按状态
grscan list --query=ryxl           :: 路径或名字包含
grscan list --kind=bare            :: 按类型（repo/bare/worktree/submodule）
grscan list --sort=commit --desc   :: 排序
grscan list --limit=30             :: 最多显示 N 条
grscan list --short                :: 压缩路径显示
grscan list --json                 :: 结构化输出
```

几个容易踩的点：

- **默认只看"待处理"**。这是刻意的设计：清场时你只想看还没处理的。想看全部加 `--all`。
- **`--missing` / `--ignored` 是"只要这一类"的强开关**，不再叠加"待处理"过滤。`--dirty` 之类则是在待处理里再筛。
- **路径默认显示完整**，不截断不压缩。要窄表格加 `--short`（压成相对扫描根的形式并截断）。导出一律是完整路径。
- **`--limit` 在折叠之后生效**，所以不会出现"90 个仓库的目录"被显示成 4 个的情况。
- 排序字段：`path`（默认）/ `commit` / `name` / `group` / `status` / `size`，配合 `--desc` 反向。

`--json` 的输出可以直接喂给别的脚本：

```cmd
grscan list --dirty --json > dirty.json
```

---

## 5. 空间视图：tree

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
- 只有一层子节点的目录会自动合并，不会出现一条拖到底的无意义链
- 过滤参数和 `list` 完全一样

不带 `--brief` 时，叶子会把每个仓库连同分支、改动、时间、分组、状态和完整路径列出来。

典型用法：

```cmd
grscan tree --brief --depth=2        :: 先看格局
grscan tree --status=new             :: 还没处理的都散落在哪些目录
grscan tree --dirty                  :: 改动集中在哪
```

---

## 6. 交互判别：triage

全屏交互面板，逐个（或整组）判别。

```cmd
grscan triage
grscan triage --filter=ryxl          :: 只显示路径/名字/分组/标签含 ryxl 的
grscan triage --status=done          :: 打开时就进入「全部」视图
grscan triage --status=ignored       :: 打开时就进入「已忽略」视图
```

> **必须在真实终端里跑**（PowerShell 或 Windows Terminal）。
> 不要在 Git Bash 里管道重定向，也别指望脚本能驱动它——它是全屏交互程序。

### 6.1 界面

```
 grscan triage   视图[待处理]  共 199  已选 0  ✓ 已同步
   NAME      BRANCH    CHG   SYNC   LAST    GROUP  PATH
   > grscan     main      -    ok    3天前   -      …\grscan
     firmware   dev *     3改  ↑2    2月前   工作   …\firmware

   D:\work\grscan | 待处理 | 无远端
 ↑↓ 移动 · 空格 勾选 · d 已处理 · q 退出保存 · ? 帮助 · G 分组 · v 视图 · F 过滤
   没勾选时操作只作用于当前行 · 空格可勾选多个一起改 · ? 看全部快捷键
```

底部那行**键位条常驻**，会跟着状态变：

- 有勾选 → 换成"作用于已选项"的键：`已选 3 项 → d 已处理 · Esc 取消选择 · G 分组 · …`
- 列表为空 → 换成脱身动作：`v 切换视图 · c 清除过滤 · ? 帮助 · q 退出`
- 窗口变窄自动丢掉靠后的键，重要的（移动、勾选、`d`、`q`）永远留着

### 6.2 最重要的一条规则

> **有勾选的行 = 操作作用于全部勾选行；没勾选 = 只作用于光标所在行。**

勾一个折叠起来的目录，等于勾它底下全部仓库。

### 6.3 快捷键

按 `?` 呼出完整版：

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

注意点：

- **`A` 全选，`a` 取消选择**，大小写含义相反。
- **`G` / `T` / `N` / `F` 会进入输入状态**，此时字母键都是输入内容，**按 Enter 确认或 Esc 取消**。底部会提示。
- **按 `d` 的行立刻从"待处理"视图消失**，适合从头往下快速清场。
- **列表按最近提交时间倒序**，磁盘上已删除的沉到最后。
- **`e` 是会话内临时折叠**，不写盘；要持久用 `grscan fold <目录>`。
- **`Ctrl+C` 直接退出，不保存**；`q` 有改动时自动保存，`Ctrl+S` 随时保存。顶部会显示 `● 未保存` / `✓ 已同步`。

---

## 7. 批量标记：set

不想进面板时用命令行批量改：

```cmd
grscan set myproject --group=归档 --status=done --yes
grscan set D:\repos\old --status=archived --note="2024 年前的实验"
grscan set proj-a proj-b --tag=待推远端           :: 多个关键字
grscan set proj-a --untag=待推远端
grscan set proj-a --clear-group --clear-tags --clear-note
grscan set proj-a --ignore
grscan set proj-a --unignore
```

**关键字的匹配规则**（`state.Find`）：

1. 先找完整 ID 或精确名字，命中就只返回这些
2. 否则找 ID 片段 / 路径片段，可能匹配到多个

所以 `grscan set ryxl --group=工作` 会把所有路径含 `ryxl` 的都改掉。**匹配多个时会先列出将要修改的清单让你确认**（加 `--yes` 跳过），动手前看一眼。

`--status` 可选：`new` / `active` / `done` / `archived`，或中文 `待处理` / `跟进中` / `已处理` / `已归档`。

---

## 8. 目录级规则：hide / fold

```cmd
grscan hide  D:\repos\ryxl_rd                     :: 这块彻底不看
grscan hide  D:\tmp --note="临时克隆，不用管"      :: 加备注说明为什么
grscan fold  D:\repos\ryxl_rd                     :: 这块只留一行，知道有多少就行
grscan unhide D:\repos\ryxl_rd
grscan unfold D:\repos\ryxl_rd
grscan dirs                                       :: 看看现在设了什么
```

执行后会告诉你这条规则盖住了多少仓库。`dirs` 的输出：

```
已隐藏（完全不出现，扫描跳过）:
  D:\repos\ryxl_rd                    90 个仓库  临时克隆，不用管
已折叠（收拢成一行）:
  D:\repos\backup                     12 个仓库
```

规则写在 `state.json` 的 `hidden` / `folded` 字段里，格式是"目录路径 + 时间 + 备注"，可以直接手改。

在面板里按 `e` 可以做**会话内**的临时折叠/展开，不影响这些持久规则。

---

## 9. 找重复与查统计：dupes / stats

### dupes：按远端地址找重复克隆

```cmd
grscan dupes
grscan dupes --all        :: 把已忽略的也算进来
```

```
× 3  https://github.com/foo/bar.git
    2年前   D:\work\bar
    8月前   D:\repos\bar
    3天前   D:\tmp\bar-checkout
```

没有配置远端的仓库按 `local:<名字>` 归并。同一仓库克隆多份，多半可以只留一份。

### stats：概览

```cmd
grscan stats
```

给出清单总数、待处理数、状态分布、分组分布、类型分布，以及"有未提交改动 / 有未推送提交 / 无远端配置"三个计数和上次扫描记录。

---

## 10. 导出：export

```cmd
grscan export --format=md      -o repos.md    :: Markdown 报告（默认）
grscan export --format=outline -o tree.md     :: 只有目录树骨架
grscan export --format=csv     -o repos.csv   :: 带 BOM，Excel 直接开
grscan export --format=json    -o repos.json  :: 原始结构化数据
grscan export --format=md --flat -o table.md  :: 退回平铺表格
```

不给 `-o` 就打到标准输出，可以接管道：`grscan export --format=json | jq ...`

### md 报告的构成

1. **概览** —— 各状态数量、分组分布
2. **值得先看的几类** —— 把动手的理由挑出来：磁盘上已找不到 / 有未提交改动 / 有未推送的提交 / 没有配置远端 / 一年以上没提交
3. **按目录** —— 先给一棵目录骨架定位，再按规模合适的目录分小节列表格

分节不会一个目录一节地铺满全文：超过 25 个仓库才会继续下钻，而且如果底下全是单仓库目录就直接整节列出。

### 只导出一部分

**默认导出全集**（"给我一份完整清单"通常是这个意思），但给了过滤条件就只导那一批：

```cmd
grscan export --dirty      -o 未提交改动.md
grscan export --no-remote  -o 纯本地.md          :: 删了就没了
grscan export --unpushed   -o 未推送.md
grscan export --kind=bare --format=csv -o 裸仓.csv
grscan export --query=ryxl -o ryxl.md
```

`--format=json` 输出的东西足够全，想拿它喂给别的脚本或者让 AI 接着分析都行。

---

## 11. 配置：config

```cmd
grscan config show               :: 看当前配置和数据目录位置
grscan config init               :: 重建默认配置（已存在需 --force）
grscan config discover           :: 探查各盘，推荐可能含仓库的目录
grscan config add-root D:\repos
grscan config rm-root  D:\repos
grscan config add-exclude node_modules
grscan config rm-exclude AppData
grscan config set jobs=24 max-depth=6 fast=true
```

### discover：不知道仓库在哪时用

```cmd
grscan config discover                       :: 只读两层，列出候选目录和仓库数
grscan config discover --max-depth=3         :: 挖深一点
grscan config discover --apply --min=2       :: 含 2 个以上仓库的目录全部加进扫描根
```

`--apply` 会直接改配置，先看清楚列表再用。

### config.json

文件在 `C:\Users\<你>\.grscan\config.json`：

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
| `roots` | 扫描根目录。不递归全盘，只扫这里列出的 |
| `exclude_names` | 命中目录名就整棵跳过，支持 `*`、`?` 通配 |
| `exclude_paths` | 按路径前缀跳过 |
| `max_depth` | 从根往下最多钻几层，默认 10 |
| `jobs` | 并发 worker 数，默认取 CPU 核数（上限 16） |
| `fast` | true 则完全不起 git 进程，拿不到未提交改动 |
| `deep` | 默认找到仓库就停止下钻；true 则继续找嵌套仓库 |
| `git_exe` | git 不在 PATH 时手动指定路径 |

它是纯文本，直接编辑保存，下条命令就生效。

> `AppData` 默认在排除列表里。如果你确实把仓库放到了 `AppData` 下面，跑 `grscan config rm-exclude AppData` 再扫。

---

## 12. 杂项：open / rm / prune

```cmd
grscan open myproject        :: 资源管理器打开（多个匹配取第一个）
grscan rm myproject          :: 从清单移除记录，不动磁盘
grscan prune                 :: 清理磁盘上已不存在且未忽略的记录
grscan prune --dry-run       :: 先看会清掉什么
```

- `rm` 只删清单里的记录，磁盘上的文件一个都不动。想让某个目录再也不出现，用 `hide` 更合适——`rm` 之后下次扫描它还会回来。
- `prune` 不清理标了 `ignored` 的记录（忽略是主动决定，不能顺手清掉）。

---

## 13. 列的含义

`list` / `tree` / 面板共用同一套列：

| 列 | 含义 |
|---|---|
| 首列 | 状态标记：`N` 待处理 / `A` 跟进中 / `D` 已处理 / `Z` 已归档 / `I` 已忽略 / `F` 已折叠目录 |
| NAME | 目录名 |
| BRANCH | 当前分支。分离的 HEAD 标 `(分离)`，有未提交改动时后面带 `*` |
| CHG | 未提交改动。`3` = 3 个已跟踪文件改动；`12?` = 12 个未跟踪；`3+12?` = 两者都有；`-` = 干净 |
| SYNC | 与上游的关系。`同步` / `^2` 领先 2 个提交 / `v3` 落后 3 个 / `-` 没有上游 |
| LAST | 距最近一次提交多久 |
| GROUP | 分组，`-` 表示未分组 |
| PATH | 路径。磁盘上找不到的会标 `[已删除]` |

类型（Kind）：`repo` 普通 / `bare` 裸仓 / `worktree` 工作树 / `submodule` 子模块。

---

## 14. 典型工作流

### A. 第一次大规模清理

```cmd
grscan config discover                    :: 找仓库在哪
grscan config add-root D:\repos           :: 加扫描根
grscan scan                               :: 扫
grscan tree --brief --depth=2             :: 看格局，决定哪些块整块处理
grscan hide D:\repos\vendor               :: 明确不要的整块隐藏
grscan fold D:\repos\backup               :: 只要知道有多少的整块折叠
grscan triage                             :: 剩下的逐个过一遍
```

关键：**先缩规模再逐个看**。199 个仓库从头清到尾不现实，先用 `tree` 找出成块的目录整块处理，剩下的往往只有几十个。

### B. 只关心有改动的

```cmd
grscan list --dirty                       :: 看哪些有未提交改动
grscan tree --dirty                       :: 看改动集中在哪些目录
grscan export --dirty -o 未提交改动.md     :: 导出一份拿去想
```

### C. 找出可以删的

```cmd
grscan dupes                              :: 同一远端克隆了多份
grscan list --no-remote                   :: 没有任何远端，删了就没了
grscan list --missing                     :: 磁盘上已经找不到的
grscan prune --dry-run                    :: 确认后再清理记录
```

### D. 定期巡检

```cmd
grscan scan --fast                        :: 快扫，摸底
grscan stats                              :: 看数字变化
grscan triage                             :: 只处理新增的（已处理的不出现）
```

重扫不会丢结论，所以可以随时扫。

### E. 导出报告

```cmd
grscan export --format=md -o repos.md     :: 拿给人看
grscan export --format=json -o repos.json :: 喂给脚本或 AI
```

---

## 15. 常见问题

**Q：`list` 什么都不显示？**

默认只看"待处理"。要么已经处理完了（加 `--all`），要么还没扫（先 `scan`），要么都被 `hide` 了（跑 `grscan dirs` 看看）。

**Q：`triage` 进去是空的？**

同样先确认视图。按 `v` 在 待处理 → 全部 → 已忽略 之间循环；按 `F` 检查是不是还留着过滤条件（按 `c` 清除）。

**Q：按了 `G` 之后所有键都失灵？**

进了输入状态。按 Enter 确认或 Esc 取消。底部有提示。

**Q：改了一堆，退出后没保存？**

`Ctrl+C` 是不保存退出的。`q`（有改动时自动保存）或 `Ctrl+S` 才写盘。看顶部是 `✓ 已同步` 还是 `● 未保存`。

**Q：扫描太慢？**

先 `--fast`（不起 git 进程）摸底。仓库特别多时把明确不要的目录 `hide` 掉——隐藏目录在扫描时整棵跳过，成本为零。

**Q：改了的状态会不会被下次扫描冲掉？**

不会。扫描只覆盖事实部分（分支、提交、改动数），分组/标签/状态/备注原样保留。

**Q：某个仓库被误标成"磁盘已找不到"？**

`missing` 只在本次扫描根范围内判定。如果你只扫了 `D:\`，`E:\` 上的旧记录不会被误标。确认真的没了再 `prune`。

**Q：仓库放在 AppData 下面扫不到？**

`AppData` 默认在排除列表里。跑 `grscan config rm-exclude AppData` 再扫。

**Q：数据想挪到别的地方？**

```cmd
grscan --home D:\my\grscan scan
```

或者设环境变量 `GRSCAN_HOME`。都不设就用 `~/.grscan`。

**Q：想恢复初始状态？**

把 `~/.grscan` 整个删掉，下次运行会重新生成默认配置。

---

## 16. 命令速查

| 命令 | 作用 |
|---|---|
| （不带命令） | 进会话，提示符带着当前作用域，`exit` 退出 |
| `cd <作用域>` / `cd ..` / `cd /` / `cd -` | 走进去（会话内有效）/ 上一层 / 回全集 / 回到来路 |
| `ls` | 列当前作用域的**子项**（导航用，不受"只看待处理"过滤） |
| `pwd` | 打印当前作用域字符串 |
| `scan` | 扫配置的根目录，结果并入清单 |
| `list` | 列仓库，默认只列**待处理**（在当前作用域里筛） |
| `tree` | 按磁盘目录树展开（只看当前作用域这批） |
| `triage`（`tui`） | 交互面板，逐个判别（只处理当前作用域这批） |
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

### 通用选项

| 选项 | 适用 | 说明 |
|---|---|---|
| `--home <dir>` | 全局 | 指定数据目录（也可设 `GRSCAN_HOME`），默认 `~/.grscan` |
| `--in=<作用域>` | 全局 | 只对这一条命令临时指定范围，例: `--in=file:D:\work list` |
| `--all` | list / tree | 全部（含已忽略、磁盘已无） |
| `--ignored` `--missing` | list / tree | 只要这一类 |
| `--dirty` `--clean` | list / tree | 有无未提交改动 |
| `--unpushed` | list / tree | 有未推送提交 |
| `--no-remote` | list / tree | 没配远端 |
| `--group=` `--tag=` `--status=` | list / tree | 按分组/标签/状态 |
| `--query=` | list / tree | 路径或名字包含 |
| `--kind=` | list / tree | repo / bare / worktree / submodule |
| `--sort=` `--desc` | list | 排序字段与方向 |
| `--limit=N` | list | 最多显示 N 条 |
| `--width=N` | list / tree | 表格宽度（默认 120） |
| `--short` | list | 压缩路径显示 |
| `--json` | list | 结构化输出 |
| `--brief` `--depth=N` | tree | 只看骨架 / 展开层数 |
| `--root=` `--fast` `--deep` `--size` `--jobs=` `--max-depth=` | scan | 扫描行为 |
| `--filter=` `--status=` | triage | 初始过滤 / 初始视图 |
