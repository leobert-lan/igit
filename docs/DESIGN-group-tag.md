# Group 与 Tag 交互模型设计规范 (Living Spec)

> **【定位：永驻设计规范 / Living Spec】**  
> **更新日期**：2026-10-12  
> **关联代码**：[`main.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/main.go)（`cmdGroup`, `cmdTag`, `pickGroupForRepo`）, [`internal/state/state.go`](file:///c:/Users/Lenovo/WorkBuddy/scan_git/internal/state/state.go)

---

## 1. 设计动机与核心痛点

在传统的命令行中，管理仓库元数据（Group / Tag）存在两大核心痛点：
1. **记忆负担重**：用户必须频繁使用 `group list` 或 `tag` 确认系统内已有的分组和标签，无法就地直接选用；
2. **打字成本高**：在深度嵌套的 REPL 作用域中，全量敲击 `group set <仓库名> <分组名>` 繁琐且容易输错。

### 核心解法
- **Scope 隐式推断宾语**：站在不同作用域，系统自动推断操作对象（单仓/当前组/全局）；
- **交互式选项回退（Picker Fallback）**：意图明确而参数未满时，不抛语法错误，就地输出编号选项菜单；
- **排他性与宽松性区分治理**：
  - **Group 是 $N:1$（单一互斥归属）**：仓库至多属于一个 Group。替换已有 Group 时必须**二次确认**；
  - **Tag 是 $N:M$（自由多重标记）**：支持多选、追加与摘除，语义更加宽松。

---

## 2. 作用域推断矩阵 (Scope Inference Matrix)

| 用户所在作用域 | 输入命令 | 推断宾语 / 上下文 | 交互行为 |
| :--- | :--- | :--- | :--- |
| **单仓作用域** (`repo:...` 或 `file:.../仓`) | `group` | 当前仓库 `TargetRepo()` | 弹出**分组编号选择菜单**（包含已有分组、当前标识 `[√]`、新建 `+`、清除 `0`） |
| **单仓作用域** | `group <组名>` | 当前仓库 `TargetRepo()` | 尝试加入 `<组名>`；若已有其他分组，弹出**替换确认 `[y/N]`** |
| **单仓作用域** | `group clear` | 当前仓库 `TargetRepo()` | 清除当前仓库的分组 |
| **分组作用域** (`group:<组名>`) | `group` | 当前分组 `Ident` | 弹出当前分组管理菜单（移出仓库 / 添加仓库 / 列出所有分组） |
| **分组作用域** | `group rm` / `group clear` | 当前分组内的仓库列表 | 弹出**组内仓库编号菜单**，用户输入序号即可移出该仓库 |
| **分组作用域** | `group add <kw>` | 外部目标仓库 | 将目标仓库加入当前分组（若已有组则提示替换确认） |
| **目录 / 根作用域** (`/` 或 `file:...`) | `group <仓库名>` | 命中的目标仓库 | 识别出仓库后，自动打开该仓库的**分组编号选择菜单** |
| **目录 / 根作用域** | `group set <仓库名>` | 命中的目标仓库 | 同上，打开分组选择菜单（不报错缺少参数） |
| **目录 / 根作用域** | `group set <仓> <组>` | 目标仓库与目标分组 | 执行设置；若仓库已有其他分组且未带 `-y`，弹出替换确认 |
| **目录 / 根作用域** | `group clear <仓库名>` | 命中的目标仓库 | 清除该仓库的分组 |
| **任意作用域** | `group list` / `group ls` | 全局 | 列出全盘所有分组及各包含多少仓库 |
| **任意作用域** | `group new <组名>` | 全局 | 预先创建一个空分组并持久化 |

---

## 3. Group 交互原型与状态机

### 3.1 仓库选择分组菜单原型 (`pickGroupForRepo`)

```text
仓库: my-project (当前分组: study)

请选择目标分组:
   1) [√] study   (当前所属)
   2)     job
   3)     leobert
   +) [新建分组...]
   0) [清除分组] (移出当前分组)

请选择 (编号 / + / 0 / q 取消): 2
```

### 3.2 唯一性替换确认契约
- **从未设置 $\to$ 新分组**：无需确认，直接更新；
- **相同分组 $\to$ 相同分组**：提示“仓库已属于该分组，无需变更”；
- **已有分组 A $\to$ 变更为分组 B**：
  ```text
  my-project 当前属于 [study]，确认变更为 [job]? [y/N] 
  ```
  输入 `y`/`yes` 确认写入并落盘；输入回车/`n` 安全取消。

### 3.3 分组内移出仓库原型 (`pickRepoToRemoveFromGroup`)

```text
分组 study 内的仓库 (共 3 个):
   1) algorithm-rs
   2) compiler-demo
   3) scan_git

请选择要移出的仓库 (序号 / q 取消): 2
-> 已将 compiler-demo 从分组 study 移出。
```

---

## 4. Tag 交互模型规范 (Tag Interactive Model)

与 Group 的 $N:1$（单一互斥归属）不同，Tag 是 $N:M$（自由多重标记）。

### 4.1 单仓聚焦下的交互式打标 (`pickTagForRepo`)
当用户处于单仓作用域时，直接键入 `tag` 启动多选复选框面板：
- **系统中尚无标签**：提示并引导 `+) [新建标签并打上...]`，支持直接输入名称创建并打上；
- **系统中已有标签**：展示带编号的勾选框清单：
  ```text
  仓库: AgyArchiveHelper (当前标签: helper)

  现有标签 (输入编号切换勾选，支持多个编号用空格隔开):
     1) [ ] backup
     2) [√] helper
     3) [ ] rust
     +) [新建标签...]
     0) [清空此仓库所有标签]

  请选择 (编号 / + / 0 / q 完成退出): 1 3
  已更新 AgyArchiveHelper 标签: helper, backup, rust
  ```
- **支持多项切换**：输入 `1 3` 可同时翻转 `1` 和 `3` 的勾选状态；
- **快捷追加**：在单仓内直接输入 `tag rust, cli` 立即追加标签，不覆盖已有标签；
- **清空标签**：输入 `tag clear` 或选项 `0` 直接清空该仓库所有标签。

### 4.2 标签作用域下的交互 (`pickTagActionInTagScope`)
当用户处于 `tag:<标签名>` 作用域（如 `tag:rust`）时：
- 键入 `tag`：弹出标签作用域管理菜单（移除关联仓库 / 添加关联仓库 / 列出所有标签）；
- 键入 `tag rm`：列出当前标签下的所有仓库编号，按数字即可直接剥离该标签。

### 4.3 全局与目录视角下的标签命令
- 键入 `tag`：输出全盘所有标签的统计量与引导提示；
- 键入 `tag <仓库名>`：自动解析仓库并打开该仓库的交互式复选打标面板；
- 键入 `lsbytag`：进入响应式多维分面标签漏斗检索，多选过滤并直达目标仓库。

---

## 5. Scope 优先级与责任链组装机制 (Scope Chain of Responsibility)

Context 中的 Scope 具备明确的优先级层级。当用户输入不带目标宾语的动词（如 `tag`、`group`、`hide`、`tig`）时，命令分发沿责任链自顶向下逐层匹配处理：

```text
Priority 1: TargetRepo (单仓聚焦)
    │  ├─ group        -> pickGroupForRepo (单仓唯一分组设置与替换确认)
    │  ├─ tag          -> pickTagForRepo (单仓多选复选打标面板)
    │  ├─ tig          -> 唤起当前仓库的 tig/git log
    │  └─ note / status-> 针对当前单仓就地查看/修改
    ▼
Priority 2: NodeTag (标签作用域 tag:xxx)
    │  ├─ tag rm       -> pickRepoToRemoveFromTag (组内成员仓库列表摘标)
    │  └─ tag          -> pickTagActionInTagScope
    ▼
Priority 3: NodeGroup (分组作用域 group:xxx)
    │  ├─ group rm     -> pickRepoToRemoveFromGroup (组内仓库移出)
    │  └─ group        -> pickGroupActionInGroupScope
    ▼
Priority 4: NodeDir (目录作用域 file:xxx)
    │  ├─ group/tag    -> 全局/目录视角（提示或指定仓库语法）
    │  └─ hide/fold .  -> 隐藏/折叠当前目录
    ▼
Priority 5: NodeRoot (全局根目录 /)
       └─ group/tag    -> 列出全集元数据统计与操作引导指南
```

---

## 6. 终端交互与键盘输入防乱码规范 (Input Hygiene & Key Sequences)

在 Windows ConPTY、PowerShell、cmd 或类 Unix 终端中，用户在交互式 Prompt 录入字符时经常使用键盘控制键（退格键 Backspace、删除键 Delete、方向键 $\leftarrow \rightarrow \uparrow \downarrow$）：

1. **Readline 劫持**：所有子交互输入统一通过 `readPromptLine` 委托给 `ctx.rl.Readline()`，由 `readline` 在 raw mode 内部捕获并处理光标移动和行编辑；
2. **ANSI 过滤与模拟退格 (`cleanTerminalInput`)**：
   - 使用正则 `(?:\x1b|\^\[)(?:\[[0-9;?]*[ -/]*[@-~]|O[A-Za-z0-9]|.)` 彻底剔除残留的 ANSI 转义序列（如 `\x1b[D`、`\x1b[3~`、`^[[D` 等），防止方向键/删除键变成 `[D` 或 `[3~` 等乱码写入元数据；
   - 识别并模拟终端退格行为（`\b` 与 `\x7f` DEL），动态回退 rune 栈；
   - 过滤 `< 32`（非空白/换行）的不可见控制字符；
3. **全局防线**：`readPromptLine` 与 `cleanTagInput` 形成双层防线，确保任何终端环境下录入的标签名、分组名均绝对纯净。

