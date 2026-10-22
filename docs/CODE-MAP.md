# 源码模块概览(CODE-MAP)

项目:`igit` —— Windows 下本地 git 仓库扫描/整理工具(Go,单 exe,依赖 `bubbletea`/`lipgloss`)。
本文仅概述各模块职责,依据:目录结构、函数/类型签名、README、`docs/SCOPE.md`。
标注 **TODO** 的为推测内容,需精读代码确认。

## 1. 入口层(根目录 `package main`)

| 文件 | 职责 |
|---|---|
| `main.go`(~150KB,最大) | 命令行入口与分发:`run` → `dispatch`,含全局参数解析(`--home`/`--scope`)、REPL(`runREPL`、Tab 补全 `replCompleter`)、管道模式(`replPipe`)、作用域解析(`resolveScope`)、以及大部分 `cmdXxx` 命令实现(cd/ls/scan/list/tree/tag/group/status/note/dupes/stats/open/tig/rm/prune/hide/fold/export/config 等)。 |
| `commands_gitops.go` | 涉及真实 git 操作的命令:`ignore`、`remote`、`fetch`(含批量)、`pull`、`commit`、`push`。 |
| `help.go` | 帮助文本与各命令的 help 主题。 |
| `*_test.go` | 对应测试。 |
| `build.cmd` | 一键编译脚本。 |

> TODO:我猜测 `main.go` 体量过大,混杂了命令实现、REPL、作用域解析等多种职责,后续可能需要拆分;请精读确认各部分边界。

## 2. internal 子包

| 包 | 职责 |
|---|---|
| `config` | 配置(`Config`)的读取/保存、默认值、扫描根目录猜测(`guessRoots`)、`--home` 目录与配置/状态文件路径解析。 |
| `state` | 核心数据模型与持久化:`Repo`、`State`、`ScanRecord`、`DirRule`;管理仓库清单、分组、标签、备注、隐藏(Hide)/折叠(Fold)规则;`Load`/`Save`(README 称原子写入)、`Upsert`(扫描结果合并)、`Find`、`PruneMissing`、排序。 |
| `scan` | 并发遍历目录寻找 git 仓库(`Run`、`walker`),区分普通仓库/linked worktree/submodule,处理排除路径。 |
| `gitinfo` | 获取单个仓库信息:`Inspect` 优先直接读取 `.git`(HEAD、config、loose object 等),必要时回退调用 git 命令(`fallbackCommit`、`readStatus`);`ops.go` 封装 git 写/网络操作(remote 查看与改用户名、untracked 列表、.gitignore 追加、fetch/pull --ff-only、commit-all、upstream 信息)。 |
| `scope` | 导航模型:`Scope`/`Seg`/`TrailStep`(来时路栈),`Parse`/`Resolve` 把 `dir:`/`group:`/`tag:`/repo 路径解析为当前可见仓库集合与子项(`Item`、`Totals`);设计详见 `docs/SCOPE.md`。 |
| `tree` | 将仓库列表构建成目录树(`Build`)、折叠单链(`collapse`)、文本渲染(`Render`)、分段/分组(`Sections`/`Groups`)。 |
| `render` | 列表行/表头/状态列的文本渲染(`View`、`Row`、`Header`、`FoldRow`、各类 Label 格式化)。 |
| `pick` | “命令作用于哪个仓库”的选择逻辑:`pick.Repo` 按关键字匹配,多个候选时提示/选择;`menu.go` 提供单选/多选菜单(bubbletea 版 + 文本回退版)。 |
| `tig` | 联动外部 `tig`:`Decide` 自动选择视图(状态/日志等)、`Exe` 定位可执行文件、`Launch` 在新终端启动(含 msys2 终端处理)。 |
| `export` | 导出:`export.go`(CSV/JSON)、`markdown.go`(Markdown 报告)、`summary.go`(全局汇总)、`tree_export.go`(全局目录树)、`orphans.go`(孤儿仓库 TODO 清单)、`scope_export.go`(按当前作用域的报告)、`wizard.go`(交互式导出向导 `RunWizard`/`Execute`)、`types.go`(`Context`/`Options`/`Result`)。 |
| `util` | 通用工具:路径规范化/长路径、前缀匹配、时间/体积格式化、显示宽度对齐与截断(含中文宽度)、`SortedKeys`。 |
| `security` | 凭据安全加密: 基于机器特征派生密钥的 AES-256-GCM 加解密实现，用于保护 GitHub/GitLab 等平台 Token 不落盘明文。 |

## 3. 依赖方向(据 import 习惯推测)

> TODO:我猜测大致为 `main → (scope, export, pick, tig, scan, gitinfo, tree, render, config)`,`scope/tree/render/export → state`,`state/gitinfo → util`;未验证是否存在循环或反向依赖,请精读 import 确认。

## 4. 现有文档

- `README.md`:使用概述与代码结构(其目录清单未列出 `help.go`、`commands_gitops.go`、`pick/menu.go`、`export` 下多个文件,与实际不完全一致)。
- `docs/USAGE.md`:详细用法。
- `docs/SCOPE.md`:Scope 与来时路设计。
- `docs/DESIGN-group-tag.md`、`docs/ANALYSIS-*.md`:分组/标签与命令设计分析。
  > TODO:我猜测这些是 AI 生成的设计/分析记录,可能与现状有偏差,请精读时对照代码。

## 5. 待精读的重点

1. TODO:我猜测 `state.State` 是全局数据中心(JSON 持久化到 state 文件),其字段与迁移/兼容逻辑需细读。
2. TODO:我猜测 `scope.Resolve` 是所有命令的“可见仓库”来源,其行为影响几乎所有命令。
3. TODO:我猜测 `gitinfo.Inspect` 手工解析 `.git` 对象(含 packed 对象处理)是易出错点。
4. TODO:`push` 命令实现位置未确认(`main.go` 中有 case,可能在 `commands_gitops.go`)。
