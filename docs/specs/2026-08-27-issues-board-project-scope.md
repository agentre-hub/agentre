# 看板：项目维度、筛选与呈现重构

> Status: Draft
> Owner: 桌面端
> Last updated: 2026-08-27（第二轮：范围扩到 agentre-server 与多端同步）

**Objective:** 把 `/issues` 从一块与项目无关、状态轴自相矛盾、只能筛标签的**本机私有**待办板，改成一块按项目组织、条件齐全、可就地编辑、**随账号在多端之间同步**的看板；桌面端与 `agentre-server` 的 web 控制台渲染同一套呈现件，同一轮交付。

**Hard invariant:** 现有 `issues` / `labels` / `issue_labels` 的行不因这一轮丢失或改写语义；新迁移只追加到 `migrationList()` 末尾（`migrations/migrations.go:21`），不修改既有迁移。

**第二条 Hard invariant（跨仓）：** 同步载荷里**不出现任何本地自增 ID**。两端各有一份同名同规则的守卫
（桌面端 `internal/pkg/syncwire/guard.go:49` `GuardPayload`、服务端 `sync_entity.ValidatePayload`），后者按键名拦截「以 `id` 结尾且取值为数字」
（`agentre-server/internal/model/entity/sync_entity/payload.go:106-110`，其归一化函数的注释正是拿 `agent_backend_id` 举例）。
跨机引用一律用同步标识（字符串）、agentred 指纹或 `provider_key`。

## Problem

1. **看板与项目无关。** 后端筛选早就支持 `ProjectID`（`internal/repository/issue_repo/issue.go:19-25`、`:100`），前端把它硬编码成 `0`（`frontend/src/components/agentre/issues-page.tsx:75`）；新建对话框能选项目，列表与卡片却从不展示、也不能按它过滤。项目字段写得进去、看不出来。

2. **两套彼此重叠的状态轴。** `stage` 与 `state` 各说一次「这条走到哪一步」。`SetStage` 会同步 `state`（`internal/model/entity/issue_entity/issue.go:78`），`SetState` 却不动 `stage` —— 于是可以存在一张**躺在「待办」列里、`state` 已经是 closed** 的任务，界面上完全看不出来。列表视图的 Open/Closed 两个 tab 与看板的四个列说的也是同一件事。

3. **行首状态图标读的是死字段。** 列表行按 `agentStatus` 选图标（`issues-page.tsx:468`），而该字段只在创建时写死 `idle`（`internal/service/issue_svc/issue.go:63`），全仓没有第二处写入。三种颜色里只有灰色那一种会出现。

4. **看板卡片没有任何菜单。** `BoardCard` 只接 `onEdit`（`frontend/src/components/agentre/kanban/issues-board.tsx:164`），菜单只存在于列表视图。改一张卡的唯一办法是拖，而拖拽对键盘用户等于没有。

5. **筛选条件只有标签，且永远是「任意一个」。** 仓储写死 `label_id IN ?`（`issue_repo/issue.go:101-104`），要「既是 bug 又是 perf」做不到；`createtime` / `updatetime` / `closed_at` 三列在表里躺着，没有任何入口能用它们筛；没有关键词搜索。

6. **表单丢信息、且编辑态少一栏。** 项目下拉是 `projects.map(p => p.name)` 的扁平 `<Select>`（`frontend/src/components/agentre/issue-new-dialog.tsx:141-158`），`ProjectFlat` 的 `depth` 与 `color`/`icon` 全丢，两个同名子项目分不出来；阶段一栏写成 `{!editing && …}`（`:160`），编辑时不渲染，想换列只能关掉弹窗回去拖；`projectID` 初值恒为 `0`（`:49`），正看着某个项目的看板建出来的任务却挂在「未归属」；提交错误块是 `DialogBody` 的最后一个子元素（`:205`），表单一滚就看不见。

7. **标签只读，且色调名与「标签可自建」互相矛盾。** `issue_svc` 只有 `ListLabels`（`internal/service/issue_svc/issue.go:224`），没有任何创建 / 改名 / 换色 / 删除路径；内置标签写死在迁移种子里（`migrations/202608080010_issues.go:68` 种十条，`migrations/202608270004_*.go:35` 又裁到五条）。而这五档的**名字就是语义**——`bug` / `critical` / `docs` / `feature` / `refactor`（`issue_entity/label.go:17`、`issue-tones.ts:1-6`）：色调等于用途。一旦用户能自建标签，「一个叫『前端』的标签，色调叫 `bug`」就说不通了；五档也不够分。

8. **暗色下两类元素画不出来。** 实测：`--border` 在 `--popover` 上暗色 **1.06**（浅色 1.27），弹层与对话框里的分隔线整条消失；`--secondary` 与 `--popover` 在暗色是**同一个字节** `#262931`（比值 1.00），用 `bg-secondary` 填充的 `docs` 档、`toneClass` 的兜底档（`issue-tones.ts:23,30-32`）与溢出计数 `+N`，在任何弹层里都只剩文字。

9. **文案中英混排，并混着一批从未被引用的键。** 侧栏是 `Issues`、筛选是 `Open` / `Closed`、视图是 `Board` / `List`，与「新建 Issue」这类中文混在一起。`issues.actions.dispatchToAgent` / `redispatch` / `filters.assignedAgent` / `status.unassigned` / `board.lastFailure` / `board.addCard` / `addToColumn` 没有任何组件引用 —— 是一套没做的派发功能留下的文案。`README.md:45` 与 `docs/README_zh.md:45` 同样承诺了不存在的「把任务交给 Agent / 回复创建关联会话」。

10. **任务是本机私有数据，不随账号走。** `issues` / `labels` / `issue_labels` 三张表都没有 `sync_id`，也不在 `sync_svc` 的
    `syncKinds` 里（`internal/service/sync_svc/adapter.go:142-152` 列的是 project / department / agent / agent_backend /
    agent_backend_cli / agent_exec_target / project_agent / project_location / llm_provider 九个）。换一台机器打开
    Agentre，看板是空的；同一个人在两台机器上记的任务永远互不相见。

11. **web 控制台没有看板。** `agentre-server` 既没有 issue 实体、表、路由，也没有页面（`frontend/src/pages/` 下是
    Account / Chat / Device* / Org / Overview / SessionDetail / Settings）。而组织面与项目面已经是「浏览器直写
    `sync_objects`」的成熟通道（`internal/api/router.go:154-175`），任务是同一类「全是指向、没有一件是机器上的东西」
    的数据，本该走同一条路。

## Actors and user stories

1. 作为**同时开着多个项目的使用者**，我要把看板收窄到某一个项目（并带上它的子项目），这样看板上就只有我此刻在做的那一摊事。
2. 作为**要找回某条任务的使用者**，我要用关键词、标签、时间范围把它筛出来，这样不必从几十张卡里翻。
3. 作为**在录任务的使用者**，我要一个以标题和描述为主体的表单，属性用最少的点击设定，这样连着录几条不觉得累。
4. 作为**整理标签的使用者**，我要能新建、改名、换色、删除标签，这样标签体系跟得上项目的变化。
5. 作为**用键盘工作的使用者**，我要能不靠拖拽移动一张卡，这样看板对我不是只读的。
6. 作为**在两台机器上工作的使用者**，我要在任意一台上记的任务在另一台上也看得见、改得动，这样看板才值得我往里录东西。
7. 作为**手边只有浏览器的使用者**，我要在 web 控制台上看同一块看板并改动它，这样离开自己的电脑也能安排事情。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 项目维度做成**标题栏的选择器**，不加侧栏 | 用户决定。Rejected：左侧项目侧栏（与 `/chat` 同构但主区更窄）；项目泳道（项目一多纵向极长，跨项目拖拽语义还要另定） |
| 2 | 选中父项目**一并显示其子项目** | 用户决定；与 `/chat` 项目树按子树聚合一致（`session-index/index-page.tsx:637` `countDescendants`）。Rejected：严格等值匹配 —— 父项目多是分组壳，只看它自己往往是空板 |
| 3 | **只保留看板视图**，删除列表视图与视图切换 | 用户决定。四个列已经表达「走到哪一步」，列表 + Open/Closed tab 是同一件事的第二种说法 |
| 4 | **`state` 轴退出界面**，「在不在已完成列」是唯一说法；卡片菜单用「移动到」，不再有「关闭 / 重新打开」 | 消除 Problem 2 的自相矛盾。Rejected：保留两个动作并在卡片上标出 closed —— 等于把矛盾画出来给用户看 |
| 5 | 标签**仍全局共享**，但可新建 / 改名 / 换色 / 删除 | 用户决定。Rejected：按项目隔离标签（要加列、要决定内置种子怎么分配，且跨项目统一筛选会变复杂） |
| 6 | 色板从五档的**语义名**改成 **8 档颜色名**（灰 / 红 / 红实心 / 琥珀 / 绿 / 钢蓝 / 蓝 / 紫），中性档用**描边**而非填充 | Problem 7：标签一旦可自建，色调名就不能再等于用途（`bug` / `feature` 是给内置目录起的名，配不上用户自己的标签），五档也不够分。现有五档按外观 1:1 映射到新名（`bug`→红、`critical`→实心红、`docs`→灰、`feature`→绿、`refactor`→钢蓝），琥珀 / 蓝 / 紫是净增，没有种子行需要改写。中性档改描边不依赖任何表面色，顺带解掉 Problem 8 的后半条。Rejected：保留语义名再加三档（名字与用途的矛盾原样留着）；新增 tone token —— 八档全都落在既有 token 上，见「呈现与主题」 |
| 7 | **抬起表面（popover / dialog）上的分隔线一律 `--border-strong`** | 实测 1.06 → 1.36（Problem 8）。Rejected：调暗 `--border` 本身 —— 它在 `--background` / `--card` 上是调好的，动它会波及全端 |
| 8 | 任务表单：**标题与描述不画框**、属性收成一排圆角 pill、面包屑当标题 | 用户提供的参考实现；标题是唯一必填，理应是唯一的大号输入。Rejected：三个等宽下拉一行；五行带字段名的表单行（现状） |
| 9 | 表单加 **Agent / 机器 / 模型**三颗 pill，全部复用现成组件；本轮**只存不读** | 用户在被明确告知「不接派发它们就是三个写进去没人读的字段」后仍选择只加选择器。Rejected：与派发链路一起做（量翻倍，且要先定重复派发时会话的复用键）；完全不做 |
| 10 | **不给看板加排序** | 看板的顺序是人拖出来的 `position`，加排序等于把拖拽结果盖掉 |
| 11 | 看板的**呈现层写进 `@agentre-hub/agentre-ui`**，两端共用一份 | 用户决定「本轮连 server 页面一起做」；`agentre/AGENTS.md` 与 `agentre-server/AGENTS.md` 的第 3 条都把跨宿主呈现件的唯一实现钉在共享包，手抄同步是明令禁止的。宿主留状态、导航、传输与平台操作，靠 props / ports 接入。Rejected：两端各写一份（被规则禁止）；桌面端先做、server 事后抽取（抽取要改动已经跑起来的桌面端代码，成本更高） |
| 12 | 任务**并入既有同步组**（`sync_objects`），新增 `issue` / `label` / `issue_label` 三个 kind，不另建同步机制 | 同步组本就是双向的、账号级的，冲突用账号级单调版本号收敛（`sync_entity.Wins`），墓碑、30 天窗口、游标全都现成。`project_agent` 这个连接对象已经是「join 也单独成 kind」的先例。Rejected：为任务新造一条同步通道（要重做冲突、墓碑与窗口三件事） |
| 13 | 载荷里**一律用同步标识**：`project_sync_id` / `agent_sync_id` / `agent_backend_sync_id`；`llm_provider_key` / `llm_model_key` 本就是字符串 | 第二条 Hard invariant。桌面端本地表照旧留自增外键供自己查询，只有**载荷**换成同步标识——这正是既有 adapter 的做法（`sync_svc/adapter.go:17-30` 的 `ref{Kind, SyncID}` 在接收端解析回本地 ID）。Rejected：把本地 ID 塞进载荷（会被两端守卫各自拒收，且在别的机器上指向完全不同的对象） |
| 14 | server 的写通道**复用组织面/项目面那条**：浏览器直写 `sync_objects` | 判据与那两轮同一条——载荷全是「指向」，没有一件是机器上的东西，所以任务可以在 web 上建改删。同理，**web 上不建 `agent_backend`**（它带本机可执行文件路径），机器那颗 pill 在 web 上只能从既有后端里挑，复用现成的 `SelectableBackends`。Rejected：web 只读（用户要的是能改） |
| 15 | server 端的筛选**在 Go 里做**，不为看板另建投影表 | 任务住在 `sync_objects.payload`（JSON）里，`ListByKinds(userID, kinds)` 现成（`sync_repo/object.go:183`）；建一张投影表等于给同一份数据开第二条写路径，与「一个概念一个实现」冲突。代价是过滤成本随账号任务数线性增长——**明确假设单账号任务量在千条量级以内**，超出属于另一轮的事。Rejected：MySQL JSON 函数索引（关键词 + 标签全部满足 + 三类时间范围叠起来，可维护性差） |

## 页面结构

页面自上而下三层，没有第四层：**标题栏**（在哪 · 新建）、**筛选条**（看哪些）、**看板**（做到哪一步）。

标题栏左侧是页名与一行计数（「N 个任务 · M 个进行中」），中间是项目选择器，右侧是「新建任务」。筛选条只有一个搜索输入框、一个「筛选」按钮，以及生效条件的 chip 行。

看板占满剩余高度，横向可滚；**每一列自己纵向滚动，列头常驻**。此前是整块看板一起滚，滚到下面就不知道自己在哪一列。

四列固定为 todo / doing / review / done，与 `issue_entity` 的 `Stage*` 常量一一对应，不新增列也不允许用户改列。

## 项目范围

选择器的触发器显示当前范围：`全部项目`、`未归属`，或某个项目的字形 + 名字。选中的是父项目时，触发器额外显示一枚 `+N` 徽标，N 为其子项目数量 —— 说明当前看到的不止这一个项目。触发器放不下时**先截断父级路径，保留子项目名**：两个不同父项目下的同名子项目若从尾部截断会变成同一个样子。

弹层顶部常驻「全部项目」与「未归属」两项，其下是项目树，只有树那一段滚动。项目按 `ProjectFlat.depth` **逐级缩进**，缩进处画一条竖引导线，第三层及更深也认得出挂在谁下面。每项右侧的计数是该项目**及其子树**里**未完成**的任务数，**不随筛选条变化** —— 打开选择器的目的就是判断该切到哪，这个数跟着当前筛选缩水就失去了这个用途。

**没有未归属任务时，「未归属」一项不出现**，不留一个点进去必定是空板的入口。

弹层内的项目搜索命中时，**命中项的祖先照常保留**，路径才看得见；祖先压暗但仍可选，命中片段高亮。这与 `/chat` 项目树现行规则一致（`session-index/index-page.tsx:569`）。

键盘上下移动的当前行与「已选中项」是两个视觉：当前行用 `accent` 底，已选中项才有选中底色与 ✓。打开时滚动到已选中项。

选中范围是 `全部项目` 时，卡片显示项目字形；范围是某个含子项目的父项目时，**父项目自己的任务不带字形，子项目的任务带一枚「↳ + 字形」**；范围是单个无子项目时，卡片不显示字形。判据是「当前范围里是否不止一个项目」。

## 搜索与筛选

一共六个条件：

- **关键词** —— 匹配标题、描述，以及 `#编号`（输入 `#179` 或 `179` 命中该条）。输入停手 200ms 才发起查询；查询期间旧结果留在原地，只有输入框右端出现一个转圈。命中数显示在搜索框右侧紧邻处。
- **项目** —— 见上一节，在标题栏而非筛选面板里。
- **标签** —— 多选，可选「任意一个」或「全部满足」，另有「只看没有标签的」。
- **更新时间** —— 不限 / 今天 / 7 天内 / 30 天内 / 自定义起止日期。
- **创建时间** —— 同上。
- **已完成保留多久** —— 30 天内 / 90 天内 / 全部。它替代「默认只显示最近 N 个」这种写死的数字，成为一个能说出口、能被摘掉的条件。

「筛选」按钮上的数字是**当前生效的条件个数**，不是标签个数。每个生效的条件都在 chip 行里出现一次并能单独摘掉；时间类 chip 带前缀（`更新于 今天`、`已完成 全部`）。chip 行放不下时自己横滚，不挤压搜索框与看板。

搜索或筛选生效时，看板本身有三处变化：列头计数变成「命中 / 全部」（`1 / 9`），零命中的列照常留在原位不塌缩；**已完成列自动展开、不再折叠**（否则命中会被藏在折叠行里，搜了却搜不到）；命中片段高亮，命中落在描述里时卡片多出一行不超过一行的摘录。

未筛选时已完成列只渲染最近若干张，其余折叠为一行「还有 N 个已完成」，就地展开而非跳转。

## 卡片与拖拽

卡片显示：编号、项目字形（按上节判据）、标题（最多两行）、标签（最多两个 + `+N` 溢出）、以及一行元信息 —— 描述非空时一枚图标、加相对时间。

卡片右上角有一个 hover 才浮出的菜单，包含**编辑**、**移动到 ▸ 四列**、**删除**。该菜单在卡片获得键盘焦点时一并显形，不让「hover 才出现」变成键盘用户够不着的功能。

拖拽时：原位保留一张虚线残影，被拖起的卡片微微抬起并旋转，**目标列整列高亮**。此前只有卡片自身换边框色，放到哪一列全靠猜。

空列显示「拖到这里」而非「暂无」—— 它是一个放置目标，应当说出它能干什么。

## 任务表单

同一个壳承担新建与编辑。

**主体只有标题与描述**，两者都不画输入框边框，靠字号分层级。标题是唯一必填。描述占据剩余高度。

**属性是正文底部的一排圆角 pill**，不写字段名，每颗自己显示当前值：阶段（带列头同款图标与颜色）、项目（带字形，可一键 × 改回「未归属」）。标签**不套在 pill 里** —— 标签本身就是 chip，直接站在这一行上，末尾一颗虚线 `+` 打开选择器，悬停某一颗露出它自己的移除键；卡片上的标签同样是裸 chip，两处一致。

属性 pill 与执行 pill 之间有一条竖线：左边说「这条任务是什么」，右边说「谁在哪台机器上用什么模型做它」。

头部是面包屑而非标题：新建时是「<项目名> › 新建任务」，编辑时第二段换成 `#<编号>`，右侧显示更新时间与一个更多菜单（删除收在其中），最右是关闭。

**默认值跟随上下文**：从某一列的「+」进入时阶段预置为该列；看板当前范围是某个具体项目时项目预置为它。

**编辑态阶段照常可改**，与卡片菜单的「移动到」说同一件事。

提交失败时错误块显示在**提交按钮正上方**，不在表单末尾。提交进行中按钮内转圈并禁用，其余字段保持可读不可改。

界面不显示任何快捷键提示。

## 标签管理

从筛选面板进入。列表逐行显示标签 chip、被多少个任务使用、以及改名与删除两个动作；底部是新建：名称输入 + 8 档色板。

色调固定为设计系统的 8 档，不开放自由取色。删除是软删（`labels.status` 已有该语义），但对使用者不可逆，因此先说清爆炸半径：「这个标签会从 N 个任务上移除，任务本身不受影响」。

## 执行归属（Agent / 机器 / 模型）

表单执行段的三颗 pill 全部复用既有实现，不新造控件：

- **Agent** —— `AgentAvatar`（共享包）+ 既有 agent 列表，与会话侧同一枚字形。
- **机器（执行目标）** —— `useExecTargetCandidates(agentId, projectId)`（`frontend/src/components/agentre/session-exec-target.tsx:54`）。候选行显示在线态、`kind`（local / desktop / daemon）、后端类型，以及**那台机器上这个项目的路径**；不可用的档保留并说明原因。
- **模型** —— 共享包的 `ModelTargetPicker`，pill 的呈现用同包的 `ProviderPillTrigger`（解析到模型即显示 `modelId` 且走等宽字体）。

三颗**触发器**统一成表单属性行上同一形状的 pill，不各用各的：`ModelTargetPicker` 的
`className` 会并进触发器的 `cn()`（`frontend/packages/agentre-ui/src/engine/model-target-picker/picker-trigger.tsx:80`），
执行目标的 chip 同理，两处都是现成的缝。Rejected：各自沿用组件自带的触发器 —— 机器是
tone 着色的小 chip、模型是 input 描边的方框，摆进同一排 pill 里三颗三个样子。

三者有**由接口本身决定的依赖顺序**：执行目标需要 `(agentId, projectId)` 才算得出候选；模型需要用生效档的 `backendType` 过 `providerCompatibleForBackend()`。因此**未选 Agent 时，机器与模型两颗 pill 为禁用态**；更换 Agent 或项目后两者重新解析，已选的档若不再兼容则退回「跟随 Agent 绑定」。

三个值随任务一并保存。桌面端本地表存自增外键，**同步载荷里换成同步标识**（`agent_sync_id` /
`agent_backend_sync_id`，见 Design decision 13）；`llm_provider_key` 与 `llm_model_key` 本就是字符串，原样过机。

web 上这三颗 pill 的能力与桌面端有一处**故意的差别**：**不能在浏览器里新建 agent backend**，只能从账号里已有的档中挑一个
（复用 `workspaceCtr.SelectableBackends`）。理由与组织面排除 `agent_backend` 写通道的理由是同一条：backend 的载荷带本机
可执行文件路径与透传环境变量，浏览器建出来的档必然不可用。

**本轮不存在任何读取这三个值的路径** —— 两端都不会因此启动任何执行。这是用户在知情下的决定，见 Design decision 9。

## 呈现与主题

落在 `--popover` / 对话框上的分隔线一律用 `--border-strong`；`--border` 保留给 `--background` 与 `--card` 上的分隔。

中性档标签与 `+N` 溢出计数使用**描边**（`--border-strong` + `--muted-foreground`）而非 `bg-secondary` 填充。

八档色调**全部落在既有 token 上，本轮不新增任何颜色 token**；八对都已在 `tokens.css` 的
`@theme inline` 里暴露成 Tailwind 工具类，实现时直接写类名即可：

| 色调 | 底 / 字 |
| --- | --- |
| 灰 | 描边 `--border-strong` + `--muted-foreground`（不填充） |
| 红 | `--destructive-soft` / `--destructive-text` |
| 实心红 | `--destructive` / `--destructive-foreground` |
| 琥珀 | `--status-waiting-bg` / `--status-waiting-text` |
| 绿 | `--status-running-bg` / `--status-running-text` |
| 钢蓝 | `--primary-soft` / `--primary-text` |
| 蓝 | `--tone-blue-bg` / `--tone-blue-text` |
| 紫 | `--tone-violet-bg` / `--tone-violet-text` |

`--tone-blue-*` 与 `--tone-violet-*` 在 `tokens.css` 里早已定义并暴露，但全仓没有任何一处引用
（`frontend/packages/agentre-ui/styles/tokens.css:442-445`）—— 这一轮是它们的第一个消费方。

加载态用四列骨架卡片，不是屏幕中央一个转圈；数据到位就地填充，页面不跳。

空态分两种，出路不同：「这个项目还没有任务」给「新建任务」；「没有符合条件的任务」给「清除筛选」。

窄到最小窗口宽度（860px）时，项目选择器整条换到标题栏第二行占满宽度；筛选按钮退化为纯图标。焦点环沿用既有的 `focus-visible:ring-[3px] ring-ring/40`。

## 文案与 i18n

术语统一为「看板 / 任务」，界面上不再出现 `Issue` / `Open` / `Closed` / `Board` / `List`：

| 现在 | 改成 |
| --- | --- |
| `Issues`（侧栏） | 看板 / Board |
| 新建 Issue | 新建任务 / New task |
| `{{open}} 个 Open · {{closed}} 个 Closed` | `{{total}} 个任务 · {{doing}} 个进行中` |
| 暂无（空列） | 拖到这里 / Drop here |
| 查看全部 {{count}} 个 → | 还有 {{count}} 个已完成 |
| 还没有 Issue（一句通用空态） | 拆成「这个项目还没有任务」与「没有符合条件的任务」 |

以下键整段删除：`issues.view.*`、`issues.filters.open` / `closed`、`issues.list.*`，以及从未被任何组件引用的 `issues.actions.dispatchToAgent` / `redispatch`、`issues.filters.assignedAgent`、`issues.status.unassigned`、`issues.board.lastFailure`、`issues.board.addCard` / `addToColumn`。

`zh-CN` 与 `en` 两棵树的键必须对齐。`README.md:45` 与 `docs/README_zh.md:45` 里「把任务交给 Agent / 回复创建关联会话」的描述与事实不符，一并改写。

## 数据与迁移

### 桌面端（`agentre`）

追加一条新迁移（编号取 `migrationList()` 末尾之后）：

- `issues` 增加 `agent_backend_id`、`llm_provider_key`、`llm_model_key` 三列，并复用既有的 `assignee_agent_id`；
- `issues` / `labels` / `issue_labels` 三张表各增加 `sync_id`（字符串，账号内唯一），**并给既有行逐行补发**
  —— 这是 Hard invariant 里「行不丢失」的具体落点：补发只写空值列，不改任何既有字段；
- `labels.tone` 的取值域从五个语义名改为 8 个颜色名，并把既有行的 `tone` 按 1:1 映射就地改写
  （`bug`→`red`、`critical`→`red_solid`、`docs`→`gray`、`feature`→`green`、`refactor`→`steel`）；
  标签的 `name` **不动** —— 改的是色调取值域，不是标签本身；
- `issues.agent_status` 与 `issues.source` 在本轮**不删除**（删列属于另一件事），但界面不再读取它们。

`issue_entity.Label.Check` 的 `allowedTones`（`internal/model/entity/issue_entity/label.go:17`）随之改为 8 档，并与前端的色调表保持同一份取值。

内置的五个种子标签在**每台机器上都存在同一份**（都来自 `migrations/202608080010_issues.go:68`）。补发 `sync_id` 时它们会各自
拿到不同的标识，首次上行后同一个「前端」会在账号里变成 N 份。因此种子标签的 `sync_id` **按名字确定性派生**（而不是随机），
让两台机器上的同一个种子标签天然收敛成同一个对象；用户自建的标签照常随机取标识。

### 同步（跨仓）

`internal/pkg/syncwire/wire.go:23` 增加三个 kind 常量并加进 `syncKinds`（`sync_svc/adapter.go:142`），顺序按「被引用者在前」插在
`project` / `agent` 之后：`label` → `issue` → `issue_label`。桌面端相应新增 `adapter_issue.go`
（三个 adapter，与既有 `adapter_project.go` / `adapter_org.go` 同形）。

载荷形状：

| kind | 载荷键 |
| --- | --- |
| `label` | `name`、`tone`、`status` |
| `issue` | `title`、`description`、`stage`、`position`、`project_sync_id`、`agent_sync_id`、`agent_backend_sync_id`、`llm_provider_key`、`llm_model_key`、`closed_at` |
| `issue_label` | `issue_sync_id`、`label_sync_id` |

服务端 `sync_entity` 的 `KindValid` 同步扩三项（`sync_entity/sync.go:26`）。载荷守卫两端都不需要改规则 ——
上表里没有任何一个键以 `id` 结尾配数字值。

**并发语义**：收敛依旧是「账号级单调版本号较大者胜」（`sync_entity.Wins`），逐对象生效。两台机器同时拖同一张卡 →
后到达 server 的那次赢，卡最终停在一列上；两台机器拖**不同**的卡 → 互不影响。`position` 是普通字段，不做特殊处理。

**首次上行的后果要说在前面**：两台机器各自积累的历史任务，首次同步后会**合并**出现在同一个账号下（种子标签除外，
见上）。这是「任务随账号走」的应有之义，不是缺陷；但它一旦发生就不可逆（撤销只能靠逐条删除），所以桌面端在
首次把看板并入同步时给一次一次性说明，而不是静默合并。

## 两端如何分工

`@agentre-hub/agentre-ui` 新增一族看板呈现件，两端共用同一份：列与列头、卡片、标签 chip 与 8 档色调表、项目选择器、
筛选面板与生效条件 chip 行、任务表单壳、四类空态与骨架。它们**宿主中立**：不 import 任何宿主类型，数据从 props 进，
动作从 ports 出（`onMove` / `onSave` / `onDelete` / `onQueryChange` …），文案走共享包自己的 `useUiTranslation` namespace。

留在各自宿主的：

| | 桌面端 `agentre` | `agentre-server` |
| --- | --- | --- |
| 取数与写入 | Wails 绑定 → `issue_svc` → 本地 SQLite | HTTP → `issue_ctr` → 直写 `sync_objects` |
| 路由 | 既有 `/issues` 页 | 新增 `/issues` 路由与页面 |
| 项目/Agent 列表 | 本地表 | `sync_objects` 的 `project` / `agent` |
| 拖拽 | dnd-kit（共享包提供呈现，宿主提供落库） | 同左 |

拖拽、键盘移动、筛选这些**交互状态**属于呈现件，随共享包走；「移动之后写到哪」属于宿主。

## `agentre-server` 端

**不新增任何表。** 任务、标签与两者的关联全部住在既有的 `sync_objects` 里，靠 `kind` 区分；读用
`sync_repo.ListByKinds`（`internal/repository/sync_repo/object.go:183`），写用组织面/项目面同一条「浏览器直写
`sync_objects`」通道 —— 分配版本号、落墓碑、把 `origin_fingerprint` 记成空串的既有逻辑原样复用。

新增 `/issues` 路由与页面，渲染与桌面端同一族共享呈现件。数据通道新增一组走既有 workspace 那条鉴权与写入模式的端点：
列表（含项目/标签/时间三类筛选参数）、建、改、删、移动列、标签的增改删。鉴权由本组的会话/JWT 圈定账号，**请求体里没有任何身份字段**
—— 与组织面、项目面完全一致。

筛选按 Design decision 15 在 Go 里做：一次 `ListByKinds` 取回账号下的 `issue` / `label` / `issue_label`，在内存里解析载荷、
连接标签、套六个条件。计数（项目选择器右侧那个「子树未完成数」）同一趟算出来。

web 与桌面端的**唯一功能差别**是不能在浏览器里新建 agent backend（见「执行归属」）。其余六个筛选条件、卡片菜单、
拖拽、标签管理、任务表单，两端一致。

## 交付顺序（跨仓，强制）

按 `AGENTS.md`「Cross-host frontend ownership」的依赖顺序，一轮里分三步提交，**不得并行**：

1. **`agentre`**：共享包里写呈现件与其测试 → 桌面端宿主切过去 → 桌面端后端（迁移、`sync_id`、三个 adapter、`syncwire` kind）
   → 验证 → 提交并**推送**。
2. **`agentre-server`**：钉住第 1 步推出去的 commit → 服务端 `KindValid`、`issue_ctr`、页面接上共享呈现件 → 验证。
3. 两个仓库**各自独立提交**，绝不把改动混进同一个提交。

第 2 步之前，`agentre-server` 里不写任何看板代码 —— 没有可钉的不可变版本时就动手，等于先造一份将来要删的重复实现。

## Out of scope

- **任务派发**：把任务交给 Agent 真正跑起来（建会话、回填 `session_id`、卡片运行状态、`agent_status` 活过来）。三颗执行 pill 本轮只写不读。
- **优先级、指派人、附件**：参考实现里有，本项目数据模型里没有，不在这一轮引入。
- **删除 `agent_status` / `source` 两列**：界面不再读取，列本身留待单独一轮清理。
- **把「分隔线用 `--border`」与「`bg-secondary` 填充」的排查扩到全端**（会话侧、`agentre-server`）：本轮只改看板范围内的用法。
- **看板列的自定义**：四列固定。
- **三个执行弹层（Agent / 机器 / 模型）本身的重构**：本轮**照现有实现原样复用**，弹层内部的
  主副行层级、分隔线、停用行、选中态一律不动。它们的问题已单独记录并出图（`agentre.pen` →
  「复用组件 · 现状 vs 新版」），改动会波及 `/chat` 与 `agentre-server`，单开一轮。
- **在浏览器里新建 agent backend**：web 只能引用既有后端，理由见「执行归属」。
- **为看板在 server 建投影表 / 加 JSON 索引**：Design decision 15 明确假设单账号任务量在千条以内；越过这个量级再谈。
- **把任务纳入 `agentre-hub`**：只有桌面端与 `agentre-server` 两端。

## Testing decisions

| Seam | What it verifies | Prior art |
| --- | --- | --- |
| `issue_repo.List` （sqlmock） | 项目子树集合筛选、关键词与编号匹配、标签「任意一个 / 全部满足」两种语义、三类时间范围 | `internal/repository/issue_repo/issue_test.go` |
| `issue_svc`（repo mock） | 子树 id 的收集、计数口径（未完成、含子树、不随筛选变）、标签增删改的校验与「被 N 个任务使用」统计 | `internal/service/issue_svc/issue_test.go` |
| `issue_entity.Label.Check` | 8 档色调取值域，越界即拒；五档旧值经迁移后不再出现 | `internal/model/entity/issue_entity/label_test.go` |
| `internal/app` DTO 转换 | 新增三个执行字段的进出映射 | `internal/app/issue_test.go` |
| 看板页组件（RTL） | 范围切换后请求参数、列头「命中 / 全部」、已完成列在筛选态展开、空态二分、卡片菜单的键盘可达 | `__tests__/issues-page.test.tsx` |
| 任务表单（RTL） | 默认值继承（列 → 阶段、范围 → 项目）、编辑态阶段可改、未选 Agent 时机器与模型禁用、提交失败时错误块位置 | `issue-new-dialog.test.tsx` |
| 色调守卫（既有测试扩写） | 8 档在两个主题、各表面上的对比度；中性档描边不依赖表面色 | `__tests__/issue-tones.test.ts` |
| i18n 守卫 | 两棵语言树键对齐、被删的键不再被引用 | `frontend/src/__tests__/i18n.test.ts` |
| `sync_svc` 三个 adapter（桌面端） | `issue` / `label` / `issue_label` 的上行载荷只含同步标识、下行能解析回本地 ID、墓碑往返 | `internal/service/sync_svc/adapter_test.go` |
| `syncwire.GuardPayload`（桌面端） | 三种新载荷全部通过；把 `project_id: 42` 塞进去必须被拒 | 既有守卫测试向量 |
| `sync_entity.ValidatePayload`（server） | 同上，两端规则逐条对齐 | `internal/model/entity/sync_entity/payload_test.go` |
| `issue_ctr` + service（server，repo mock） | 六个筛选条件在 Go 里的语义（尤其标签「全部满足」与三类时间范围）、计数口径、建改删各自分配版本号与落墓碑 | `internal/controller/*_ctr`、`internal/service/workspace_svc` 同形 |
| server 写通道鉴权守卫 | 请求体不含身份字段；跨账号读写被拦 | `internal/api/workspace/guard_test.go` |
| 共享呈现件（RTL，共享包内） | 列头「命中 / 全部」、空态二分、卡片菜单键盘可达、表单默认值继承 —— **一份测试同时覆盖两端** | `packages/agentre-ui/src/**/__tests__` |
| 共享包边界守卫 | 新增呈现件不 import 任何宿主类型 | `packages/agentre-ui/src/boundary.test.ts` |

无法自动化的部分：分隔线与标签在**暗色真实窗口**下的可见性（对比度可由色调守卫覆盖取值，但「这条线在这个弹层里看得见」需要人眼确认），以及拖拽的手感。两者在收尾时按 [docs/verification.md](../verification.md) 的路线跑一次真实应用并留下证据。

## Open questions

无。
