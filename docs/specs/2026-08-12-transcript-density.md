# 对话流密度：工具活动聚合与三档视觉权重

> Status: Draft
> Owner: chat experience / frontend
> Last updated: 2026-08-12

**Objective:** 让一轮对话按「正文是主线」来读 —— 把工具调用从「每次一张等重卡片」改成「按时间顺序聚合成一个可展开的活动块」，并按「这一步是否改变了工程」给三档视觉权重；使一轮里**需要用户注意的视觉标记**从「每次调用一个绿胶囊」降到「只有失败与运行中」。

**Hard invariants:**

1. **折叠是收起，不是藏。** 任何被折叠的步骤都能就地展开，且展开后可见**该步的参数与结果**。不存在「只能看到发生过、看不到发生了什么」的状态。
2. **折叠态不得让发生过的事消失。** 组头必须汇总本组的写操作（文件数 + 增删行）与失败计数；失败计数用 `status-error` 着色，且**不参与任何截断**。
3. **阻塞用户的卡片永不进组。** 工具权限请求、计划审批、向用户提问、内置写工具审批、OpenClaw 执行审批在任何情况下都独立可见 —— 折叠一个正在等你点的东西等于把会话卡死。
4. **时间顺序不得重排。** 遇到出组项就先结束当前活动块；正文与工具的先后关系与今天一致。
5. **流式内容不降级。** 正在流式输出的思考仍是整卡（它是那一刻唯一承载 live tail 的表面）；正在跑的活动块自动展开并显示实时尾巴；轮次结束后收起。
6. **分层不得依赖工具名硬集合。** 沿用 `frontend/src/components/agentre/canonical-tool/raw/summary.ts` 开头已经立下的规矩（「不依赖工具名硬集合 —— 那是 backend-specific 知识泄漏」），判据只用后端已算好的 `canonical.kind` 与 input shape。
7. **轮次级失败零回归。** `m.errorText` 驱动的 `ErrorCard`（`transcript-row-view.tsx:820`）仍在活动块之外、行为与文案不变。工具级失败是过程，轮次级失败是结论，两个信号继续分开。
8. **新增与改写的 UI 文案全部走 i18n**，`zh-CN` 与 `en` 双语，由 `frontend/src/__tests__/i18n.test.ts` 守卫。
9. **行级虚拟化与懒挂载不回归。** 一个 `TranscriptRow` 仍渲染成一个虚拟行；折叠态不得 mount 组内步骤的结果文本（`agent-spawn/card.tsx:1288-1293` 已确立的约定：一个 200 步子代理折叠时把几十 KB 隐藏文本挂进 DOM 会让整个 transcript 陪跑 layout/paint）。

## Problem

1. **每一次工具调用都是一张等重的卡片。** `transcript-card.tsx:32` 给出 `w-full max-w-measure rounded-lg border bg-card`，`canonical-tool/raw/card.tsx:126` 起的 `RawToolCard`、`file-edit/card.tsx`、`file-write/card.tsx` 全部用它。一轮里 13 次调用就是 13 张同样宽、同样有边框、同样浮在 `bg-background` 上的白卡，assistant 的正文被夹在卡片墙中间。用户报告：「调用的工具太多了，感觉很混乱」。
2. **成功是常态，却占了最高对比度的位置。** `raw/card.tsx:90` 取 `statusConfig[status]`，成功走 `running` 通道（`types.ts:102-107` = `bg-status-running-bg text-status-running`，绿底绿字），于是每一次成功的 Read 都挂一个绿色「完成」/「EXIT 0」胶囊。同一轮里 12 个绿胶囊传达的信息量为零，但它们是整屏对比度最高的元素。
3. **工具名占用品牌色。** `raw/card.tsx:144/147`、`file-edit/card.tsx:75`、`agent-spawn/card.tsx:420` 都用 `text-primary-text` 渲染图标与工具名。`docs/design.md` §2.3 规定「颜色是语义，不是装饰」，`primary` 的语义是「可交互 / 品牌」—— 现在每个不可点的工具名都在和真正可交互的元素抢注意力。
4. **读文件与改文件视觉重量完全相同。** 三张卡共用同一个 `TranscriptCardHeader` 结构与内边距，`Read` 与 `Edit` 在扫视时无法区分。用户扫一轮转录时最需要回答的问题是「哪一步真的动了工程」，当前版面不回答这个问题。
5. **「已思考」也是一张整卡，且与工具高频交替。** `thinking-block.tsx:133` 用的同样是 `TranscriptCard`。在开启 extended thinking 的后端上，`think → tool → think → tool` 是常态，于是「思考卡 + 工具卡」交替出现，任何只针对工具的聚合都会被思考打断成七八块而失效。
6. **子代理卡内部是同一个病的复刻。** `agent-spawn/card.tsx:1149` 的 `AgentSpawnStepCard` 把每一个子步骤又渲染成一张带边框、带左侧色条、带状态胶囊的小卡。展开一个 200 步的子代理就是卡片墙套娃。
7. **未知 / MCP 工具没有任何分层依据，也没有可读的名字。** `summary.ts` 只负责摘要，不负责分层；`RawToolCard` 对所有非 canonical 工具一视同仁。MCP 工具以 `mcp__<server>__<tool>` 的原始串直接显示，既长又难扫。而 MCP 工具里既有只读（`search_files`）也有写（`write_file`）与语义不明的（`execute`），当前一律同权重。
8. **失败要多点一次才看得到，且与成功共用同一套 chrome。** 失败卡除了边框换成 `border-status-error/40`、胶囊换成红色外，与成功卡结构相同、同样默认折叠；在一屏 13 张卡里，那一张红边框卡并不比其它卡更容易被扫到。

## Actors and user stories

1. 作为**读一轮长排障转录的用户**，我要一眼看到 assistant 说了什么、动了哪些文件，而不必在十几张等重卡片里找正文。
2. 作为**回头复查 agent 干了什么的用户**，我要能展开任意一步看到它的**参数与结果**，折叠不能等于信息丢失。
3. 作为**盯着 agent 实时跑的用户**，我要看到它此刻在做什么（当前这组自动展开、带实时尾巴），而不是等它跑完再一起出现。
4. 作为**需要判断这轮是否出过问题的用户**，我要在折叠态就看到「有 1 次失败」，并能一键展开定位。
5. 作为**派了子代理的用户**，我要子代理内部也按同一套规则呈现，而不是展开后掉进另一面卡片墙。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 一轮里连续的**思考 / 只读探查 / 写操作 / 命令 / 失败**聚合成一个**活动块**（一行组头 + 可展开的活动行列表） | 用户裁定「除 subagent 外工具都可以聚合」。聚合把常态压到只剩正文与组头。Rejected: 只聚合只读探查（方案 C）—— 写操作仍逐张成卡，长轮次仍是墙；Rejected: 只分层不聚合（方案 A）—— mockup 实测高度反而与现状持平（865px vs 1081px，且默认展开失败后 1095px），因为省下的行高被展开的失败输出吃掉 |
| 2 | **只有两类永远出组**：子代理、审批 / 提问 | 子代理自带一整棵执行树（提示词 / 步骤 / 结果 / token / 模型），不是一次调用；审批与提问阻塞用户交互，折叠等于卡死（Hard invariant 3）。Rejected: 失败也出组 —— 用户裁定「失败的不影响整个进程，可以折叠」，agent 循环里失败多半是它自己会修的中间态 |
| 3 | 失败**进组、只用红、不默认展开**；组头带红色 `N 失败` | 用户裁定。不藏事靠组头红标（Hard invariant 2）而不是靠把它提出去；「这次失败要不要紧」不需要推断，因为轮次级失败本来就有独立信号 `m.errorText`（Hard invariant 7）。Rejected: 失败默认展开 —— 一次中间态失败撑开三四百像素，与本轮目标相反 |
| 4 | **思考是活动块的组内成员**（组头统计带 `思考 N`）；**正在流的思考仍是整卡** | extended thinking 下 think/tool 高频交替（Problem 5），让思考打断聚合等于不聚合。流式态例外的理由是它是那一刻唯一承载 live tail 的表面（`thinking-block.tsx:70-77` 把 body 手动推到底部）。Rejected: 思考一律降为一行 —— 流式期间没有地方承载实时输出；Rejected: 思考不进组但也不打断组 —— 会让思考显示在它实际发生的位置之外，违反 Hard invariant 4 |
| 5 | **正文（assistant 文本）永远打断活动块** | assistant 的话是主线，不能被折进活动块。副作用是它天然把一轮切成「几段活动 + 几段结论」，这正是期望的阅读节奏 |
| 6 | 分层判据按顺序取第一个命中：① `canonical.kind` ② input shape ③ 都不匹配落**中性层** | Hard invariant 6。③ 是关键：**认不出来的工具不假装它是只读**，给介于读与写之间的重量，组头单列 `其它 N` 而不并进「查阅」。Rejected: 用 MCP tool annotations（`readOnlyHint` / `destructiveHint`）—— **已核实不可得**：agentre 只作为 MCP server 暴露自己的工具（`internal/service/{orgtool,subagent,hooktool}_svc/mcp.go` 的 `tools/list`），第三方 MCP 由 CLI 自己连接，前端只从 `tool_use` 帧拿到 name 与 input，全仓 `readOnlyHint` 零命中；Rejected: 维护一张工具名 → 层级表 —— 正是 `summary.ts` 明令禁止的 backend-specific 知识泄漏 |
| 7 | 组头汇总按**固定顺序**输出 `思考 / 查阅 / 改 N 个文件 +P −M / 写入 / 命令 / 其它 / N 失败`，最多 4 类 + `…`；**失败计数永不被截断** | 组头是折叠态唯一的信息出口，必须短到能扫、又不能丢掉「动了工程」和「出过错」。固定顺序让位置可预期。Rejected: 全量列出 —— 类目多时组头会挤成两行；Rejected: 只报步数 —— 等于把写操作藏了 |
| 8 | 活动块的默认折叠只看**运行态**（运行中展开、落定折叠），不看条数 | 落定后一律折叠给出可预期的阅读节奏；用户要看细节时点开即可。子代理内部另有阈值（决策 9），因为那层是用户已经主动展开过一次的 |
| 9 | 子代理**卡片形态保留**，但内部步骤区就是同一个活动块；**运行中**走活动块运行态（自动展开 + 超过 8 步省略尾部），**落定后**步骤 ≤ 20 默认展开、> 20 默认折叠 | 卡片保留是因为它承载的是一整棵执行树而非一次调用；内部递归同一套规则消除套娃（Problem 6）。阈值防止 200 步子代理展开成墙；运行态与转录对齐是 2026-08-17 增补 —— 此前阈值作为「活」的 fallback 在流式中途翻转，卡片开着也会在第 21 步当场收起。Rejected: 子代理也降为活动行 —— 会丢掉提示词 / 结果 / token / 模型这些只有它才有的结构 |
| 10 | **成功态不再显示状态胶囊**；状态语义反转为「没有标记 = 成功」，只有运行中 / 失败 / 待审批有标记。工具名与图标改用中性前景色，`primary` 归还给可交互元素 | Problem 2 / 3；`docs/design.md` §2.3「颜色是语义，不是装饰」。成功态腾出的位置改放实义信息：`+18 −4`、`13 passed · 1.42s`、`1204 行` |
| 11 | 折叠行报**对象**而非只报数量（`chat.go · chat-streams-store.ts +2`） | 纯数字无法让用户判断该不该展开 |
| 12 | 消息脚注（模型 / token / 耗时）与既有审批、计划卡形态**一律不动** | 用户明确裁定脚注不降级；审批与计划卡本轮不在范围内，改动它们会扩大回归面 |
| 13 | 活动行的展开体**按 `canonical.kind` 复用既有卡片正文**（`file.edit` → diff hunk、`file.write` → 文件内容、其余 → 参数 + 结果），而不是一律渲染成参数 JSON | 拆 plan 时发现的欠规定：`FileEditCard` 今天展开是 diff（`canonical-tool/file-edit/hunk-renderer.tsx`），若改成活动行后只给参数，等于把 diff 降级成一段 `old_string` / `new_string` 文本 —— 直接违反 Hard invariant 1。复用既有渲染器还能让本轮不触碰 diff 逻辑。Rejected: 展开体统一为参数 + 结果 —— 实现简单但丢掉 diff；Rejected: `file.edit` 继续走整卡不进组 —— 与决策 1 冲突，写操作正是最该进时间轴的一类 |

## 一轮转录的阅读结构

一条 assistant 消息自上而下由四种东西组成，除此之外不再有其它形态：

- **正文**（markdown），权重最高，排版不变；
- **活动块**：一行组头 + 可展开的活动行列表，承载思考、只读探查、写操作、命令与失败；
- **出组卡片**：子代理卡、审批 / 提问卡、轮次级 `ErrorCard`；
- **脚注**：模型 / token / 耗时 / 复制 / 重新生成，不变。

活动块按时间顺序生成：从上一个「正文或出组项」之后的第一步开始，到下一个「正文或出组项」之前的最后一步结束。遇到出组项时当前活动块立即结束并输出，随后从出组项之后重新开始积累 —— 任何情况下都不跨越出组项合并，也不重排。

## 分层判据

对每一步按顺序取**第一个命中**的判据，不匹配就继续下一条；全程不查工具名表。

1. **`canonical.kind`**（后端 translator 已算好）：`file.write` / `file.edit` → 写层；`agent.spawn` / `user.ask` / `plan.approve_request` / `tool.permission` → **出组**。
2. **input shape**（与 `summary.ts` 的探测同源）：含 `command` / `cmd` → 命令，归写层；含 `content` / `edits` / `old_string` 等写入语义字段 → 写层；只含 `path` / `pattern` / `query` / `url` 等定位语义字段 → 读层。
3. **都不匹配** → **中性层**，摘要退化为 `summary.ts` 现有的 `key=value` 兜底。

三档的可观察差异只在字重与前景色：读层最轻（次级前景色、常规字重），中性层居中（主前景色、中等字重），写层最重（主前景色、加粗）。三档的行高、内边距、交互完全相同 —— 分层是为了扫视，不是为了制造第二种控件。

工具名为 `mcp__<server>__<tool>` 形态时显示为 `server · tool`；其余工具名原样显示。

## 活动块的折叠态与展开态

**组头**（折叠态唯一的信息出口）自左至右为：展开指示 → 活动图标 → `N 步` → 汇总 → 耗时。汇总按决策 7 的固定顺序与截断规则；组内有失败时追加红色 `N 失败`，该项不参与截断。运行中的组头改为「正在执行 · 已 N 步」+ 当前步骤的实时尾巴，耗时位留空。

**展开态**是一列同构的活动行，左侧一条细线把它们串成时间轴。每行自左至右为：展开指示（静息时不可见，hover 或展开时显形）→ 工具图标 → 工具名 → 摘要 → 实义信息（`+N −N` / 结果预览）→ 规模（`1204 行` / `42 处`）。失败行的图标与工具名取 `status-error`，右侧显示红色 `exit N`，但**不默认展开**。

**任意活动行可就地展开**，展开体的内容**按该步的 `canonical.kind` 复用既有卡片正文**，不因为改成活动行就退化：

- `file.edit` → 既有的 diff hunk 渲染（增删行、多文件分块）；
- `file.write` → 既有的文件内容渲染；
- 其余（含命令、只读探查、中性层）→「参数」与「结果」两段：参数以键值对逐条列出，结果按现有 `collapsible-code.tsx`（`42bd344a` 已入库）的截断 / 展开 / 复制行为渲染。

这是 Hard invariant 1 的落点：折叠前能看到的东西，展开后必须一样能看到 —— 把一次 `Edit` 的 diff 换成一段 JSON 参数属于信息降级，不满足本条。

**单条不成组**：一段活动只有一步时不套「1 步」的壳，直接渲染那一行活动行。

## 运行态

当前正在跑的活动块自动展开，组头显示实时尾巴，轮次结束后自动收起（与 `thinking-block.tsx:80-87` 既有的「流式结束强制收回」同构）。正在流式输出的思考仍以整卡呈现在活动块之外；它结束后并入活动块，成为一条普通的思考行。用户手动展开 / 收起过的块，在本次会话内保持用户的选择，不被自动收起覆盖。

## 子代理

子代理卡的外形、展开区的「提示词 / 步骤 / 结果」三段结构、meta 行（模型 / 工具数 / token / 耗时）以及并行 / 链式的 run 分组全部保留。变化只有三处：

1. **步骤区就是一个活动块** —— 同一个组头、同一套活动行、同一套展开体；
2. 步骤区的展开态与转录同源分两段（2026-08-17 增补）：运行中走活动块运行态 —— 自动展开、超过 8 步省略只留最后 6 行（不再出现「卡片开着、流到第 21 步当场自己收起」）；落定后退回子代理阈值 —— ≤ 20 默认展开，> 20 默认折叠只留组头；
3. 卡头完成态**不再挂绿色状态胶囊**，改为 `N 步 · <token> · <耗时>` 的次级信息；名字不再使用品牌色。运行中 / 失败 / 取消 / 未知等非成功态的标记保持不变。

**已核实的限制：** 子代理内部的思考不进步骤列表 —— `agent-spawn/card.tsx:135` 的 `pairChildBlocks()` 只配对 `tool_use` / `tool_result`，thinking 块不在 childBlocks 中。因此子代理的活动块内暂时没有思考行；若后端将来透出该数据，按同一条规则并入即可，形态不需要改。

## 可访问性

组头与活动行都是原生 `button`，可 Tab 聚焦、带 `aria-expanded`、`focus-visible` 走既有的 `ring-ring/50`。折叠态靠颜色传达的信息（失败）同时有文字冗余（`N 失败` / `exit N`），不单靠红色。展开指示的显隐是视觉修饰，不影响可聚焦性与读屏播报。组头的实时尾巴区域使用 `aria-live="polite"`，与既有的打字指示器一致；所有动画带 `motion-reduce:` 降级。

## Out of scope

- **消息脚注**（模型 / token / 耗时 / 复制 / 重新生成）不做任何改动。
- **审批 / 提问 / 计划卡**（`tool.permission`、`plan.approve_request`、`user.ask`、内置写工具审批、OpenClaw 执行审批）形态不变，本轮只规定它们不进组。
- **`GroupedAgentSpawnCard` 的 run 分组**不动 —— 那是一层有意义的结构。
- **虚拟化机制**不改造，只要求不回归（Hard invariant 9）。
- **MCP tool annotations** 不纳入判据（决策 6 已核实不可得）；若将来 agentre 变成 MCP client 需另开一轮。
- **后端 / wire 改动**：本轮不新增后端字段；分层所需的 `canonical.kind` 与 input 都已在现有帧里。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 行模型纯函数（`transcript-rows.ts` 出口） | 聚合边界（正文 / 出组项打断、思考并入、单条不成组）、时间顺序不重排、出组分类正确 | `frontend/src/components/agentre/__tests__/transcript-rows.test.ts` |
| 分层判据纯函数 | canonical → shape → 中性层三条路径各自命中；未知 shape 不落读层；MCP 名拆分 | 无（新增）；判据来源与 `canonical-tool/raw/summary.ts` 同源，可参照其测试 |
| 活动块组件测试 | 组头汇总文案与截断规则、失败红计数不被截断、活动行展开后参数与结果均可见、失败行默认折叠、运行中自动展开 | `canonical-tool/raw/card.test.tsx`、`canonical-tool/agent-spawn/card.test.tsx` |
| 子代理卡组件测试 | 步骤区渲染为活动块、> 20 步默认折叠、运行中自动展开且超阈值省略尾部（跨过 20 步不收起、落定退回阈值）、完成态无绿胶囊、既有 run 分组行为不回归 | `canonical-tool/agent-spawn/card.test.tsx` |
| i18n 守卫 | 新增文案 zh-CN / en 双语齐全，无硬编码中文 | `frontend/src/__tests__/i18n.test.ts` |
| 排版守卫 | 新增行不引入被 token 取代的字面量（字号 / 宽度 / 阴影 / 圆角） | `frontend/src/components/agentre/__tests__/transcript-typography-guard.test.ts` |

**不适合自动化的部分：** 视觉层次是否真的「一眼能扫出哪一步动了工程」、以及流式期间的观感（自动展开 / 实时尾巴 / 收起时机），由收尾时按 `docs/verification.md` 驱动真实应用观察并留证据；行级虚拟化不回归由收尾时的源码复查 + 一次长轮次会话的滚动观察覆盖，不新增性能断言测试。

## Links

- 本地 mockup（决策证据，不在 Git 中）：`.dev-kit/artifacts/2026-08-12-transcript-density/mockups/`
  —— `08-d-light.png`（折叠态）、`10-d-expanded.png`（展开态）、`12-d-params.png`（参数 / 结果）、`11-d-running.png`（运行态）、`09-sub-compare.png`（子代理现状 ↔ 新规则）
- 设计系统：`docs/design.md`（§2.3 颜色语义、§3.5 状态色、§8 动效）
- 前端硬规则：`docs/frontend.md`（shadcn / i18n / lint）
