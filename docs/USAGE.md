# igit 使用手册

扫出机器上散落各处的 git 仓库，然后按你自己的判断分组、标记、归档——处理完的不再打扰你。

单个 exe，无运行时依赖。数据就是两个纯文本 JSON 文件，随手可拷可迁。

> 这份手册按功能组织，覆盖全部命令与参数。
> 快速概览与设计背景见 [README](../README.md)。

---

## 目录

- [1. 一分钟上手](#1-一分钟上手)
- [2. 六个必须先懂的概念](#2-六个必须先懂的概念)
  - [2.1 扫描的事实 vs 你的结论](#21-扫描的事实-vs-你的结论)
  - [2.2 四个状态 + 忽略](#22-四个状态--忽略)
  - [2.3 分组、标签、备注](#23-分组标签备注)
  - [2.4 hide 与 fold：对一整块目录表态](#24-hide-与-fold对一整块目录表态)
  - [2.5 两个视角：清单 vs 空间](#25-两个视角清单-vs-空间)
  - [2.6 作用域与来时路：在哪就管哪，怎么来的怎么回](#26-作用域与来时路在哪就管哪怎么来的怎么回)
- [3. 扫描：scan](#3-扫描scan)
- [4. 清单视图：list](#4-清单视图list)
- [5. 空间视图：tree](#5-空间视图tree)
- [6. 交互判别：triage](#6-交互判别triage)
- [7. 标记与分类维护：group / tag / status / note / set](#7-标记与分类维护group--tag--status--note--set)
- [8. 目录级规则：hide / fold](#8-目录级规则hide--fold)
- [9. 查重与统计：dupes / stats](#9-查重与统计dupes--stats)
- [10. 导出：export](#10-导出export)
- [11. 配置：config](#11-配置config)
- [12. 联动与打开：open / tig / rm / prune](#12-联动与打开open--tig--rm--prune)
- [13. 表格列的含义](#13-表格列的含义)
- [14. 典型工作流](#14-典型工作流)
- [15. 常见问题](#15-常见问题)
- [16. 命令速查](#16-命令速查)

---

## 1. 一分钟上手

```cmd
rem 1) 探查各盘，看哪些目录里仓库密集（只读两层，很快）
igit config discover

rem 2) 把看着靠谱的目录加进扫描根，可重复执行
igit config add-root D:\repos
igit config add-root D:\work

rem 3) 正式扫描
igit scan

rem 4) 交互式判别处理
igit triage
```

之后日常只需要两步：`igit scan` 更新事实，`igit triage` 或 `igit list` 看剩下的。

**建议先固定 exe 位置**，免得路径变来变去：

```cmd
mkdir "%LOCALAPPDATA%\igit"
copy igit.exe "%LOCALAPPDATA%\igit\"
```

之后用 `%LOCALAPPDATA%\igit\igit.exe <命令>`，或把这个目录加进系统 PATH。

---

## 2. 六个必须先懂的概念

### 2.1 扫描的事实 vs 你的结论

每条记录分两半：

| | 内容 | 谁在改 |
|---|---|---|
| **事实** | 路径、分支、最近提交、改动数、远端地址、体积 | 每次 `scan` 整体覆盖刷新 |
| **结论** | 分组、标签、状态、忽略、备注 | 只有你自己用命令或面板能改 |

**所以可以随便重扫，已经打好的分组和状态绝对不会丢。** 这是整个工具的地基。

### 2.2 四个状态 + 忽略

| 状态 | 含义 | 建议用法 |
|---|---|---|
| `new` 待处理 | 还没看过 | 默认视图只显示这一类 |
| `active` 跟进中 | 看了，手上还在改 | 正在攻关的项目 |
| `done` 已处理 | 处理完了 | 暂告一段落，不再打扰 |
| `archived` 已归档 | 留着备查，平时不看了 | 老项目或参考库 |
| `ignored` 已忽略 | 不当作关心的仓库看待 | 下次扫描在父目录直接跳过 |

状态是**自由容器**，工具不规定"已处理"具体意味着什么，四个状态怎么用全由你定。

`--status` 参数支持中文别名：`待处理 / 跟进中 / 已处理 / 已归档 / 已忽略`。

### 2.3 分组、标签、备注

- **分组**：一个仓库只能有一个（`G` 键 / `group <名>` / `set --group=`），适合项目归属、业务线、客户名。
- **标签**：可以有多个（`T` 键 / `tag add <名>` / `set --tag=a,b`），适合打属性，比如"待删"、"要推远端"、"依赖老环境"。
- **备注**：一句话说明（`N` 键 / `note <内容>` / `set --note=`），写清楚为什么这么处理。

在面板里输入分组或标签时，底部会提示已有候选，避免手滑输错。

### 2.4 hide 与 fold：对一整块目录表态

逐个仓库打标记太慢，人真正的想法往往是"这块我不看"或者"这块我知道有一堆就行"。

| | `hide` 隐藏 | `fold` 折叠 |
|---|---|---|
| 清单与树中 | 完全不出现，`--all` 也不出现 | 收拢成一行，显示总数和待处理数 |
| 扫描时 | 整棵跳过，连目录读取都不做 | 照常扫描（统计数量和改动） |
| 新克隆进来的仓库 | 自动一并隐藏 | 自动一并折叠 |
| 导出报告 | 不出现 | 汇总为一行，不逐条展开 |
| 交互面板里 | 不出现 | 占一行，勾选它等于勾选底下全部 |

新克隆进来的仓库自动继承规则——这是目录级规则相比逐个打标最大的好处。

### 2.5 两个视角：清单 vs 空间

- `list` 回答"有哪些"（按名字、时间或分组排序的平面清单）。
- `tree` 回答"它们怎么分布"（按磁盘目录树展开）。

判断"这块要不要整块处理"必须用 `tree`，光看清单看不出仓库在磁盘上是怎么聚集的。

### 2.6 作用域与来时路：在哪就管哪，怎么来的怎么回

本地放了几十上百个仓库后，总在全盘大清单里翻找会很累。`igit` 让你能直接走进某个目录、分组或单仓里工作。

#### 1. 怎么移动（游走导航）

直接运行 `igit` 进入交互会话：

```cmd
igit                                       :: 进入会话
igit [root · 190 个仓库] > ls               :: 看看当前底下有哪些目录或分组
igit [root · 190 个仓库] > cd work          :: 走进 work 目录
igit [file:D:\work · 85 个仓库] > cd backend :: 走进 backend 仓库
igit [file:D:\work] -> [repo:backend] > pwd
当前节点: 仓库 backend
来时路: [file:D:\work] -> [repo:backend]

igit [file:D:\work] -> [repo:backend] > cd ..   :: 原路返回上一级目录
igit [file:D:\work · 85 个仓库] > cd -          :: 切换回刚才的 backend 仓库
igit [file:D:\work] -> [repo:backend] > cd /    :: 一键回到全局根
```

- **来时路提示**：提示符清楚显示你是从哪一步进来的（如 `[file:D:\work] -> [repo:backend]`）。
- **原路返回**：`cd ..` 返回上一层，`cd -` 在最近两个位置来回切，`cd /` 回到全局。
- **内存生命周期**：会话位置只在当前终端内存里有效，`exit` 退出即销毁，不改系统当前工作目录，也不写磁盘配置。
- **单次指定**：在外部命令也可以用 `--in` 单次限定，如 `igit --in=group:工作 list`。

#### 2. 停在哪里，命令就对哪里生效

1. **如果你走进了单个仓库**：
   后面的命令**不用再重复敲仓库名字**，直接对它生效：
   - `tig`：直接新开窗口用 tig 审阅这个仓库的提交与改动。
   - `open`：直接在资源管理器里打开该仓库的物理目录。
   - `stats`：直接查看这个仓库的完整属性（分支、未提交文件、最新提交、体积）。
   - `dupes`：精准排查这个仓库在全盘其他地方是否有重复克隆。
   - `group <组名>` / `group clear`：就地划分分组或移出分组。
   - `tag add <标签>` / `tag rm <标签>`：就地打标签或摘标签。
   - `status <状态>` / `note <备注>`：更新处理结论或记下备忘。
   - `scan`：原地重新检测这一个仓库的最新状态。

2. **如果你走进了某个目录、分组或标签**：
   后面的命令**自动收敛在当前这批仓库里**：
   - `list` / `tree`：只列当前这批仓库，或只展开这块的目录树。
   - `stats`：统计当前这批仓库的数字。
   - `triage`：在当前这批仓库中进行全屏逐个分类。
   - `export`：只导出当前这批仓库的报告。
   - `scan`（在目录下）：只重新扫描这个物理目录，局部刷新极快。

3. **全局查阅命令随时能用**：
   不管你当前停在多深的目录下，这些查询命令随时可用：
   - `group`：查看全盘所有分组及各包含多少仓库。
   - `group new <名>` / `group rename <旧> <新>`：新建或重命名分组。
   - `tag`：查看全盘打过的所有标签及使用频次。
   - `dirs`：查看当前设置的所有隐藏与折叠目录规则。

---

## 3. 扫描：scan

```cmd
igit scan
igit scan --root=D:\repos --root=E:\work    :: 临时指定目录，可重复，不写进配置
igit scan --fast                            :: 不起 git 进程，只读 .git，快很多
igit scan --size                            :: 统计仓库体积（会慢不少）
igit scan --deep                            :: 命中仓库后仍继续找嵌套仓库
igit scan --jobs=24                         :: 并发 worker 数
igit scan --max-depth=6                     :: 目录最多下钻几层
```

输出示例：

```
扫描完成，用时 3.2s
  遍历目录   18420
  扫到仓库   199（新增 0）
  已忽略跳过 12
  磁盘已无   3（运行 igit prune 清理）
  清单总数   199，其中待处理 199
```

要点：

- **`--fast` 拿不到未提交改动**。默认会起 git 进程问一次工作区状态，`--fast` 跳过这步，只读 `.git` 元数据。机器慢或仓库多时先 `--fast` 摸底，再完整扫一次。
- **`--size` 只在本次扫描统计体积**，之前统计过的值会保留（`scan` 不会把已有的体积清零）。
- **"磁盘已无"只在扫描范围内判定**。只扫 `D:\` 时不会把 `E:\` 上的旧记录误标成丢失；标了忽略的仓库也不算丢失。
- **`hidden` 目录在扫描时整棵跳过**，成本为零；`folded` 目录照常扫。
- **局部原位刷新**：
  - 在**单仓节点**（`[repo:xxx]`）下敲 `scan`：直接就地重新检测当前单仓的工作区与分支状态，不遍历其他任何目录；
  - 在**物理目录节点**（`[file:D:\work]`）下敲 `scan`：只扫描该目录子树，刷新极快。
- 每次扫描会往 `state.json` 的 `scans` 里留一条流水（最多保留 50 条），可以回头看历史。

---

## 4. 清单视图：list

```cmd
igit list                        :: 默认只看「待处理」
igit list --all                  :: 全部（含已忽略、磁盘已无）
igit list --dirty                :: 有未提交改动的
igit list --clean                :: 没有未提交改动的
igit list --unpushed             :: 有未推送提交的
igit list --no-remote            :: 没配远端的（删了就没了）
igit list --missing              :: 磁盘上找不到的
igit list --ignored              :: 已忽略的
igit list --group=工作           :: 按分组
igit list --tag=待删             :: 按标签
igit list --status=active        :: 按状态
igit list --query=ryxl           :: 路径或名字包含
igit list --kind=bare            :: 按类型（repo/bare/worktree/submodule）
igit list --sort=commit --desc   :: 排序
igit list --limit=30             :: 最多显示 N 条
igit list --short                :: 压缩路径显示
igit list --json                 :: 结构化输出
```

几个容易踩的点：

- **默认只看"待处理"**。这是刻意的设计：清场时你只想看还没处理的。想看全部加 `--all`。
- **单仓节点直接出单行详情**：停在具体仓库节点时，敲 `list` 直接输出该单仓的明细表格行，无需指定参数。
- **`--missing` / `--ignored` 是"只要这一类"的强开关**，不再叠加"待处理"过滤。`--dirty` 之类则是在待处理里再筛。
- **路径默认显示完整**，不截断不压缩。要窄表格加 `--short`（压成相对扫描根的形式并截断）。导出一律是完整路径。
- **`--limit` 在折叠之后生效**，所以不会出现"90 个仓库的目录"被显示成 4 个的情况。
- 排序字段：`path`（默认）/ `commit` / `name` / `group` / `status` / `size`，配合 `--desc` 反向。

`--json` 的输出可以直接喂给别的脚本：

```cmd
igit list --dirty --json > dirty.json
```

---

## 5. 空间视图：tree

```cmd
igit tree --brief --depth=2
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

- `--brief` 只看骨架和数量，不逐个列仓库。
- `--depth=N` 展开到第 N 层，超出的折叠成一行摘要。
- 只有一层子节点的目录会自动合并，不会出现一条拖到底的无意义链。
- 过滤参数和 `list` 完全一样。

典型用法：

```cmd
igit tree --brief --depth=2        :: 先看整体格局
igit tree --status=new             :: 还没处理的都散落在哪些目录
igit tree --dirty                  :: 未提交的改动都集中在哪
```

---

## 6. 交互判别：triage

全屏交互面板，适合坐下来一口气逐个或成批整理。

```cmd
igit triage
igit triage --filter=ryxl          :: 只显示路径/名字/分组/标签含 ryxl 的
igit triage --status=done          :: 打开时就进入「全部」视图
igit triage --status=ignored       :: 打开时就进入「已忽略」视图
```

> **注意**：必须在真实终端里跑（PowerShell、CMD 或 Windows Terminal）。不要在 Git Bash 里管道重定向。

### 6.1 界面示例

```
 igit triage   视图[待处理]  共 199  已选 0  ✓ 已同步
   NAME      BRANCH    CHG   SYNC   LAST    GROUP  PATH
   > igit       main      -    ok    3天前   -      …\igit
     firmware   dev *     3改  ↑2    2月前   工作   …\firmware

   D:\work\igit | 待处理 | 无远端
 ↑↓ 移动 · 空格 勾选 · d 已处理 · q 退出保存 · ? 帮助 · G 分组 · v 视图 · F 过滤
   没勾选时操作只作用于当前行 · 空格可勾选多个一起改 · ? 看全部快捷键
```

### 6.2 规则与快捷键

> **有勾选的行 = 操作作用于全部勾选行；没勾选 = 只作用于光标所在行。**

勾一个折叠起来的目录，等于勾它底下全部仓库。

按 `?` 呼出完整快捷键表：

```
对象   有勾选的行 = 作用于全部勾选行；没勾选 = 只作用于光标所在行
移动   ↑/k  ↓/j  PgUp/PgDn  Home/End
多选   空格 选择/取消     A 全选    a 取消选择   Esc 清空选择
处理   S 循环状态        d 已处理  w 跟进中  z 已归档  u 退回待处理
归类   G 分组           T 标签    N 备注    x 清除分组
忽略   I 忽略/取消忽略
折叠   e 折叠/展开光标所在目录（临时，不写盘）
视图   v 待处理/全部/已忽略      F 过滤路径   c 清除过滤
查看   g 用 tig 打开当前仓库（新窗口）  o 资源管理器打开  r 查看详情
保存   Ctrl+S           q 退出并保存        Ctrl+C 放弃改动退出
```

- **按 `d` 的行立刻从「待处理」视图消失**，适合从头往下快速清场。
- **`G` / `T` / `N` / `F` 会进入输入状态**，按 Enter 确认，按 Esc 取消。
- **`Ctrl+C` 放弃未保存改动直接退出**；`q` 会自动保存后退出。顶部会显示 `● 未保存` 或 `✓ 已同步`。

---

## 7. 标记与分类维护：group / tag / status / note / set

### 7.1 单仓就地维护（停在单仓节点时）

当你 `cd` 进某个具体仓库节点时，当前仓库就是默认操作对象，直接打命令即可：

```cmd
rem 分组归类与清除
group 核心业务             :: 将当前仓库划入"核心业务"分组（不存在则自动创建）
group clear                :: 清除当前仓库的分组

rem 标签追加与摘除
tag add 待推远端           :: 追加标签
tag rm 待推远端            :: 摘除标签

rem 处理状态流转
status done                :: 标记为已处理（可选 new / active / done / archived / ignored）
status clear               :: 重置状态回待处理（new）

rem 备忘速记
note 需等待联调上线         :: 记录一句话备注
note clear                 :: 清空备注
```

### 7.2 全局分类查阅（任何位置随时可用）

```cmd
group                      :: 列出全盘所有分组及其仓库数量
group new 基础设施         :: 预先创建一个空分组
group rename 临时 待归档   :: 全局重命名分组

tag                        :: 列出全盘所有打过的标签及用量
```

### 7.3 命令行批量标记：set

需要在脚本中执行，或者一次性修改多个仓库时：

```cmd
igit set myproject --group=归档 --status=done --yes
igit set D:\repos\old --status=archived --note="2024 年前的实验"
igit set proj-a proj-b --tag=待推远端           :: 多个关键字同时指定
igit set proj-a --untag=待推远端
igit set proj-a --clear-group --clear-tags --clear-note
igit set proj-a --ignore
igit set proj-a --unignore
```

**关键字匹配规则**：
1. 先找完整 ID 或精确名字，命中就只返回这些；
2. 否则按路径片段模糊匹配。命中多个时会先列出待修改清单要求你输入 `y` 确认（加 `--yes` 跳过确认）。

---

## 8. 目录级规则：hide / fold

```cmd
igit hide  D:\repos\ryxl_rd                     :: 这块彻底不看
igit hide  D:\tmp --note="临时克隆，不用管"      :: 加备注说明为什么
igit fold  D:\repos\ryxl_rd                     :: 这块只留一行，知道有多少就行
igit unhide D:\repos\ryxl_rd
igit unfold D:\repos\ryxl_rd
igit dirs                                       :: 看看现在设了什么
```

- **相对路径按当前位置解析**：在 `file:D:\repos` 下敲 `hide ryxl_rd` 等于 `hide D:\repos\ryxl_rd`。
- 如果这条规则盖住 0 个仓库，**不会保存**，避免手滑输错留下无效规则。
- 规则保存在 `~/.igit/state.json` 中，后续新克隆进该目录的仓库自动生效。

---

## 9. 查重与统计：dupes / stats

### dupes：查找重复克隆

#### 1. 全盘或当前集合查重
```cmd
igit dupes
igit dupes --all        :: 把已忽略的也算进来
```

```
× 3  https://github.com/foo/bar.git
    2年前   D:\work\bar
    8月前   D:\repos\bar
    3天前   D:\tmp\bar-checkout
```

没有配置远端的仓库按本地名字归并。同一仓库在不同地方克隆了多份，多半可以清理多余的副本。

#### 2. 单仓精准查重（停在单仓节点时）
停在具体仓库节点时，敲 `dupes` 会**直接排查当前这个仓库在全盘其他地方是否有相同克隆**：
- 如果别处有同源克隆，直接把那些路径和提交时间列出来比对；
- 如果没有，提示 `当前仓库在清单中无重复副本`。

### stats：统计概览与单仓详情

#### 1. 范围概览（在根节点或目录/分组下）
```cmd
igit stats
```
给出当前范围内的仓库总数、待处理数、状态分布、分组分布、类型分布，以及有改动/未推送/无远端三项重要线索的计数。

#### 2. 单仓详情（停在单仓节点时）
停在单仓节点时敲 `stats`，直接打印当前单仓的**详细属性**：
- 仓库名、绝对路径、类型（普通/裸仓/子模块）；
- 当前分支、HEAD 状态、最新提交时间、提交者与说明；
- 未提交改动明细（暂存/未暂存/未跟踪文件数）、远端地址与同步状态；
- 当前状态、所属分组、标签、备注；
- 磁盘物理体积（若已扫描）。

---

## 10. 导出：export

```cmd
igit export --format=md      -o repos.md    :: Markdown 报告（默认）
igit export --format=outline -o tree.md     :: 只有目录树骨架
igit export --format=csv     -o repos.csv   :: 带 BOM，Excel 直接打开
igit export --format=json    -o repos.json  :: 原始结构化数据
igit export --format=md --flat -o table.md  :: 平铺表格
```

不给 `-o` 就打印到标准输出，可接管道处理：`igit export --format=json | jq ...`

### Markdown 报告构成
1. **概览** —— 状态汇总、分组分布。
2. **值得先看** —— 挑出重点关注项：磁盘丢失、未提交改动、未推送提交、无远端、一年未提交。
3. **按目录分节** —— 目录骨架 + 各目录明细表格。

### 导出部分内容
带过滤参数就只导出对应的那一批：

```cmd
igit export --dirty      -o 未提交改动.md
igit export --no-remote  -o 纯本地.md
igit export --unpushed   -o 未推送.md
igit export --query=ryxl -o ryxl.md
```

---

## 11. 配置：config

```cmd
igit config show               :: 查看当前配置与数据目录位置
igit config init               :: 重建默认配置
igit config discover           :: 探查各盘，推荐含仓库的目录
igit config add-root D:\repos  :: 添加扫描根目录
igit config rm-root  D:\repos  :: 移除扫描根目录
igit config add-exclude node_modules :: 添加排除目录名
igit config rm-exclude AppData       :: 移除排除目录名
igit config set jobs=24 max-depth=6 fast=true :: 修改配置参数
```

### discover：不知道仓库散在哪时用
```cmd
igit config discover                       :: 只看两层，推荐候选目录
igit config discover --max-depth=3         :: 挖深一层
igit config discover --apply --min=2       :: 仓库数 >= 2 的目录自动加进扫描根
```

### 配置文件位置
文件在 `~/.igit/config.json`（纯文本，直接修改也能生效）：

```json
{
  "version": 1,
  "roots": ["D:\\repos", "D:\\work"],
  "exclude_names": ["node_modules", "vendor", "$Recycle.Bin", "AppData"],
  "exclude_paths": [],
  "max_depth": 10,
  "jobs": 16,
  "fast": false,
  "deep": false,
  "git_exe": "",
  "tig_exe": "",
  "term_cmd": ""
}
```

---

## 12. 联动与打开：open / tig / rm / prune

```cmd
rem 单仓节点下直接免参执行
open                         :: 资源管理器打开当前仓库目录
tig                          :: 弹窗用 tig 打开当前仓库并自动选开屏视图

rem 集合与全局下指定关键字
igit open myproject          :: 资源管理器打开
igit tig myproject           :: 新终端窗口用 tig 打开
igit rm myproject            :: 从清单移除记录（不动磁盘文件）
igit prune                   :: 清理磁盘上已不存在的废弃记录
igit prune --dry-run         :: 试运行，看会清理哪些
```

- **tig 自动挑选开屏视图**：有改动开在 `status` 页面；有未推送开在未推提交日志；无远端开在 `refs`；其他开在常规历史。
- **自定义终端**：想用 Windows Terminal 拉起 tig，执行 `igit config set term-cmd=wt.exe -d "{dir}" {cmd}`。

---

## 13. 表格列的含义

| 列 | 含义 |
|---|---|
| 首列 | 状态标记：`N` 待处理 / `A` 跟进中 / `D` 已处理 / `Z` 已归档 / `I` 忽略 / `F` 折叠目录 |
| NAME | 仓库目录名 |
| BRANCH | 当前分支。分离 HEAD 标 `(分离)`，有未提交改动末尾带 `*` |
| CHG | 未提交改动。`3` = 3个已跟踪改动；`12?` = 12个未跟踪；`3+12?` = 两者都有；`-` = 干净 |
| SYNC | 与上游关系。`同步` / `^2` 领先2个提交 / `v3` 落后3个 / `-` 无上游 |
| LAST | 距最后一次提交多久 |
| GROUP | 所属分组，`-` 表示未分组 |
| PATH | 物理路径。磁盘上找不到标 `[已删除]` |

---

## 14. 典型工作流

### A. 第一次摸底清理
```cmd
igit config discover                    :: 探查各盘
igit config add-root D:\repos           :: 加扫描根
igit scan                               :: 扫描
igit tree --brief --depth=2             :: 看目录骨架
igit hide D:\repos\vendor               :: 彻底不需要看的目录直接隐藏
igit fold D:\repos\backup               :: 只需看数量的目录直接折叠
igit triage                             :: 全屏面板逐个整理
```

### B. 下班前排查改动
```cmd
igit list --dirty                       :: 查看哪些仓库有未提交改动
igit tree --dirty                       :: 查看改动集中在哪些目录
```

### C. 清理磁盘空间
```cmd
igit dupes                              :: 排查重复克隆
igit list --no-remote                   :: 看看哪些仓库没配远端（别误删）
igit list --missing                     :: 看看哪些目录已经被删掉了
igit prune                              :: 清理无效记录
```

---

## 15. 常见问题

**Q：`list` 什么都不显示？**  
默认只看「待处理」。若都处理过了加 `--all` 查看；或确认是否执行过 `scan`。

**Q：按了 `G` 之后键盘字母键按了没反应？**  
进入了分组名称输入状态。输入完按 Enter 确认，按 Esc 取消。

**Q：按 `Ctrl+C` 退出后刚才改的分组丢了？**  
`Ctrl+C` 是放弃未保存更改。保存请按 `q` 或 `Ctrl+S`。

**Q：扫描比较慢？**  
加 `--fast` 参数跳过工作区改动检测；明确不需要看的庞大目录用 `hide` 屏蔽掉。

**Q：重扫会不会冲掉我打的标签和分组？**  
绝对不会。重新扫描只刷新 Git 仓库客观状态，你的分组、标签、状态、备注属于主观结论，原样保留。

---

## 16. 命令速查

| 命令 | 适用位置 | 说明 |
|---|---|---|
| （无参数） | 终端 | 运行 `igit` 进入交互会话，提示符携带当前来时路 |
| `cd <目标>` | 会话内 | 走进目录、仓库、分组（`group:x`）或标签（`tag:x`）；支持 `cd ..` / `cd -` / `cd /` |
| `ls` | 会话内 | 列出当前位置直属子项（用于浏览下钻） |
| `pwd` | 会话内 | 打印当前位置类型与完整来时路 |
| `scan` | 任意位置 | 全盘扫描；在目录下局部刷新；在单仓下原地重新检测 |
| `list` | 任意位置 | 查看仓库清单；单仓下直接输出该单仓明细行 |
| `tree` | 任意位置 | 按目录树查看空间分布 |
| `stats` | 任意位置 | 统计概览；**单仓下输出该仓库完整属性详情** |
| `dupes` | 任意位置 | 查找重复克隆；**单仓下精准查找全盘同源副本** |
| `triage` | 集合节点 | 全屏交互面板，在当前范围内逐个判别分类 |
| `open` | 任意位置 | 资源管理器打开；**单仓下免参直接打开** |
| `tig` | 任意位置 | 终端拉起 tig 查看历史；**单仓下免参直接打开** |
| `group` | 任意位置 | 列出所有分组；单仓下 `group <名>` 划分分组或 `group clear` 移除 |
| `tag` | 任意位置 | 列出所有标签；单仓下 `tag add <t>` 打标或 `tag rm <t>` 摘标 |
| `status` | 单仓节点 | 更新处置状态（`status <st>`）或清空（`status clear`） |
| `note` | 单仓节点 | 记录备忘（`note <txt>`）或清空（`note clear`） |
| `set` | 任意位置 | 命令行批量设置元数据（`--group`, `--tag`, `--status`, `--note`） |
| `rm` | 任意位置 | 从清单移除记录（不动磁盘物理文件） |
| `prune` | 任意位置 | 清理磁盘上已不存在的失效记录 |
| `export` | 任意位置 | 导出报告（Markdown / CSV / JSON / Outline） |
| `hide` / `unhide` | 任意位置 | 目录屏蔽与解封 |
| `fold` / `unfold` | 任意位置 | 目录折叠与展开 |
| `dirs` | 任意位置 | 查看所有隐藏与折叠规则 |
| `config` | 任意位置 | 配置管理（`show` / `discover` / `add-root` / `set`） |
