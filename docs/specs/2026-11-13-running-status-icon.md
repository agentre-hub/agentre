# 状态标记改图标

> Status: Approved
> Owner: 共享 UI 包（agentre-ui）+ 桌面端；agentre-server 钉版本跟随
> Last updated: 2026-11-13

**Objective:** 转录、会话列表和几处弹层里的状态标记，凡是图标能说清的都换成图标；中文界面不再出现英文状态词和英文小标题。

**Hard invariant:** 每个状态对读屏用户的可读名不能变少：去掉的可见文字必须由图标的可访问名和悬停提示补上；要用户去处理的状态始终保留文字。

## Problem

1. **左侧对话列表行尾写死英文。** 已核实：`frontend/packages/agentre-ui/src/session-index/attention.ts:163`、`:165` 对运行中、出错直接返回字面量 `"running"`、`"error"`，注释称「状态码不进 i18n」。中文界面的左侧列表因此显示小写英文。顶部标签页同一状态已经是转圈（`frontend/src/components/agentre/chat-tabs/tab.tsx:134`），两处不一致。
2. **中文界面的状态胶囊写英文大写。** 已核实：`transcript.json`（zh-CN）里 `canonical.status.*`、`canonical.agentSpawn.status.*` 全是 `RUNNING` / `DONE` / `ERROR` / `FAILED` 等；`chat.json`（桌面 zh-CN）的 `taskProgress.status.*` 同样；`transcript/agent-status.ts:24-42` 的 `statusConfig.label` 是 `RUNNING` / `WAITING` / `IDLE` / `ERROR`，Agent 会话弹层（`frontend/src/components/agentre/sessions-popover.tsx:104`）原样显示，连空闲都写 `IDLE`。`canonical.raw.allowed` 在中文里是 `Allowed`，`canonical.userAsk.waiting` 是 `WAITING · 等待回复`。
3. **设计规范把英文大写定成了约定。** 已核实：`docs/design.md` §3.5 写「labels are uppercase (`RUNNING`)」，§6.4 把 `StatusPill` 定义为「Dot + uppercase label pill」，原则 1 写运行状态「rendered as dots and pills」。
4. **成功态和终态的标记不一致。** 已核实：子 Agent 卡成功时不挂胶囊（`agent-spawn/card.tsx` 注释「spec 决策 10：没有标记 = 成功」），工具卡成功仍挂 `EXIT 0` / `DONE` 胶囊（`canonical-tool/raw/card.tsx:91-99`），`!` 终端命令卡成功挂「完成 · exit 0」（`local-command/card.tsx`）。终端命令卡的运行中用黄色（`status-waiting`），其它地方的运行中是绿色。
5. **同一信息说两遍，或只靠文字区分。** 已核实：任务进度条左边已有状态图标，右边又写一遍英文状态词；而失败、已取消与排队共用同一个空心圈，只靠右边的字区分（`task-progress/task-progress-bar.tsx:47-52`）。部门负责人徽标有皇冠又写 `LEAD`（`org/org-department-badges.tsx`）。
6. **状态点的读屏名是英文。** 已核实：`ui/status-dot.tsx:34` 的 `aria-label` 由 `statusConfig.label` 拼成 `running status`，中文界面读屏读英文。
7. **子 Agent 卡展开后的小标题是英文。** 已核实：zh-CN `canonical.agentSpawn.sections` 为 `TASK PROMPT` / `TASKS` / `STEPS` / `OUTPUT` / `SUMMARY`；同类的工具卡小标题已是「参数」「结果」。

## Actors and user stories

1. 作为桌面端用户，我扫一眼左侧列表和转录就能分清哪些在跑、哪些出错、哪些在等我，不用读英文词。
2. 作为控制台用户，我在共享的转录和手机版会话列表里看到同一套标记。
3. 作为读屏用户，图标替换文字后，我听到的状态名和原来一样，而且是界面语言。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 进行中统一用绿色转圈（loader-circle），不带字 | 用户已决定（「把 running 也改成图标」）。标签页已是转圈。否决：保留脉冲点 + 字——占宽，且中文界面里是英文 |
| 2 | 成功默认不挂标记；只有在一列结局里需要区分时（任务进度条、子 Agent 多次运行列表）才给 ✓ | 沿用已有约定「没有标记 = 成功」。否决：成功一律挂 ✓——转录里几十个对勾只是噪音 |
| 3 | 失败用红色三角；有退出码时后面保留 `exit N` | 退出码是实义信息。否决：只给图标——丢掉了退出码 |
| 4 | 已停止 / 已取消 → circle-slash；结果未知 → circle-help；跳过 → skip-forward；已过期 / 已失效 → hourglass；已批准 → check；已拒绝 → x；新建 / 修改 / 删除 → plus / pencil / trash | mockup 已获同意 |
| 5 | 要用户处理的状态保留文字，用中文：等你、审批、等你回复、待审批 | 用户已同意。只给图标不够明确。`WAITING · 等待回复` 去掉英文前缀 |
| 6 | 「未读」「后台」及「后台运行 · 任务 id」保留文字 | 它们说的是理由或另一种状态，任务 id 必须可见 |
| 7 | 新增共享组件 `StatusMark`：一个状态对应一个图标，自带可访问名和悬停提示；所有改动点都经它渲染 | 推荐，已在 mockup 说明，用户已同意。否决：各处各写图标——状态到图标的对应会分叉 |
| 8 | 更新 `docs/design.md`：去掉「标签写英文大写」，`StatusPill` 只用于要用户处理的状态并写中文，新增「状态 → 图标」对照表和纯图标的无障碍要求 | 用户已决定（「可以更新设计规范」） |
| 9 | 子 Agent 卡小标题译成 任务说明 / 各次运行 / 步骤 / 输出 / 总结，只改中文 | 用户已决定（「英文小标题也可以顺手做」）。`TASKS` 那一节列的是每次运行，故译「各次运行」 |
| 10 | 左侧列表行首状态点保留，行尾换图标 | 推荐：整列对齐不变，行布局零改动。否决：运行中把行首点换成转圈、行尾回到时间——转圈比点宽，所有行的标题会错位 |
| 11 | 记忆卡片（⑩）最后改 | 另一会话仍在运行期验证这张卡片的编辑与撤销，避免同时改同一文件 |
| 12 | 控制台通过钉住新的 agentre 版本获得共享包里的改动；控制台宿主自有的列表不改 | 已核实：控制台桌面版列表行尾只有时间与理由（`agentre-server/frontend/src/components/session/SessionIndex.tsx` 的 `trailing`），没有英文状态词 |

## 状态标记规则

**StatusMark。** 给定一个状态，渲染该状态的图标（对照表见决策 1–4），图标颜色取状态色（进行中、成功、批准、新建用 `status-running`，失败、拒绝、删除用 `status-error`，修改用 `primary-text`，其余终态用 `muted-foreground`）。它带可访问名（界面语言的状态名，如「运行中」「已停止」）和同文案的悬停提示。进行中的转圈在用户开启「减少动态效果」时静止。

**可访问名。** 被替换掉的可见文字原样成为图标的可访问名，所在行或卡头的可访问名因此不变（活动行、组头刻意不设 `aria-label`、由行内文本算出的约定保持不变）。状态点的读屏名改为界面语言，不再拼英文。

**语言包。** 不再被引用的英文状态词键删除；新的状态名在中、英两套语言包里都有。

## 各处改动

以下每条的前提都是该状态出现时，结果是用户看到的样子；未列出的状态保持现状。

**左侧对话列表（桌面）。** 会话在运行时，行尾显示转圈而不是 `running`；出错时显示红色三角而不是 `error`。等待审批、未读、后台的行尾文字不变；空闲行照旧显示相对时间。行首状态点不变。

**Agent 会话弹层（桌面）。** 运行中显示转圈，出错显示红色三角，等你显示文字「等你」，空闲不挂标记（行里已有最后活动时间）。

**活动行。** 一步还在运行时，行尾的「● 运行中」换成转圈。已终结却没配到结果的一步照旧写「失败」「已取消」「结果未知」，不转圈。后台运行标记不变。

**工具卡头。** 等待返回时显示转圈；成功时不挂标记（不再显示 `EXIT 0` / `DONE`）；失败时显示红色三角，有退出码时后面跟 `exit N`。已允许的徽标换成盾牌图标；本会话始终允许时，盾牌后跟灰字「本会话」。后台运行胶囊不变。

**子 Agent 卡。** 卡头运行中或等待中显示转圈 + 灰色耗时；出错、部分完成显示红色三角 + 耗时；已停止显示 circle-slash + 耗时；结果未知显示 circle-help + 耗时；成功照旧不挂胶囊。多次运行列表的每一行按同一对照表，完成的行显示 ✓。展开后的小标题按决策 9 显示中文。

**文件改动头。** 新文件的文件名前显示 file-plus（绿），删除的文件显示 file-x（红）且文件名划线；不再挂 `NEW` / `DELETED` 胶囊。普通修改不变。

**提问卡。** 已回答显示 ✓，已跳过显示 skip-forward，已失效显示 hourglass；等待回答显示文字胶囊「等待回复」。

**审批结果。** 工具审批卡、agrctl 审批卡、工具权限卡在已决之后：批准 / 允许显示 ✓（本会话允许时后跟灰字「本会话」），拒绝显示 ✕，过期显示 hourglass。agrctl 改动清单里每项的「新建 / 修改 / 删除」换成 plus / pencil / trash。待审批的卡片不变。

**`!` 终端命令卡。** 运行中显示绿色转圈；完成不挂标记；失败显示红色三角 + `exit N`；已停止显示 circle-slash。

**任务进度条（桌面）。** 删掉行尾的状态词。左侧图标：排队空心圈、运行中转圈、完成 ✓、失败红色三角、已取消 circle-slash 且该行描述划线。

**手机版会话行（控制台）。** 运行中的「运行中」胶囊换成转圈，出错换成红色三角；「等你」胶囊不变。

**记忆卡片。** 开头的动作词换成图标：记住了 → bookmark-plus，改了记忆 → pencil，归档了记忆 → archive，已有这条记忆 → bookmark-check，已撤销 → undo（摘要照旧划线）。所在层级：全局 → globe，部门 → users + 名字，项目 → folder + 名字。编辑、撤销换成带名字和悬停提示的图标按钮。撤销失败时的说明文字不变。

**记忆页条目行。** 「你写的」换成 user 图标，「来自会话」换成对话气泡图标 + 会话标题链接，「有详情」换成 text 图标。已置顶的星标和分组标题不变。

**权限模式菜单。** 当前模式的 `ACTIVE` 胶囊换成 ✓。

**部门负责人徽标。** 去掉 `LEAD` 字样，只留皇冠，可访问名与悬停提示为「负责人」。

**退出确认框。** 运行中的「运行中」胶囊换成转圈；「等你」不变。

## 设计规范

`docs/design.md` 同一次改动里更新：原则 1 改为「运行状态以状态点和状态图标呈现，只有要用户处理的状态才用带字胶囊」；§3.5 去掉「labels are uppercase (`RUNNING`)」，改为经 `StatusDot` / `StatusMark` / `StatusPill` 渲染且不出现英文状态词；§6.4 把 `StatusPill` 限定为要用户处理的状态并新增 `StatusMark` 条目；新增「状态 → 图标」对照表；写明纯图标状态必须有可访问名和悬停提示，且图标形状本身能区分状态、不只靠颜色。

## 交付

共享包和桌面端在 agentre 一次交付并推送；随后 agentre-server 钉住该版本，控制台的转录、手机版会话行随之更新，控制台宿主代码不改。记忆卡片与记忆页（共享包 `memory/`、`memory-write/`）排在最后一步，开工前确认另一会话的验证已收尾。

## Out of scope

- OpenClaw 执行审批卡的状态徽标：已决后显示的是具体决策（仅本次允许、本会话允许等），属于信息，保留文字。
- 设置页里的「本机」「桌面端」、TLS 模式、Hook 日志的「失败」、同步卡的计数徽标：身份、配置或计数，出现频率低。
- 活动块组头「正在执行 · 已 N 步」、思考块的秒数、`+N` / `−N`：本身是汇总或数字。
- 会话筛选的「全部 / 需要你 / 运行中 / 未读」：是筛选项名称。
- 英文界面的小标题和其它英文文案。
- 控制台宿主自有的会话列表和各页面。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `StatusMark` 组件 | 每个状态对应的图标、颜色角色、中英可访问名与悬停提示；减少动态效果下转圈不转 | 无（新组件） |
| 左侧列表行模型 `indexRowFromMeta` + `SessionRow` | 运行中、出错不再产出英文字面量，行尾渲染对应图标；审批 / 未读 / 后台 / 相对时间不变 | `session-index/attention.test.ts`、`session-row.test.tsx` |
| 各卡片与行组件渲染测试 | 每条「各处改动」：旧文字消失、图标及其可访问名出现、保留文字的状态仍是文字 | `activity-block/block.test.tsx`、`raw/card.test.tsx`、`agent-spawn/card.test.tsx`、`user-ask/card.test.tsx`、`file-edit/hunk-renderer.test.tsx`、`tool-permission/card.test.tsx`、`tool-approval/*.test.tsx`、`local-command/card.test.tsx`、`memory-write/card.test.tsx`、`memory/memory-row.test.tsx`、`permission-mode-pill.test.tsx` |
| 桌面宿主组件 | 任务进度条、Agent 会话弹层、退出确认框、负责人徽标的同上行为 | 无，新增 |
| `StatusDot` | 读屏名为界面语言 | 无，新增 |
| i18n 守卫 | 新键中英齐全、静态键存在 | `frontend/src/__tests__/i18n.test.ts`、`agentre-ui/src/i18n/i18n.test.tsx` |

运行期：用 `make verify-up` 起隔离桌面，对一个正在运行的会话截取左侧列表、标签页、转录（活动行、工具卡、子 Agent 卡）的浅 / 深两张图，确认图标与提示；控制台钉版本后在 dev 环境看一次转录与手机版会话行。视觉细节（尺寸、对齐）无法自动化，由这两次截图覆盖。

## Open questions
