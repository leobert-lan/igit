# igit 复杂指令梳理与交互简化研究规范

> **【定位：永驻分析与设计规范 / Living Spec】**  
> **创建日期**：2026-10-20  
> **核心原则**：**“如非必要，不使用复杂命令参数，选项交互永远是第一优先级！”**  
> **关联代码**：[`main.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/main.go), [`commands_gitops.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/commands_gitops.go), [`internal/pick/menu.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/internal/pick/menu.go)  
> **前置参考**：[`docs/DESIGN-group-tag.md`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/docs/DESIGN-group-tag.md), [`internal/export/wizard.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/internal/export/wizard.go)

---

## 1. 背景与改造现状 (Context & Current Progress)

过去命令行工具（CLI）最大的通病是**“极客思维泛滥”**：
- 迫使用户记忆各种冗长且容易拼错的 flag（如 `--format=csv|json|md --scope=xxx --out=path --filter=dirty`）；
- 在交互式终端中，意图明确而参数未满时直接抛出语法错误退出，强迫用户查阅 `--help`；
- 操作完成后无反馈或需要用户手工翻找输出文件。

### 1.1 已完成交互简化的典型模块
当前项目中已有部分核心指令彻底贯彻了「选项交互第一」的设计哲学：
* **`export`**：裸敲进入 `export.RunWizard` 多选向导，提供合理的默认预设，导出后自动调用系统 Explorer 打开结果目录。所有 flag（`--format`, `-o`）退居为无头（Headless）脚本调用的兼容兜底。
* **`config`**：裸敲直接进入配置管理交互中心（扫描根、导出目录、并发数/深度、排除规则等全菜单化操作），告别 `config set k=v` 的死记硬背。
* **`group`**：结合 Scope 自动推断操作宾语；单仓下直接弹出分组编号菜单；目录作用域下提供分组管理向导，彻底消除 `group set <repo> <group>` 的全量敲击负担。
* **`lsbytag` (`tag filter`)**：响应式标签漏斗交互，动态计算伴生标签频次，支持编号追加与退栈收缩，实现零敲击直达目标。

---

## 2. 现有复杂指令全景排查矩阵 (Inventory of Complex Commands)

按交互摩擦力与认知负荷，当前项目中亟需简化的复杂指令可划分为以下梯队：

| 复杂度评级 | 指令名称 | 当前命令行语法 | 核心痛点 (Pain Points) | 建议交互简化路径 |
| :---: | :--- | :--- | :--- | :--- |
| **P0 (极高)** | **`set`** | `set <关键字> [--group=X] [--status=X] [--tag=a,b] [--untag=a,b] [--note=X] [--ignore\|--unignore] [--clear_*] [--yes]` | 包含 10+ 冗长 flags，裸敲或漏参直接报错退出；修改属性必须打长参数。 | 裸敲或指定目标后弹出**仓库属性编辑看板 (Property Wizard)**，菜单化切换状态、打标、设组与备注。 |
| **P1 (高)** | **`list` / `tree`** | `list [--dirty] [--clean] [--unpushed] [--no-remote] [--group=X] [--tag=X] [--status=X] [--sort=X] [--desc] ...` | 过滤透镜多达十几种组合，高频操作（如看待推送）需敲长参数；缺少即时切面。 | 提供交互式**快速透镜切换器 (Filter Preset / Lens Palette)** 或单键快筛预设。 |
| **P1 (高)** | **`dirs` / `hide` / `fold`** | `dirs`<br>`hide <目录> [-y]`<br>`unhide <目录>`<br>`fold <目录>`<br>`unfold <目录>` | `dirs` 仅被动输出文本；撤销折叠或隐藏必须手动复制冗长绝对路径并敲 `unhide <完整路径>`。 | `dirs` 升级为**规则交互管理中心**：编号列出规则，输入序号一键撤销，或就地浏览选择添加。 |
| **P2 (中)** | **`remote`** | `remote user [远端名] [用户名]`<br>`remote remember <域名> <用户>`<br>`remote clear-user [远端名]` | 多级子命令容易拼错，域名与账号记忆指令反直觉。 | 裸敲 `remote` 弹出**远端管理交互卡片**：自动识别 Host，一键选择“绑定账号 / 切换用户 / 测试连通性”。 |
| **P2 (中)** | **`scan`** | `scan [--root=dir] [--fast] [--deep] [--size] [--jobs=N] [--max-depth=N] [--all]` | 深度扫描、极速模式与体积计算分散在不同 flags 中。 | 支持无参直接执行标准刷新；带 `-i` 或环境探测时提供**扫描模式预设选择 (极速 / 标准 / 全量深度+体积)**。 |
| **P3 (低)** | **`status` / `note`** | `status [仓库] <new\|active\|done\|archived>`<br>`note [仓库] <文本>` | 单仓作用域下裸敲 `status` 仅显示文本，不能直接 1~4 单选改状态；无参时在集合域直接报错。 | 单仓下裸敲 `status` 弹出 4 选 1 状态选择器；`note` 提示就地输入新内容。 |
| **P3 (低)** | **`ignore`** | `ignore add <规则...>`<br>`ignore untracked` | 看完未跟踪项后，仍需手动敲 `ignore add <pattern>`。 | `ignore untracked` 支持编号多选勾选，直接落盘追加至 `.gitignore`。 |

---

## 3. 各复杂指令详细痛点与交互原型方案

### 3.1 `set`：仓库属性统一交互看板

#### 现状痛点
用户想给一个刚拉下来的仓库标状态为 active、打上 rust 标签、移入开发组：
```bash
# 现状：用户必须记忆并敲击
igit set my-repo --status=active --tag=rust,backend --group=核心业务 --note="重构中"
```
一旦拼错 `--tag` 成为 `--tags` 或漏掉参数，命令直接失败。

#### 简化方案：`set` 交互属性看板 (Interactive Property Wizard)
当用户输入 `set` 或 `set <kw>` 未带 flags 时，自动进入向导：
```text
[仓库属性设置向导] 目标: my-repo (D:\repos\my-repo)
------------------------------------------------------------
当前属性:
  分组 (Group):   未分组
  状态 (Status):  new (新发现)
  标签 (Tags):    [tools]
  备注 (Note):    无
------------------------------------------------------------
请选择要修改的属性 [输入序号，按 q 退出]:
  1) 修改所属分组 (Group) -> 弹出已有分组单选/新建
  2) 修改处理状态 (Status) -> 弹出 new/active/done/archived
  3) 管理标签 (Tags) -> 追加标签、多选摘除、复用系统常用标签
  4) 编辑备注 (Note) -> 输入文字更新
  5) 忽略设置 (Ignore) -> 切换忽略状态 (不再纳入扫描清单)
  0) 全部清空与重置
```


---

### 3.2 `list` / `tree`：多维过滤透镜快捷面板

#### 现状痛点
想要在终端快速看“有未提交改动的仓库”，必须敲：
```bash
igit list --dirty --sort=commit --desc
```
想要看树状骨架必须敲：
```bash
igit tree --brief --depth=2
```

#### 简化方案：透镜快捷指令与交互切换
1. **预设语义别名 (Sensible Aliases)**：
   - `list -d` / `dirty`：一键查看有脏改动的仓库
   - `list -u` / `unpushed`：一键查看有未推送提交的仓库
   - `tree 2`：等价于 `tree --depth=2`
2. **交互式过滤调色盘 (Filter Palette)**：
   在 REPL 中敲 `filter` 或 `ls -i`，弹出快速多选开关：
   ```text
   [仓库列表过滤透镜] 当前作用域: / (共 182 个仓库)
   [x] 1. 仅看未提交改动 (--dirty)
   [ ] 2. 仅看有未推送提交 (--unpushed)
   [ ] 3. 仅看纯本地无远端 (--no-remote)
   [ ] 4. 隐藏已归档仓库 (仅看 active & new)
   ------------------------------------------------------------
   排序依据: [1] 最近提交时间  [2] 路径字母  [3] 占用体积
   [回车应用筛选] [c 清除所有透镜]
   ```

---

### 3.3 `dirs`：目录级规则交互管理中心

#### 现状痛点
当前查看目录规则：
```text
> igit dirs
已隐藏（完全不出现，扫描跳过）:
  D:\repos\temp_build
  D:\work\backup_code\old
已折叠（只显示一行，不展开）:
  D:\repos\third_party
```
如果用户想要把 `D:\repos\temp_build` 解除隐藏，必须打字：
```bash
igit unhide D:\repos\temp_build
```
路径长、反斜杠多、极容易敲错。

#### 简化方案：`dirs` 规则编号交互管理
敲 `dirs` 直接展示带编号管理菜单：
```text
[目录过滤与折叠规则管理中心]
------------------------------------------------------------
[已隐藏目录] (扫描跳过，彻底不展示)
  [1] D:\repos\temp_build (包含 12 个仓库)
  [2] D:\work\backup_code\old (包含 5 个仓库)

[已折叠目录] (收拢为一行摘要)
  [3] D:\repos\third_party (包含 28 个仓库)
------------------------------------------------------------
操作选项:
  • 输入序号 [1-3] 直接移除对应规则 (解除隐藏/折叠)
  • 输入 +h <路径> 或 +h . 隐藏新目录
  • 输入 +f <路径> 或 +f . 折叠新目录
  • 按 q 或回车退出
> 
```

---

### 3.4 `remote`：远端与多账号记忆交互卡片

#### 现状痛点
不同 Git 平台（公司内部私有 GitLab、GitHub、Gitee）的账号经常不同。
目前命令为：
```bash
igit remote user origin zhangsan
igit remote remember gitlab.mycompany.com zhangsan
```
语法晦涩，用户经常记不住。

#### 简化方案：`remote` 交互卡片
进入单仓后输入 `remote`：
```text
仓库: client-core (当前分支: main)
远端配置:
  origin -> https://gitlab.internal.net/rd/client-core.git
  检测到当前 Host: gitlab.internal.net (已记忆账号: zhangsan)
------------------------------------------------------------
请选择操作:
  1) 为当前仓库绑定用户名 [当前: 未绑定 -> 选定将写入 URL: https://zhangsan@gitlab...]
  2) 修改/记忆该域名 (gitlab.internal.net) 的常用账号
  3) 清除当前仓库 URL 中的内嵌用户名
  4) 连通性测试 (git ls-remote 验证鉴权是否有效)
  0) 退出
> 
```

---

## 4. 实施推进建议 (Next Steps)

1. **第一阶段 (P0)**：落地 `set` 交互属性看板。修改仓库属性是日常最高频动作，彻底干掉 10+ 复杂 flags 的记忆负担。
2. **第二阶段 (P1)**：改造 `dirs` 为编号交互菜单，彻底免除用户在终端中复制长路径 `unhide`/`unfold` 的痛苦。
3. **第三阶段 (P2/P3)**：逐步优化 `list`/`tree` 的过滤透镜切换与 `remote` 交互卡片。
