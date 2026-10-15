package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"grscan/internal/util"
)

// ----------------------------------------------------------------
// 帮助系统：渐进式披露 (Progressive Disclosure)
// 1. 默认帮助 (概览看板，控制在 25 行一屏内，绝不滚屏)
// 2. 专题帮助 (help <topic> / help --<topic>)
// 3. 子命令帮助 (help <cmd> / <cmd> --help)
// 4. 会话上下文感知帮助 (REPL 动态按当前 Scope 裁减)
// ----------------------------------------------------------------

const helpOverviewText = `igit - 本地 git 仓库扫描与整理工具 (v` + version + `)

用法:
  igit [选项]                  进入交互式作用域会话 (Tab 补全, exit 退出)
  igit <命令> [选项]            单次命令行执行并退出

核心命令 (按层次分组):
  导航 (Navigation)   cd <作用域> | ls | pwd
  浏览 (Explore)      list | tree | stats | triage (TUI面板)
  整理 (Triage)       tag | group | status | note | set
  动作 (Actions)      open | tig | export
  自管理 (GitOps)     ignore | remote | fetch | pull | commit | push
  配置 (Config)       scan | config | dirs (hide/fold) | prune

通用选项:
  --home <dir>        指定数据目录 (默认 ~/.igit)
  --in=<作用域>        临时指定作用范围 (例: --in=group:工作)

更多帮助 (支持 igit help <topic> 或 help --<topic>):
  igit help scope     作用域语法与维度导航 (tag / group / file)
  igit help filter    list / tree 状态与过滤选项 (--dirty, --unpushed...)
  igit help tig       tig 智能开屏联动机制与设置
  igit help rules     目录级跳过与折叠规则 (hide / fold)
  igit help config    系统配置与扫描根设置
  igit help all       输出完整综合手册
  igit <命令> --help  查看具体子命令参数 (例: igit scan --help)`

const helpTopicScopeText = `[专题] 作用域语法与导航指南 (igit help scope)

作用域是 igit 对仓库集合的切片视角，所有数据命令均在当前作用域生效。

维度前缀:
  tag:<标签>            标签维度 (例: tag:待推送)
  group:<分组>          分组维度 (例: group:工作)
  file:<目录>           物理目录维度 (例: file:D:\repos)
  repo:<路径>           单个具体仓库

组合与层级:
  group:工作/tag:待推送  维度交集：工作分组里待推送的仓库
  group:工作/scan_git    钻取进入单个仓库（进入单仓后 tig / open / show 可用）
  * 注: ":" 与 "#" 均支持，系统输出统一使用 ":"

作用域导航 (会话内):
  cd <作用域>           进入指定作用域 (例: cd tag:待推送)
  cd ..                 上一层
  cd /                  回到全集根
  cd -                  回到上一个作用域
  cd .                  回到按当前物理目录推断的作用域
  ls                    查看当前作用域下的子项或仓库
  pwd                   显示当前所在作用域`

const helpTopicFilterText = `[专题] 仓库过滤与排序选项 (igit help filter)

适用于 list 与 tree 命令 (在命令行模式或会话内均生效):

状态过滤:
  --all                 查看全部 (默认只看"待处理"仓库)
  --dirty / --clean     有 / 无未提交改动的仓库
  --unpushed            有未推送至远端的提交
  --no-remote           尚未配置远程仓库
  --status=X            按生命周期状态 (new|active|done|archived)
  --ignored             已标记为忽略的记录
  --missing             磁盘上已找不到物理目录的记录

维度与搜索:
  --group=X             按分组名过滤
  --tag=X               按标签名过滤
  --query=X             路径或仓库名包含关键字 X

显示与排序:
  --sort=<字段>         排序字段 (path|commit|name|group|status|size)
  --desc                降序排列
  --limit=N             最多显示 N 条
  --width=N             表格输出宽度 (默认 120)

命令特有选项:
  list --short          压缩路径显示 (默认显示完整路径)
  list --json           输出为 JSON 格式
  tree --brief          只看目录骨架与数量，不逐个列仓库
  tree --depth=N        展开到第 N 层，超出折叠为一行摘要`

const helpTopicTigText = `[专题] tig 智能联动指南 (igit help tig)

智能开屏:
  igit tig [关键字]     按仓库状态自动选择最佳开屏视图:
    有未提交改动  →  status 视图 (即刻查看改动)
    领先远端      →  仅查看未推送提交的差异
    没配上游/新仓 →  refs 视图
    其它干净状态  →  main 主视图 (提交历史)

选项与覆盖:
  --view=X             强制停在某一视图 (auto|main|status|log|refs)
  --print              只打印要执行的命令行，不弹出窗口
  --repo=X             当仓库名与视图名冲突时安全指定仓库

操作与快捷键:
  站在集合作用域时:    后接关键字模糊匹配 (多命中时列出候选编号供选择)
  站在单仓作用域时:    直接敲 tig 打开
  triage (TUI) 面板中: 按 g 直接打开当前光标所指仓库

配置项:
  igit config set tig-exe=<路径>    tig 不在 PATH 时指定其完整路径
  igit config set term-cmd=<命令>   指定拉起终端，占位符 {dir} {cmd}`

const helpTopicRulesText = `[专题] 目录级过滤与折叠规则 (igit help rules)

用于排除无关目录或保持输出整洁的全局规则:

命令:
  hide <目录>           整个目录不再出现，扫描也整棵跳过 (当前仓库/目录下可用 '.')
  unhide <目录>         取消隐藏
  fold <目录>           整个目录收拢成一行，只显示数量不展开仓库 (当前仓库/目录下可用 '.')
  unfold <目录>         取消折叠
  dirs                  列出当前已配置的所有隐藏与折叠规则

选项:
  -y, --yes             跳过二次确认提示，直接执行

示例:
  igit hide .                      在当前仓库或目录下快速隐藏当前目标 (需二次确认)
  igit fold .                      在当前仓库或目录下快速折叠当前目标 (需二次确认)
  igit hide D:\repos\temp_build    彻底不看此目录，扫描也不进
  igit fold D:\repos\third_party   只保留一行摘要，不展开细节
  igit dirs                        查看当前生效的所有规则`

const helpTopicConfigText = `[专题] 系统配置与扫描根设置 (igit help config)

管理扫描范围与全局运行参数:

配置命令:
  config show          查看当前配置参数与数据目录路径
  config init          重建默认配置
  config discover      自动探查系统各盘符，推荐可能含仓库的目录
  config add-root <dir> 添加扫描根目录
  config rm-root <dir>  移除扫描根目录
  config add-exclude <n>添加跳过的目录名 (支持 * 通配)
  config rm-exclude <n> 移除跳过的目录名
  config set k=v       直接修改配置参数

可配置参数 (config set k=v):
  jobs=N               并发扫描协程数
  max-depth=N          扫描最大下钻深度
  fast=true|false      快速模式 (只读 .git，不调 git，速度极快)
  deep=true|false      命中仓库后仍继续向下递归扫描
  git-exe=<path>       git 命令路径
  tig-exe=<path>       tig 命令路径
  term-cmd=<cmd>       终端启动命令模板 ({dir} {cmd})`

var commandHelps = map[string]string{
	"scan": `igit scan - 扫描配置的根目录并更新状态

用法:
  igit scan [选项]

说明:
  扫描配置的根目录，分析 git 状态并并入本地状态库。
  在已选定目录或单仓作用域下执行时，将自动进行原位窄化刷新。

常用选项:
  --root=<dir>   临时指定扫描根 (可重复指定多个)
  --all          强制全局扫描 (即使当前在局部作用域下)
  --fast         不调用 git，只读 .git 目录 (极快，但拿不到详细改动)
  --deep         命中仓库后仍继续往下钻 (扫描嵌套子仓库)
  --size         统计仓库占用体积 (较耗时)
  --jobs=N       并发协程数
  --max-depth=N  目录最大下钻深度`,

	"list": `igit list - 列出当前作用域下的仓库

用法:
  igit list [选项]

常用选项:
  --all          查看全部 (默认只看"待处理"仓库)
  --dirty        只看有未提交改动的
  --clean        只看干净无改动的
  --unpushed     只看有未推送提交的
  --no-remote    只看没配远端的
  --group=X      按分组名过滤
  --tag=X        按标签过滤
  --status=X     按状态过滤 (new|active|done|archived)
  --query=X      按路径或名字关键字过滤
  --sort=X       排序 (path|commit|name|group|status|size)  --desc
  --short        压缩路径显示
  --json         输出为 JSON 数据
  --limit=N      最多显示 N 条
  --width=N      表格宽度 (默认 120)`,

	"tree": `igit tree - 按磁盘目录树展开仓库，查看空间分布

用法:
  igit tree [选项]

常用选项:
  --brief        只看目录骨架与数量，不逐个列仓库
  --depth=N      展开到第 N 层，超出折叠为一行摘要
  --dirty        只看有未提交改动的分支
  --unpushed     只看有未推送提交的分支
  --group=X      按分组过滤
  --tag=X        按标签过滤
  --status=X     按状态过滤`,

	"triage": `igit triage (别名 tui) - 交互式整理面板

用法:
  igit triage [选项]

说明:
  进入全屏交互整理面板，支持逐个判别仓库、快速分组、打标签与归档。
  在面板中按 'g' 键可直接在外部唤起 tig 检视当前仓库。`,

	"export": `igit export - 导出仓库清单报告

用法:
  igit export --format=md|outline|csv|json [-o <输出文件>]

格式说明:
  md             按目录分节的 Markdown 报告 (概览 + 需关注分类 + 明细)
  outline        只有目录树骨架，看清分布格局
  csv            带 BOM 的 CSV 文件，Excel 直接双击打开
  json           原始结构化数据，供下游脚本或工具处理`,

	"group": `igit group - 管理仓库分组 (单仓至多归属一个分组)

用法:
  group                单仓下打开分组选择菜单；分组下打开成员管理菜单；全集下查看所有分组
  group <仓库名>       为指定仓库打开分组编号选择菜单
  group <组名>         将当前仓库加入该分组 (若已有其他组需二次确认)
  group new <名>       新建空分组
  group set <仓> [名]  设置仓库分组 (缺省名时自动弹出编号选项)
  group rm             分组作用域下选择移出仓库；或删除分组 'group rm <名>'
  group clear [仓]     清空仓库所属分组

选项:
  -y, --yes            覆盖已有分组时跳过确认直接执行`,

	"tag": `igit tag - 管理仓库标签

用法:
  tag list             列出所有标签及使用频次
  tag filter [标签]    进入响应式标签漏斗检索 (同 lsbytag)
  tag add <标签>       为当前仓库打上标签 (单仓作用域下)
  tag rm <标签>        移除当前仓库的指定标签`,

	"lsbytag": `igit lsbytag - 响应式多维标签漏斗检索与就地导航

用法:
  lsbytag [初始标签...] 进入响应式多维标签漏斗检索交互

交互指令:
  <编号/标签名>        追加伴生标签，实时收缩交集
  -<标签名> / pop      移除已选标签，空间动态回弹
  clear                清空所有已选标签，重置回初始全量
  #<序号>              直达并进入该目标仓库作用域
  q / quit             退出检索并保持当前作用域`,

	"ls-by-tag": `igit lsbytag - 响应式多维标签漏斗检索与就地导航 (同 lsbytag)`,

	"status": `igit status - 查看或修改仓库状态

用法:
  status               查看当前仓库或作用域状态
  status <新状态>      更新状态: new | active | done | archived`,

	"note": `igit note - 查看或记录仓库备注

用法:
  note                 查看当前备注
  note <内容>          写入/更新备注`,

	"set": `igit set - 批量或针对性修改仓库属性

用法:
  igit set <关键字> [--group=X] [--status=X] [--tag=X] [--note=X] [--ignore]`,

	"dupes": `igit dupes - 查找全盘重复克隆的副本

用法:
  igit dupes           分析全盘仓库，找出同源或重复命名的克隆副本`,

	"stats": `igit stats - 统计概览或仓库画像

用法:
  igit stats           集合作用域下看总体统计；单仓下查看详细画像画像`,

	"open": `igit open - 在资源管理器中打开仓库目录

用法:
  igit open [关键字]   单仓下直接打开，集合下指定关键字打开`,

	"tig": helpTopicTigText,

	"rm": `igit rm - 从状态清单中删除记录

用法:
  igit rm <关键字>     从 igit 状态中移除记录 (不删除磁盘真实文件)`,

	"prune": `igit prune - 清理无效记录

用法:
  igit prune           扫描清理磁盘上物理文件已不存在且未被忽略的残留记录`,

	"dirs": helpTopicRulesText,
	"hide": helpTopicRulesText,
	"unhide": helpTopicRulesText,
	"fold": helpTopicRulesText,
	"unfold": helpTopicRulesText,
	"config": helpTopicConfigText,

	"ignore": `igit ignore - 简易 .gitignore 创建与规则追加 (支持 Tab 补全)

用法:
  ignore               查看当前仓库 .gitignore 有效规则
  ignore add <模式...>  追加规则到 .gitignore (文件不存在时自动新建，支持 Tab 补全)
  ignore untracked     查看当前工作区未被跟踪的目录与文件`,

	"remote": `igit remote - 远端检查与 HTTPS 用户名记忆管理

用法:
  remote               列出当前仓库所有远端详细地址与绑定的鉴权用户
  remote user [用户名] 将用户名固化进远端 URL (Push 时仅需输密码，且按域名跨仓记忆)
  remote remember <域名> <用户> 手动记录指定域名的默认常用账号
  remote clear-user    移除远端 URL 中的嵌入用户名`,

	"fetch": `igit fetch - 远端更新抓取与状态拓扑诊断

用法:
  fetch [远端名]       拉取最新 refs 并原位刷新 ahead/behind 状态 (单仓或集合批量)`,

	"pull": `igit pull - 仅快进安全同步 (阻止冲突与命令行 Merge)

用法:
  pull                 仅在工作区干净且无分叉时执行快进同步 (遇分叉提示使用可视化工具处理)`,

	"commit": `igit commit - 全量暂存并提交改动 (add -A & commit)

用法:
  commit [提交说明]    将工作区所有改动一次性打包提交 (不传说明时提示输入)`,

	"push": `igit push - 推送到远端跟踪分支 (直接交互输入密码)

用法:
  push                 推送到远端 (未建跟踪分支时给出标准 upstream 命令提示)`,
}

var helpTopics = map[string]string{
	"scope":  helpTopicScopeText,
	"filter": helpTopicFilterText,
	"tig":    helpTopicTigText,
	"rules":  helpTopicRulesText,
	"config": helpTopicConfigText,
}

// fullHelpText 拼接生成的完整综合手册 (对应 help all)
func fullHelpText() string {
	var sb strings.Builder
	sb.WriteString(helpOverviewText)
	sb.WriteString("\n\n" + strings.Repeat("=", 70) + "\n\n")
	sb.WriteString(helpTopicScopeText)
	sb.WriteString("\n\n" + strings.Repeat("=", 70) + "\n\n")
	sb.WriteString(helpTopicFilterText)
	sb.WriteString("\n\n" + strings.Repeat("=", 70) + "\n\n")
	sb.WriteString(helpTopicTigText)
	sb.WriteString("\n\n" + strings.Repeat("=", 70) + "\n\n")
	sb.WriteString(helpTopicRulesText)
	sb.WriteString("\n\n" + strings.Repeat("=", 70) + "\n\n")
	sb.WriteString(helpTopicConfigText)
	return sb.String()
}

// usageText 保留兼容全局变量
var usageText = helpOverviewText

// normalizeHelpTopic 标准化用户输入的专题名 (去掉开头的 - 或 --，支持短别名)
func normalizeHelpTopic(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimLeft(s, "-")
	switch s {
	case "s", "scope", "scopes":
		return "scope"
	case "f", "filter", "filters":
		return "filter"
	case "t", "tig":
		return "tig"
	case "r", "rule", "rules":
		return "rules"
	case "c", "cfg", "config", "configs":
		return "config"
	case "a", "all":
		return "all"
	default:
		return s
	}
}

// helpTopicNames 给 Tab 补全使用的候选主题名
func helpTopicNames() []string {
	names := []string{"scope", "filter", "tig", "rules", "config", "all"}
	for cmd := range commandHelps {
		names = append(names, cmd)
	}
	sort.Strings(names)
	return names
}

// printCommandHelp 输出单个子命令的帮助
func printCommandHelp(cmd string) int {
	clean := strings.ToLower(strings.TrimSpace(cmd))
	clean = strings.TrimLeft(clean, "-")
	if h, ok := commandHelps[clean]; ok {
		fmt.Println(h)
		return 0
	}
	fmt.Fprintf(os.Stderr, "未找到命令 %s 的专属帮助，请查看常用命令概览:\n\n", cmd)
	fmt.Println(helpOverviewText)
	return 1
}

// printHelp 命令行入口帮助分发: igit --help [topic] 或 igit help [topic]
func printHelp(args []string) {
	if len(args) == 0 {
		fmt.Println(helpOverviewText)
		return
	}

	topic := normalizeHelpTopic(args[0])
	if topic == "" || topic == "help" {
		fmt.Println(helpOverviewText)
		return
	}
	if topic == "all" {
		fmt.Println(fullHelpText())
		return
	}
	if text, ok := helpTopics[topic]; ok {
		fmt.Println(text)
		return
	}
	if text, ok := commandHelps[topic]; ok {
		fmt.Println(text)
		return
	}

	fmt.Fprintf(os.Stderr, "未知帮助主题或命令: %s\n\n", args[0])
	fmt.Println("可用专题: scope | filter | tig | rules | config | all")
	fmt.Println("输入 igit --help 查看概览，或 igit help <topic> 查看对应栏目。")
}

// cmdHelp 会话 (REPL) 内的帮助处理
func cmdHelp(ctx *appCtx, args []string) {
	// 敲了具体参数，如 help scope, help --filter, help scan 等
	if len(args) > 0 {
		printHelp(args)
		return
	}

	// 未提供参数时，提供上下文感知的紧凑型帮助 (<= 20 行，保证一屏容纳)
	if ctx != nil && ctx.res != nil {
		fmt.Printf("当前作用域: %s · %d 个仓库\n\n", scopeLabel(ctx.res), ctx.res.Totals.Repos)
		fmt.Println("  当前可用命令:")
		for _, act := range availableActions(ctx.res) {
			fmt.Printf("    %s%s\n", util.PadRightDisplay(act.cmd, 16), act.desc)
		}
		fmt.Println()
		fmt.Println("  作用域导航:")
		fmt.Printf("    %scd <作用域> (例: cd tag:待推送, cd ..)\n", util.PadRightDisplay("走进去/切换", 16))
		fmt.Printf("    %sls (查看当前作用域下的子项或仓库)\n", util.PadRightDisplay("列出子项", 16))
		fmt.Printf("    %spwd (显示当前位置)\n", util.PadRightDisplay("当前位置", 16))
		fmt.Println()
		fmt.Println("  提示:")
		fmt.Println("    敲 help <topic> 查看专题 (scope | filter | tig | rules | config)")
		fmt.Println("    敲 help all 查看完整综合手册，敲 exit 退出")
		return
	}

	fmt.Println(helpOverviewText)
}
