# 会话索引：删掉两根左竖条，让底色与记号自己说清楚

<!-- File: docs/specs/2026-08-19-sidebar-selection-cues.md -->

> Status: **Approved**（2026-08-19，用户裁决「可以」）
> Owner: 桌面端（`agentre`，前端；会话行在共享包 `@agentre-ai/agentre-ui` 里）
> Last updated: 2026-08-19（决策 8 于同日补入：计数数字改用状态的文字角色色）
> Mockup（本地产物，`.dev-kit/` 在 Git 之外）：`.dev-kit/artifacts/2026-08-19-sidebar-selection-cues/mockups/`
> —— `?view=now` 现状（含悬停态）、`?view=options` 三方案对比、`?view=matrix` 同一行四态、
> `?view=header` 组头四种画法、`?view=bubble` 琥珀括号去留、`?view=final` 定稿方向。

**Objective:** 会话索引里「哪一条是我开着的」「哪个项目在跑」不再靠左侧 3px 竖条表达，
而由行/组头自身的底色与记号承担，且这些记号在鼠标悬停时不会消失。

**Hard invariant:**

1. **行与组头的盒模型不变。** 会话行仍是 `px-2 py-1.5 rounded-md`，项目组头仍是
   `px-2 py-1.5`（子项目 `px-1.5 py-1`）；不新增 DOM 层级、不改行高、不产生任何布局跳动。
   删条与加条一样，都必须落在既有内边距里。
2. **不引入 ring 表达选中。** `ring` 在本项目专属 `focus-visible`；选中态若借用 ring，
   键盘焦点与鼠标选中会长得一样。
3. **不新增可见文案。** 组头记号只用颜色 + 数字，不写「在跑 / 等你」这类词（用户裁决，决策 4）；
   本轮 `frontend/src/i18n/locales/{zh-CN,en}/` 不新增 key。
4. **不改 attention 的判定。** `computeAttention`（`stores/attention-store.ts:31-41`）与
   `reasonToDisplayStatus`（`lib/attention-display.ts:9-17`）一行不动；本轮只改**怎么画**，
   不改**算什么**。
5. **选中态不得只靠色相区分。** 见决策 3：底色必须与 hover 反向偏离静止面（一深一浅），
   并保留 `aria-current="true"` 与标题字重。

## Problem

1. **选中底色比 hover 还弱，所以竖条是补丁而不是装饰。**（已验证）
   亮色下 `--primary-soft #eef4fa` 对 `--sidebar #f4f4f5` 只有 **1.01:1**，
   而 `hover:bg-sidebar-active-bg`（`#ffffff`）是 **1.10:1**；暗色下选中 **1.23:1**、
   hover **1.28:1**——两个主题里都是「鼠标停着」比「我选中的」更显眼。
   `session-row.tsx:144-146` 的注释把这件事写明了，那根 `before:w-[3px]` 竖条
   （`session-row.tsx:148`）正是为补这个洞而加。**因此顺序是先修底色、再删条**：
   直接删条会让选中在两个主题里都近乎不可见。

2. **悬停选中行时底色被完全顶掉，竖条成了唯一信号。**（已验证）
   在编译产物 `frontend/dist/assets/index-DSzbcBbd.css` 里 `.bg-primary-soft` 出现在字节偏移
   47673、`.hover\:bg-sidebar-active-bg:hover` 在 85866——排在后面且多一个伪类，级联上必胜。
   于是鼠标停在选中行（或运行中的项目组头）上时，那一行的选中/运行底色整块消失。
   这既是 bug，也是竖条至今删不掉的直接原因。

3. **项目组头上三个记号在说同一件事，且互相矛盾。**（已验证）
   - 3px 绿条只在 `hasRunning`（子树里有 `running` / `bg_running`，`index-page.tsx:500-506`）时出现
     （`project-group-header.tsx:126-130`）；
   - 同一条件下还叠一层 `bg-primary-soft`（`project-group-header.tsx:121`）——**品牌蓝**说的是
     「在跑」，与绿条不是一个颜色语义；
   - 同一行的计数写死 `text-status-running` + 绿点（`project-group-header.tsx:182,189`），
     但它统计的是**全部 attention 条数**，不分 reason。于是 3 条未读的项目显示绿色「3」，
     而那三行自己画的是琥珀点（`attention-display.ts:13` 把 `unread` / `needs_attention`
     映射成 `waiting`）——组头与它自己的行对不上。

4. **用户读到的是「选中的蓝条」和「选中项目的绿条」。**（用户陈述）
   绿条实际表达的是「子树里有东西在跑」，与选中无关。两根条读起来像同一套「选中」语言的两个颜色，
   这本身就是这套表达失败的证据。

## Actors and user stories

1. 作为**在长列表里找回当前会话的用户**，我希望一眼看出哪一行是我开着的，
   这样鼠标扫过列表时不会把 hover 误当成选中。
2. 作为**鼠标停在当前会话上的用户**，我希望它仍然看得出是选中的，
   而不是悬停一下就退回成普通行。
3. 作为**扫一眼侧栏判断轻重缓急的用户**，我希望项目组头上的那个记号的颜色说的是真话——
   绿色就是真的有东西在跑，而不是「有 3 条未读」。
4. 作为**依赖读屏或存在色觉差异的用户**，我希望「选中」不只靠一种颜色成立。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 选中态改为**加深的专用底色**（方案 A「实面」），删掉 3px 主色竖条 | 底色与 hover **反向**偏离静止面（选中压深、hover 提亮），方向差比比例差更好读；`?view=matrix` 逐格验证过。Rejected：**B 浮卡**（选中白底+投影、hover 改为下沉灰）——要改整条侧栏的 hover，组头/子组头/随手对话全跟着变，波及面远超本轮；**C 实心主色行**——最不可能误读，但份量重到会先于内容被看到，且行首状态点落在饱和蓝上只有 2.3:1，得再给状态点加描边 |
| 2 | 新增一对 `--sidebar-selected-bg` token，而不是继续复用 `--primary-soft` | `--primary-soft` 是**品牌信息面**（rail 激活按钮、info toast 都用它），它被调成「在白卡上很轻」，落到 sidebar 上就只剩 1.01。选中需要自己的取值。Rejected：直接把 `--primary-soft` 调深——会同时改掉 rail 按钮与 toast 的观感，属于本轮之外的连坐 |
| 3 | 亮色取 `#d7e5f3`（对 sidebar **1.17**、对 hover 白 **1.28**），暗色取 `#27405c`（**1.75** / **1.37**） | 亮色**被 `--muted-foreground` 卡住**：时间轴的两行行第二行是 `text-muted-foreground`，`#65656d` 在 `#d7e5f3` 上是 4.51，再深一档（`#cfe0f0`）就掉到 4.28、跌破 4.5。`--primary-text` 在这块面上 4.54 同样贴着线。Rejected：`#cfe0f0`（1.23，更醒目）——需要连带新增一个更深的选中文字 token，把一处改动摊成三处 |
| 4 | 组头的三个记号收成**一个**：删绿条、删 `bg-primary-soft`，计数改为**按最强 reason 着色的裸点+数字**（甲-2），**不加文案** | 用户裁决：「不需要『在跑』这个文案，有颜色表达就可以了」。裸点+数字与今天形状完全一致，只换颜色来源。Rejected：**甲-1 软底 pill**——亮色下 `--status-running-bg #ecfdf5` 对 sidebar 只有 1.04，色块几乎看不出，白多一个形状（`?view=final` 左/中并排可见）；**乙 绿软底整行**、**丙 头像挂运行点**——均被用户否掉 |
| 5 | 组头记号取色规则：把组内每条 attention 会话过 `reasonToDisplayStatus`，按 **error(红) > waiting(琥珀) > running(绿)** 取最强 | 与行自己的点同源同色，组头与行不再对不上。优先级按「谁更需要你动手」排：出错要看、等你回最挡路、在跑只是通报。Rejected：沿用 `computeAttention` 的会话内优先级（`needs_attention > running > error > ...`）——那是单条会话选 reason 的顺序，把 error 排在 running 之后，用作组级取色会让红被绿盖住 |
| 6 | attention 气泡的琥珀 `border-l-2` **保留** | 用户裁决。它框的是「需要你看的这几条」这一**段**，是分组括号而非某一行的选中记号，与前两根条不同义（`?view=bubble` 两种都画了） |
| 7 | 既有的「选中不能只靠颜色」守卫**改写而非删除** | 该断言今天钉的是 `before:w-[3px]` 的存在（`session-row.test.tsx:97-115`），注释里明写「改色可以，去掉不行」。竖条撤走后，非颜色线索由**亮度方向**（选中压深 / hover 提亮，灰度下也分得开）、**标题字重 `font-medium`**、**`aria-current="true"`** 三者共同承担，守卫改钉这三样。Rejected：直接删掉这条测试——那等于把 WCAG 1.4.1 的约束一并删掉 |
| 8 | 数字用 `statusConfig[tone].textClassName`（状态的**文字**角色），不沿用今天的 `text-status-running`（**填充**角色） | 已验证：`--status-running` `#10b981` 落在 `--sidebar` `#f4f4f5` 上只有 **2.31:1**，tokens.css 自己写明饱和填充色在亮色下当文字不可读；换成 `--status-running-text` 是 **4.99**、`--status-waiting-text` **4.57**。这不是新增要求，而是照抄旧写法会把既有缺陷搬进新代码。已知例外：`--status-error` `#dc2626` 在侧栏上 **4.39**，差一点到 4.5——这是既有 token 缺口（今天每一行行尾的 `error` 标签同值），本轮沿用同一套投影、不新增偏差，补 token 另开一轮 |

## 会话行的选中态

**前提**：一条会话行渲染在侧栏（`--sidebar`）上，`selected` 为真。
**动作**：无论鼠标是否悬停其上。
**结果**：该行底色为 `--sidebar-selected-bg`；标题为 `--primary-text` 且 `font-medium`；
行尾短标签同为 `--primary-text`；`aria-current="true"`。行左侧**没有**任何竖条，
行的内边距、圆角、字号与未选中行完全一致。

**前提**：一条 `selected` 的行被鼠标悬停。
**动作**：`:hover` 生效。
**结果**：底色**保持** `--sidebar-selected-bg` 不变。悬停反馈由光标形态承担——
已经落在的那一行不需要再用底色告诉你「可以点」。这一条同时修掉 Problem 2。

**前提**：未选中的行被鼠标悬停。
**动作**：`:hover` 生效。
**结果**：与今天一致（`--sidebar-active-bg`）。本轮**不动** hover。

**跨宿主的影响**：`SessionRow` 在共享包里，`agentre-server` 的会话索引用的是同一个组件
（`agentre-server/frontend/src/components/session/SessionIndex.tsx:818`，同样把行放在 `bg-sidebar` 上），
它今天有**同一个**缺陷。但该仓库把共享包 pin 在一个 commit SHA 上
（`agentre-server/frontend/package.json:16`），因此不会自动跟随；本轮只在 `agentre` 落地，
`agentre-server` 何时 bump 由那个仓库自己决定。新底色在 `--card` / `--background` 上分别是
1.28 / 1.24，比在 sidebar 上更明显，换表面不会失效。

## 项目组头的运行与关注记号

**前提**：一个项目组头，其子树里有需要关注的会话。
**动作**：渲染组头。
**结果**：名称右侧一枚 `size-1.5` 圆点 + `font-mono text-2xs` 的条数。
档位由决策 5 的规则得出，两者取该档在 `statusConfig` 里的**两个不同角色**：
点用饱和填充色（`dotClassName`），数字用该状态的文字色（`textClassName`）——
与会话行的「行首状态点 + 行尾短标签」逐字同一套投影。组头**没有**左侧竖条，
**没有**因「子树在跑」而改变的整行底色——组头的底色只剩 hover 一种状态。

**前提**：子树里没有需要关注的会话。
**动作**：渲染组头。
**结果**：不渲染这枚记号（与今天 `attentionCount > 0` 的门槛一致）。

**前提**：折叠的父项目，其后代项目里有需要关注的会话。
**动作**：渲染组头。
**结果**：计数与取色都**含后代**（与今天一致，`index-group-row.tsx` 的 `subtree` 口径不变）。
折起来仍然看得见下面有几条在等你、是什么颜色。

**「随手对话」组头**（`free-group-header.tsx:65-76`）今天同样写死绿色计数，
按同一规则一并改——否则同一枚记号在两种组头上一个说真话一个不说。

## 保留不动

- attention 气泡的 `border-l-2 border-status-waiting/40`（决策 6）。
- 每一行行首的状态点、行尾的状态短标签、行首 14px 的另一维字形。
- hover 的取值与作用范围（`--sidebar-active-bg`）。
- `--primary-soft` / `--primary-text` 本身的取值，以及它们在 rail 按钮 / toast / 命令面板上的用法。

## Out of scope

- **`agentre-server` 的同步**：pin 在 commit SHA，由该仓库自行 bump（见上）。本轮不改那个仓库。
- **`--sidebar-active-bg` 的命名**：它实际是 hover 填充而非「激活」填充，与新增的
  `--sidebar-selected-bg` 并排时容易读混。改名是全仓 sweep，属于独立一轮。
- **状态点在深色填充上的对比度**：随方案 C 一起被否掉，本轮不涉及。
- **`notification-toast.tsx:94,177` 的 3px 侧条**：那是 toast 的类型色带，不在会话索引里。
- **`chat-tabs/tab.tsx`、`file-preview/preview-tab-strip.tsx` 的边框**：标签页不是列表选中，本轮不动。
- **`computeAttention` 里 error 排在 running 之后**：决策 5 只改组级取色，不动单条会话的 reason 判定。
- **`--status-error` 缺一个「作为文字」的角色色**（在 `--sidebar` 上 4.39，见决策 8）：
  补它要动 token 表并回归全部用到 `text-status-error` 的地方，属于独立一轮。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `packages/agentre-ui/src/session-index/session-row.test.tsx` | 选中行携带 `aria-current="true"`、标题字重、以及**与 hover 不同的**选中底色类；不再断言 `before:w-[3px]` | 改写现有的 `:97-115`（决策 7），BDD 句式沿用同文件 |
| `packages/agentre-ui/src/tokens.test.ts` | `--sidebar-selected-bg` 在明暗两主题下对 `--sidebar` 都 ≥ `FEEDBACK_MIN`，且**严格大于** `--sidebar-active-bg` 对 `--sidebar` 的比值 | 加进既有的 `FEEDBACK_SURFACES` 表（`:124-127`）+ 一条新断言；后半句是这一轮新增的口径，今天没有测试钉过「选中要比 hover 强」 |
| `packages/agentre-ui/src/tokens.test.ts` | `--primary-text` 与 `--muted-foreground` 落在 `--sidebar-selected-bg` 上都 ≥ 4.5 | 加进既有的 `TEXT_SURFACES` 表（`:172`）——决策 3 的取值正是被这一条卡住的，必须由测试守住 |
| `src/components/agentre/__tests__/project-group-header.test.tsx` | 组头不再渲染 `project-running-indicator`；给定混合 reason 的子树，记号取到约定的那一档颜色 | 改写现有的 `:202-210`；同文件已有 `attentionCount` 的渲染断言可复用夹具 |
| `src/components/agentre/__tests__/index-group-row.test.tsx` | 折叠父项目时记号的条数与取色含后代 | 同文件已有 `hasRunning` / subtree 夹具（`:82`） |

`hasRunning` 这个 prop 在删掉整行底色与竖条后是否还有消费者，由实现阶段判定：
若无人再用，它连同 `index-page.tsx:500-506` 的计算一并撤掉；这属于同一处的收口，不是顺手重构。

**不能自动化的部分**：新底色在真实窗口里「够不够看得出」是目视判断。
mockup 的 `?view=final` 与 `?view=matrix` 是决策依据；合入前按 `docs/verification.md` 跑一次真实应用，
在明暗两主题下各看一眼选中行、悬停选中行、混合 reason 的项目组头。

## Open questions

（无）
