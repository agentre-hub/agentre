# 合并「对话 / 项目」为单一会话索引

<!-- File: docs/specs/2026-08-16-unified-chat-index.md -->

> Status: **Approved**（2026-08-16，用户裁决「可以接受」→「先做好」）
> Owner: 桌面前端（`agentre`，纯前端 + 一处 service 修复 + 共享包抽取）
> Last updated: 2026-08-17（决策 12：行与分组容器随本轮一起进共享包）
> Mockup（本地产物，不在 Git 里）：`.dev-kit/artifacts/2026-08-16-unified-chat-index/mockups/`
> —— `?view=now` 现状对照、`?view=project|agent|time` 三档分组、`?view=menus` 菜单、
> `?view=states` 行形态与状态、`?view=free` 随手对话组与三条入口。

**Objective:** 让「我要找的那条对话」只有一个地方可去。`/chat` 与 `/projects` 合并成一个索引，
「按项目」「按 Agent」退化成同一个列表的两种分组，并新增「按时间」一档。

**Hard invariant:**

1. **右栏一行不改。** `TabStrip` + `ChatPanelHost` 今天就挂在 `AppLayout` 上、两条路由共用同一份
   实例（`App.tsx:781` 的 `hasChat`、`:833-836`）。本轮只动左栏与它的数据通路。
2. **项目的全部管理能力不减。** 层级、同级拖拽排序、设置抽屉、新建子项目、新建终端、合并、
   指定本地路径、成员 agent picker —— 逐项保留，只是挂到分组组头上。
3. ~~**不新增后端 RPC**~~ —— **2026-08-17 推翻**，见决策 13。实现时发现三个轴里有两个
   的数据**根本拿不到**，这条不变式与决策 2/4/5/6 直接冲突。
4. 不改 `chat_sessions` 表结构，不新增迁移。

## Problem

1. **两个导航项其实是同一个列表的两种排序。** `chat_sessions` 一行同时挂 `agent_id` 与
   `project_id`（`chat_entity/session.go:36,48`，`project_id=0` = 自由会话），`/chat` 按 agent 分组、
   `/projects` 按项目树分组，右栏是**同一个组件树的同一份实例**。用户在两个导航项之间跳，
   只是为了换左栏排序，而看到的还是同一条对话。
2. **同一份投影写了两遍。** `agentSessionFromMeta`（`chat-page.tsx:61`）对
   `projectSessionToAgentSession`（`project-page.tsx:130`），逻辑同形；「选中锚点钉到末尾」
   也各写一份（`chat-page.tsx:137-153` / `project-page.tsx:1067-1072`）。
3. **两条数据通路对同一批行给出不同答案。** 项目页走 `ProjectListSessions` 快照、**绕开
   session-meta-store**，因此只能内联 `computeAttention`（`project-page.tsx:1007-1023` 那段长注释
   就是在解释这次绕行）；对话页走 meta-store。两边对「未读」的判断可能不一致。
4. **项目页每秒轮询一次全量刷新**（`project-page.tsx:239-254`），对话页是事件驱动。
5. **自由会话（`project_id=0`）在项目视角里完全不存在**，而且**没有任何 RPC 能改一条会话的
   `project_id`**（全仓库 grep 无匹配）——一条会话一旦落成自由会话就再也进不了项目。
6. **筛选与搜索双份且已漂移。** 对话页 chips 是 全部/运行中/未读 N，项目页只有 全部/运行中；
   搜索框一个 `bg-background` 一个 `bg-input-bg`；侧栏宽度两个持久化键（`"chat"` / `"projects"`）。
7. **删项目会留下悬空会话。** `project_svc.Delete`（`project.go:244-272`）拒绝有 running/waiting
   会话的项目，但**对 idle 会话什么都不做** —— 项目行删掉后那些会话的 `project_id` 仍指向一个
   不存在的项目。今天表现为「它们在项目页消失了」；合并成单一索引后会变成
   **同一条会话「按 Agent」看得见、切到「按项目」就人间蒸发**。

## Actors and user stories

1. 作为**在多个仓库之间来回的用户**，我想在一个地方看到全部对话，不必先决定去「对话」还是「项目」。
2. 作为**只想接着刚才那件事干的用户**，我想按时间平铺，一眼看到最近动过的那条，不必先想它属于哪。
3. 作为**随手问一句的用户**，我想在项目视角下也能开一条不挂项目的对话，并且事后还找得到它。
4. 作为**管理项目的用户**，我要的设置 / 子项目 / 终端 / 合并 / 删除一个都不能少，只是别再单独占一个页面。
5. 作为**删掉了一个项目的用户**，我不希望里面的历史对话从某个视角里凭空消失。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 导航去掉「项目」，只留「对话」；`/projects` 重定向到 `/chat`。 | 一个只改变左栏排序的导航项是在对撞导航语义。`/projects?focus=<id>`（会话设置页点「项目」进来，`project-page.tsx:364-381`）改为重定向并打开该项目的设置抽屉。拒绝保留两个入口（问题 1 原样不动）。 |
| 2 | 分组维度是一个**视图控件**，三档：项目（默认） / Agent / 时间。 | 用户裁决（2026-08-16）。它是唯一新增的控件。默认「项目」，因为项目绑本地路径与执行目标，是干活时的真上下文。拒绝把分组做成路由（那就是今天的两个导航项换个说法）。 |
| 3 | 分组选择器只放「图标 + 当前值 + chevron」，**不带「分组」二字**。 | 320px 侧栏减 `px-4` 只剩 288px；带标签时「分组 Agent」这一档会把 `未读 N` chip 挤到第二行（mockup 第一版实测）。可发现性交给 `title="分组方式"`。拒绝带标签；拒绝把选择器单独占一行（多一行纯损失）。 |
| 4 | 行首固定一个 14px 槽位，放**分组没说的那一维**：分组＝项目 → agent 头像；分组＝Agent → 项目色文件夹字形。 | 同位置同尺寸，切分组时列表不跳动。自由会话在「按 Agent」下槽位保留、字形置灰，行的左缘不参差。拒绝「按 Agent 时不渲染字形」（左缘参差）；拒绝把另一维放行尾（与 trailing 状态文案争位）。 |
| 5 | 「按时间」用**两行行式**：第一行标题 + 状态，第二行 `agent · 项目`。 | 这一档没有组头吃掉纵向空间，两行放得下；而它恰恰是唯一需要同时给出两维的一档。拒绝单行塞两个字形（两个无文字图标读不出来）。 |
| 6 | 「随手对话」组（`project_id=0`）在「按项目」下**常驻**，空态也在。 | 一条自由会话都没有的用户，在项目轴下否则看不到任何「不属于项目」的入口 —— 功能在（命令面板项目选择器第一项就是「无项目」，`command-palette.tsx:723`），入口不在。组头有 `＋`（直接弹 agent picker、不带项目上下文），**没有 `⋮`**（虚拟组没有设置 / 子项目 / 合并 / 删除可言，挂菜单是骗人）。 |
| 7 | 命名用「随手对话」，不用「未归项目」。 | 前者是一个正当的去处，后者读起来像分类失败的残留。**前提是 R7 修掉**，否则这个组会混进「故意不挂项目」与「项目被删了的孤儿」两种人口。 |
| 8 | 筛选 chips 统一为 全部 / 运行中 / 未读 N；搜索合并成一个框，同时匹配会话标题 / 项目名 / agent 名。 | 项目视角什么都没丢，还白捡了「未读」。侧栏宽度收敛到一个持久化键 `"chat"`。 |
| 9 | 拖拽排序只在「按项目」下开启。 | 沿用今天「搜索 / 筛选生效时禁用拖拽」的规则（`project-page.tsx:449`）：顺序在过滤后的列表里没有意义。 |
| 10 | 项目页那条**每秒轮询**删除；`ProjectListSessions` 的结果并入 session-meta-store。 | 问题 3/4 同源。收敛后 attention / 未读只算一次，`project-page.tsx:1007-1023` 那段绕行注释与内联 `computeAttention` 一并删除。 |
| 11 | 头部 `＋` 指向命令面板 new-chat 模式；「新建项目」降为该下拉的次级项。 | 统一创建的原语已经存在（`new-project-chat-source.tsx` + `new-chat-context-store` 支持 Tab 切项目上下文、循环到「无项目」）。今天 `＋` 有三种含义（新对话 / 新项目 / 项目内挑成员），收敛成一种。组头 `＋` 作为「分组已填好」的快捷路径保留。 |
| 12 | **`SessionRow` 与 `SessionGroup` 随本轮重写一起搬进 `@agentre-ai/agentre-ui`**；`buildGroups`（轴的定义）、状态词汇投影、标题退化规则、导航**留宿主**。 | 用户裁决（2026-08-17）。见下节。 |
| 13 | **新增 `ListChatIndexSessions(scope, offset, limit)`**，`scope ∈ {recent, free}`；并给 `ChatSessionLite` 补 `AgentID` / `ProjectID`。 | 用户裁决（2026-08-17，「增加新接口，然后做分页不就行了么」）。推翻硬约束 3。见下节。 |

## 决策 13：三个轴里有两个拿不到数据

实现时查证出的三条硬约束，规格原先都没预料到。

### F1 —— 行首那一维在载荷里根本不存在

决策 4 要求「按 Agent」分组时行首放项目色文件夹字形，决策 5 要求时间轴第二行给
`agent · 项目`。但对话侧唯一的数据源 `ListChatAgents` 的载荷 `ChatSessionLite`
（`chat_svc/types.go:484`）**既没有 `AgentID` 也没有 `ProjectID`** —— 项目归属只有
`ChatSessionDetail` 才带，也就是「这条会话被打开过」之后才知道。

`session-meta-store` 里那个 `projectId?: number` 因此长期是空的；今天没暴露，是因为它
唯一的用途是给标签页取色。

**修法**：给 `ChatSessionLite` 补两个字段并在 `sessionLiteFromEntity` 填上。既有响应加
字段，不是新增 RPC。

### F2 —— 两个 RPC 的完整性契约不同，时间轴没有数据源

| RPC | 给什么 |
|---|---|
| `ListChatAgents` | 每 agent **前 5 条** + attention 至多 20（`chat.go:434,446`），其余靠翻页 |
| `ProjectListSessions(pid)` | 该项目**全量**，无 limit |

所以项目轴用后者、Agent 轴用前者各自成立，但**时间轴只能是两者的并集 —— 一个窗口而
不是全量**，且时间轴按决策 5 没有组头，也就没有「查看全部 N」这个出口。

更糟的是**「随手对话」组同样无源**：`project_svc.ListSessions` 挡在 `projectID > 0`
（`project.go:334`），而 0 本来就不是一个项目 —— 自由会话此前只能靠「碰巧落在某个
agent 的前 5 条里」被看见。决策 6 要求这个组常驻，它却没有查询能填。

**修法**（用户裁决）：新增一个分页接口同时补上这两个缺口。

```
ListChatIndexSessions({ scope, offset, limit }) -> { sessions, total, hasMore }
  scope = "recent"  跨 agent、跨项目的全局最近活动  →「按时间」档
  scope = "free"    project_id = 0 的会话           →「随手对话」组
```

- **具名 scope 而不是 `projectID = -1` 这类哨兵**：哨兵在 Wails 生成的 TS 签名里读不出
  含义，调用方迟早传错一个 0 进来。认不出的 scope **拒绝**而不是默默当成 `recent` ——
  静默降级会让调用方以为拿到的是「随手对话」，实际是全部会话。
- 分页口径与 `ListAgentSessions` 一致（默认 20 / 上限 100），前端两处翻页逻辑同形。
- repo 侧 `ListRecentPaged` / `ListFreePaged` / `CountAll` / `CountFree` 共用一个
  `indexScope`，`nonSubagentScope` 照旧无条件挂上（子 agent 委派会话不进任何列表）。

### F3 —— 1s 轮询还在刷项目树，而树没有推送通道

`reloadSidebarSources()`（`stores/sidebar-reload.ts:17-20`）只刷 `chat-agents-store` 与
`project-sessions-store`，**不刷 `ProjectListTree`**；全仓库 12 个 `EventsOn` 里没有一个
是项目变更。所以按决策 10 删掉轮询，代价是**另一台设备同步过来的项目要等一次别的交互
才出现**。

**修法**：把项目树刷新并进 `reloadSidebarSources()`。那本来就是「刷新左栏数据」的统一
入口，树今天不在里面只是因为有轮询兜着。

## 决策 12：什么进包、什么留宿主

agentre-server 的 `SessionList.tsx` 是同一个组件功能更少的版本：一样是「分组 → 组头（头像 + 名 + 计数）
→ 行（状态点 + 标题 + 尾部元信息）」，但**没有**折叠与持久化、attention 气泡、「查看全部 N」溢出、
行右键菜单、与气泡的去重。所以共用的收益不是消重，是 server 白捡这五样。

**进包**（`src/session-index/`）：

| 搬什么 | 为什么它是包的 |
|---|---|
| `SessionRow` | 纯展示：状态点 + 标题 + 尾标，两端像素级同一件东西 |
| `SessionGroup` | 折叠动画 / localStorage 持久化 / attention 气泡 / 溢出 popover / 去重 / 空态 —— 全是与业务无关的容器行为 |
| `ui/popover.tsx`、`ui/context-menu.tsx` | `SessionGroup` / `SessionRow` 的直接依赖，包里没有（只有 badge/button/hover-card/input/spinner/textarea/tooltip），**agentre-server 也没有**（只有 alert/button/card/dialog/input）。两个都是 `radix-ui`（已是 peer dep） |
| `sidebar-expanded-state`、`isOpenInNewTabModifier` | 纯函数。localStorage 前缀 `agentre.agentExpanded.` **保持不变** —— 存着真实用户的展开偏好 |

**留宿主**（这是决策的重点，不是省事）：

- **分组本身。** 桌面要三轴用户可选，server 要「桌面按 Agent / 移动按状态」由视口决定
  （`SessionList.tsx:14`，决策 12）。两边都对；塞进包就得二选一。**包收已经建好的 `groups`。**
- **状态词汇。** 桌面是 `AgentStatus`，server 是 wire 的 `lifecycleState` 字符串 +
  `waitingForInput`。转换属于投影。
- **标题退化。** `degradedTitle` / `sessionTitle`（`SessionList.tsx:119-136`）那套「R7 之前的老会话
  没标题就退化成 `cwd · backend · 状态`，不猜」是 server 独有的真相。
- **导航。** 见下面第 2 条接缝。

**要新开的两条接缝：**

1. **`attentionRank` 去掉对宿主 store 的依赖。** 今天 `agent-list.tsx:23` 是
   `import type { AttentionReason } from "@/stores/attention-store"` —— 边界守卫是**文本扫描**，
   `import type` 照样拦（`boundary.test.ts:21`）。行里这个值只用于排序与「展开态过滤 selected」，
   改成包自有的联合类型，宿主的 `AttentionReason` 赋值进来即可。
2. **导航要给真链接。** server 的行是 `<Link to>`（`SessionList.tsx:267`），桌面的行是
   `<button onClick>` 开标签页。包**故意**不收 react-router-dom（`boundary.test.ts:143`：
   「跳转是外壳能力，该走 ports」）。浏览器里若只留 `onClick`，会**悄悄弄丢中键 / ⌘ 点击 /
   复制链接地址**。所以 `SessionRow` 收可选的 `href` + `renderLink`：给了就渲染成链接，
   不给就是今天的按钮。拒绝「两端都用 onClick」（浏览器语义受损），拒绝包内 `useNavigate`（越界）。

**不受影响的一件事：配色。** 行用到的 `sidebar-active-bg` / `primary-soft` / `primary-text` /
`subtle-foreground` / `status-waiting` / `text-2xs` 六个全在共享 `tokens.css` 里，server 的
`globals.css:19` 已经 `@import` 了。

**落地顺序的硬约束**：agentre-server 通过 **SHA 钉死**消费本包（`package.json:16`，
`ccd19e6f`），所以 server 用上这批组件必然在本轮落地、SHA 提升之后。本规格只负责把组件搬进包
并让桌面端用起来，**不含 server 侧的接入**。

> 顺带一处对齐：决策 5 的「按时间」两行行式，与 server 的 T6 两行行（Row1 状态 + 标题 + 时间，
> Row2 设备 · 后端）是同一个结构，只是 Row2 的内容不同。两端本就该是一个组件。

## 索引的结构

**一个 axis（分组维度）由三件事定义**：怎么从一条会话取出组键、组头长什么样、行首那 14px 放什么。
本轮三个 axis 全部写在桌面端本地，**不进共享包** —— 等 web 侧也要用时再抽（见「Out of scope」）。

| axis | 组键 | 组头 | 行首 14px |
|---|---|---|---|
| 项目（默认） | `session.projectID`（0 → 随手对话组） | 项目树组头（含全部既有操作） | agent 头像 |
| Agent | `session.agentID` | agent 组头（pin / ＋ / 折叠） | 项目色文件夹字形（自由会话置灰） |
| 时间 | 无分组 | 无 | 两行行式，两维都给 |

分组值持久化到 `localStorage`，与侧栏宽度同层。

## 组头保留清单（决策 2 的硬约束）

**项目组头**：图标 + 名称 + attention 数 + `＋`（成员 agent picker，`NewSessionMenu` 原样搬）
+ `⋮` / 右键（项目设置 / 新建子项目 / 新建终端 / 指定本地路径 / 合并到已有项目 / 删除项目）。
运行中的品牌底色与左侧 3px 绿条保留（`project-page.tsx:1280-1286`）。子项目递归、同级拖拽排序保留。

**Agent 组头**：头像 + 名称 + 未配置徽标 + pin 开关 + `＋` + 折叠箭头 + 「查看全部 N」分页 popover。

## R7 —— 删项目时把幸存会话的 `project_id` 置 0

`project_svc.Delete` 在删掉项目行之后，把该项目名下**剩余的 active 会话** `project_id` 置 0，
它们成为正经的自由会话、落进「随手对话」组。

**修在 producer。** 不得在索引侧加「查不到项目就归到随手对话」的兜底 —— 那是拿 consumer 的
guard 掩盖 producer 的 bug（AGENTS.md 高优先级约束 3）。

先写回归测试并**看它红**：给一个有 idle 会话的项目调 `Delete`，断言那些会话的 `project_id` 变成 0。
现状下这条会红（今天什么都不做）。

## 实现期补充（2026-08-17）

规格没写、实现时必须定的几条，记在这里以免下次重新争论：

1. **搜索的粒度。** 一条会话被留下的条件是「**它自己命中，或它所属的组命中**」。
   搜 agent 名 / 项目名时该组名下的会话全留（你在找那个组），搜标题时才收窄到那几条。
   合并前两边各偏一半 —— 对话页只在 agent 级过滤（组内的行一条不减），项目页在项目级
   过滤（同理）；两者都无法表达「我在找这句话」。
2. **筛选生效时不显示「查看全部 N」。** 翻页拉回来的是**未过滤**的下一页，混进过滤后的
   列表只会让人以为筛选漏了东西。
3. **项目轴按树递归渲染，而不是把 `depth` 摊平成缩进。** 折叠父项目要真的把子项目一起
   收起来，同级拖拽也要按层建 `SortableContext`。
4. **`groupSessionsByAxis` 这个抽象被删掉了。** 它按「拿一个扁平会话列表、按维度分桶」
   设计，而决策 13 之后三个轴各有自己的分页查询、会话拿回来时**已经分好组**，没有可分
   的桶。`session-axis.ts` 只剩 `flattenProjectTree`（项目轴的组顺序）。
5. **「随手对话」的 `＋` 常驻可见**，不跟项目组头一样 hover 才出。决策 6 的整个理由是
   「功能在、入口不在」，一个 hover 才出现的入口把这条理由抵消掉了。
6. **`⌘D`（`nav.projects`）改成「切到项目分组轴」**（用户裁决）。「项目」不再是一个页面，
   快捷键跟着改口径：落到 `/chat` 并把 axis 切成 `project`，且和点 AxisPicker 一样持久化。
   动作 id 保留 `nav.projects` —— 它是**持久化键**，改名会让用户已经改过的绑定失效，而
   它的语义（「去项目那边」）并没有变；变的只是项目那边现在是一个轴。文案两份 locale
   都改成「按项目分组」。
   连带的一处结构变化：axis 从索引页的 `useState` 提升成 `stores/sidebar-axis-store.ts`。
   `ShortcutsProvider` 不在索引页的组件树上，只写 localStorage 叫不动已经挂载的索引。
   持久化仍留在 `lib/sidebar-axis-state.ts`（键 / 默认值 / 非法值回落），store 只管活状态。
7. **父项目的「自己的会话」子分组已恢复**（用户裁决）。一个**既有自己的会话、又有子项目**
   的项目，会话下沉进可独立折叠的 `project:<id>:sessions` 内层组；父级箭头仍收整卡。
   只有自己的会话、没有子项目时不套这一层 —— 那会凭空多出一行永远只有一个子项的组头。
   折叠态气泡留在**外层**：整卡收起时要冒的是整棵子树的 attention，内层组头这时候
   根本不在屏幕上。
   `ProjectLevel` 因此要在「这一层筛选后还剩几个子项目」上多算一步：一个都不剩时
   `children` 传 `undefined`，而不是一个只会返回 `null` 的 `<ProjectLevel>` —— 后者是
   非空 ReactNode，会骗过「有没有子项目」的判断。

### 代码复核抓出的实现回归（已修）

重写 1774 + 816 行时掉了一批合并前就有的行为。复核逐条列了出来，现在每条都有守卫
（`__tests__/index-group-row.test.tsx` 与合并后的 `session-index-page.test.tsx`）：

| 掉了什么 | 后果 | 现在 |
|---|---|---|
| 置顶 RPC 传错字段名 | `SetAgentPinned` 收到 `ID=0` → 置顶完全不工作，`as never` 让 tsc 看不见 | 传 `{id, pinned}` |
| `?focus=` 在项目树到达前就把 query 删了 | 冷缓存下抽屉永远打不开 | 等 `useProjectTree().loaded` |
| 两个 pin 文案 key 两份 locale 里都不存在 | 按钮的 `aria-label` 直接渲染出 `agentList.pin` 字面量 | 补齐；i18n 守卫漏掉它是因为 key 在三元里 |
| `renderSessionsPopover` 从没传过 | 「查看全部 N」点开是空的，翻页在任何轴上都不可达 | 三种组各自接到对应的分页接口 |
| 子树 attention 汇总与运行中高亮 | 折叠父项目 → 子项目里在跑/未读的会话彻底看不见 | 组头计数与气泡都看整棵子树 |
| 项目组默认折叠 | 首次启动是一棵全关的文件夹树 | 只有 Agent 组默认折叠（对话页旧行为） |
| 筛选只收窄行、不收组头 | 满屏是空的项目组头 | 子树无命中的项目整个不渲染 |
| 索引载荷没有 agentName/agentColor | 落在 agent 前 5 条之外的会话，行首字形与第二行是空的 | 经 agent 列表解析 `agentId` |
| 空态 / agent 组头点击 / 全部路径缺失说明条 | 三处入口与提示消失 | 逐条恢复 |
| 排序失败无出口 | unhandled rejection，用户只看到「弹回去了」 | try/catch + 错误条 |

## Out of scope

- **把一条会话从「随手对话」移进项目。** 需要新增改 `project_id` 的 RPC，本轮不做；
  「随手对话」组本轮只可见、不可拖出。
- **agentre-server 侧的接入。** 本轮只把组件搬进包并让桌面端用起来。server 那侧要改
  `SessionList.tsx`、加一个 `SessionSummary → 行模型` 的投影、并提升 SHA —— 那是
  agentre-server 仓库的改动，**不能与本仓库的提交混在一起**。
- **axis 的分组逻辑进共享包。** 按决策 12 这是**有意留在宿主**的，不是推迟：两端的轴天生不同。
  本轮唯一可能进包的是分组结果的**类型形状**，若两端不一致则先各留各的
  （见 [`2026-08-16-web-console-convergence.md`](./2026-08-16-web-console-convergence.md)）。
- **「按时间」的虚拟化。** 分组两档靠折叠懒展开；平铺档在会话量大时需要虚拟化，本轮先不做，
  若实测卡顿再单开一轮。
- **议题 / 组织 / Hooks 三个导航项**，本轮不动。
- **会话行右键菜单的能力变化**，沿用今天的改名 / 新标签打开 / 删除。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `project_svc.Delete`（Go 单测，**先红**） | R7：删项目后名下 idle 会话 `project_id` 变 0；仍有 running/waiting 时照旧拒绝、一条都不改；置零失败时删除整体失败（不留半个状态） | `internal/service/project_svc/` 既有 mockgen 结构 |
| axis 投影（纯函数 vitest） | 三个 axis 各自的组键、排序、随手对话组归属；`project_id` 指向不存在的项目时**不**兜底（R7 之后不该出现，出现即数据问题） | 新建；参照 `lib/__tests__` 既有纯函数测试 |
| 行 context（vitest） | 分组＝项目时行首是 agent 头像、＝Agent 时是项目字形、自由会话字形置灰且**槽位仍在**（守卫：行左缘对齐）；＝时间时两行且两维都在 | 既有 `agent-list` / `session-group` 测试 |
| 侧栏头部（vitest） | 分组选择器三档切换与持久化；chips 三档单选；**320px 下 chips 不换行**（守卫，决策 3 的理由） | 既有 `chat-page.test.tsx` |
| 随手对话组（vitest） | 无自由会话时组仍渲染且有空态与 `＋`；组头**没有 `⋮`**（守卫断言，决策 6） | 新建 |
| 组头能力清单（vitest） | 项目组头六项菜单与 `＋` 逐项可达；拖拽在非「按项目」档禁用 | 既有 `project-page.test.tsx` 的菜单断言，迁移 |
| 数据通路（vitest） | `ProjectListSessions` 结果写入 meta-store 后，attention 与对话页一致；**没有第二处 `computeAttention` 调用**（守卫：全仓库只剩 store 里那一处） | 新建 |
| 无轮询（守卫） | 索引挂载后不存在 1s 定时全量刷新 | 新建 |
| e2e | `e2e/tests/sync-client.spec.ts` 的 `nav-projects` 定位改为分组切换 | 既有 |
| `chat_repo` 索引查询（sqlmock，**先红**） | `ListRecentPaged` 不带 agent / project 过滤且首页不发 OFFSET；`ListFreePaged` 只要 `project_id = 0`；两个 Count 与之同 WHERE；`nonSubagentScope` 都挂着 | `internal/repository/chat_repo/session_test.go` 既有 sqlmock 断言 |
| `chat_svc.ListIndexSessions`（mockgen，**先红**） | 两个 scope 各走对应 repo 方法；每行带 `AgentID` / `ProjectID`（自由会话如实报 0）；limit 默认 20 / 上限 100；nil 请求、未知 scope、负 offset **在碰 repo 之前**就拒绝 | `internal/service/chat_svc/` 既有 mockgen 结构 |
| 包边界守卫（既有，**必须继续绿**） | 搬进去的 `SessionRow` / `SessionGroup` 不引用 `@/`、`wailsjs`、宿主 store；新增的裸依赖都在 `package.json` 里声明 | `packages/agentre-ui/src/boundary.test.ts` |
| `SessionRow` 的链接接缝（包内 vitest） | 给 `href` 时渲染成真链接（中键 / ⌘ 点击 / 复制链接地址可用）；不给时是按钮且行为与今天一致 | 新建；接缝 2 的守卫 |
| 组件搬迁等价性（vitest） | 搬迁前后桌面端行为不变：折叠持久化、attention 气泡去重、溢出 popover、右键菜单三项 | 既有 `session-group` / `agent-list` 测试，随组件一起进包 |

**测试迁移量**：`project-page.test.tsx`（2247 行）与 `chat-page.test.tsx`（1055 行）需要合并去重。
两份里对同一行为的重复断言应收敛成一份，不要机械照搬到新文件。

**合并时无家可归的三块覆盖（已各自安家）**：这两个文件里有三组测试并不测「页面」，而是测
被页面顺带渲染的组件；页面没了，它们会跟着蒸发。已分别落位：

| 覆盖 | 新家 | 备注 |
| --- | --- | --- |
| `NewTerminalSubMenu` 的设备矩阵（本地 / 在线有路径 / 在线无路径 / 离线） | `__tests__/project-group-header.test.tsx` | 旧测试为此**只为测试导出**了内部组件并搭了一个 DropdownMenu harness；现在从组头的 ⋮ 菜单真实打开子菜单，那个导出不再需要 |
| `＋` 选人菜单的两条回归：关闭时 Radix 不夺焦、重开要重取成员 | `__tests__/project-group-header.test.tsx` | 两条都验证过**去掉修复就变红**，不是白测 |
| `ProjectSettingsDrawer`（成员显示名优先于 `Agent #id`、R10 本机路径两态） | `__tests__/project-settings-drawer.test.tsx` | 组件本轮一行没动，但覆盖一度归零 |

## 相关链接

- 会话生命周期：[`session-lifecycle.md`](../session-lifecycle.md)（`chat_sessions` 的创建与复用规则）
- 后续：[`2026-08-16-web-console-convergence.md`](./2026-08-16-web-console-convergence.md)（web 侧复用同一套索引与 axis 契约，未批准）

## Open questions

无。
