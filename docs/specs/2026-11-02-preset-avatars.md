# 身份区合并与预置头像库

> Status: Draft
> Owner: desktop app 维护者（agentre-server 跟随抬共享包的钉，接入同一个身份区组件）
> Last updated: 2026-11-03（盘点后补：桌面端只画首字母的位置改为完整身份）

**Objective:** Agent、部门、项目在桌面端和 web 控制台都用同一个身份区：一枚可点的记号，加上名字和描述，不再有单独的名称、头像、颜色字段。点开记号是统一的弹层，除了现有的图标、首字母（以及桌面端 Agent 的上传图片），还能从一套内置的贴纸风格预设里直接挑一个。挑中后，两端所有显示这个身份的地方都显示这枚预设。

**Hard invariant:**
- 身份区以外的地方，没选预设的 Agent 和项目，记号的样子和改动前一样（上传图片、图标、首字母、颜色、尺寸都不变）。有意的例外只有两处：①部门没选图标时统一显示首字加主题色（决策 6）；②桌面端那些今天只画 Agent 首字母的位置，改为显示完整身份（决策 17），已选图标或上传图片的 Agent 在这些位置会跟着变。
- 各处保存机制不变：桌面端组织详情仍是输入停 600ms 自动保存、失焦和卸载时立即保存、整份快照校验通过才保存；其他位置沿用各自现有的提交时机。现有校验、保存状态提示、失败提示和重试照常工作。
- 不新增持久化字段、不加迁移、不改同步协议和 server 读写接口。
- 各处现有的默认值不变；用户没设置的字段，新建时照旧不提交。
- 旧版本客户端看到新的预设 key，不出现空白或破图（按旧代码各自的兜底显示，可能是首字母、拼图或色块）。

## Problem

1. **身份信息在同一个面板里出现两次。** 桌面端 Agent 详情的 header 已经有一个头像选择器和只读的名字（`frontend/src/components/agentre/org/org-detail-agent-chrome.tsx:44-58`），下面的「身份」栏又有名称输入框、描述、**第二个**头像选择器加上传按钮、头像色板（`org-detail-agent-identity.tsx:65-140`）。部门详情同样是 header 一份、「基本信息」里名称、描述、图标选择器、主题色再一份（`org-detail-department-identity.tsx:34-104`）。web 控制台 Agent 详情的「身份」栏是名称、描述、铺开的色板、铺开的图标（`agentre-server/frontend/src/pages/org/OrgAgentDetail.tsx:256-320`）。
2. **同一件事有五个选择器。** 桌面端 Agent 头像用 `AgentAvatarPicker`，部门用 `IconPicker`（都在 `frontend/src/components/agentre/icon-picker.tsx`），项目用共享包的 `ProjectGlyphPicker`，web 控制台用 `OrgIconPicker` 和 `OrgColorPicker`（`agentre-server/frontend/src/pages/org/orgPickers.tsx`）。交互形式各不相同：弹层、下拉字段、铺开的格子都有。
3. **Agent 和项目不手动挑，都长一个样。** 新建 Agent 默认颜色 `agent-1`、不带图标（`new-agent-dialog.tsx:61-62`），项目颜色为空时也退回 `agent-1`（`frontend/packages/agentre-ui/src/ui/agent-avatar.tsx:134`），于是列表里是一片蓝底加首字母。（桌面端新建部门默认是 hammer 图标加 `agent-2`，`new-department-dialog.tsx:52-53`；web 新建的部门不带图标。）
4. **能挑的只有线条图标和上传图片。** 图标词表只有约 30 枚 lucide 线条图标（`frontend/packages/agentre-ui/src/org/icon-registry.ts`）；上传只有桌面端 Agent 有，web 控制台没有上传通道（`orgPickers.tsx` 顶部注释）。
5. **记号有好几处不经过统一的身份方块，兜底也不一致。** 桌面端组织页的部门徽标、部门详情头部、Agent 详情里的所属部门，直接用 `iconForKey` 画 lucide 图标，没有图标就画拼图（`org-department-badges.tsx:27`、`org-detail-department-header.tsx:30`、`org-detail-agent-identity.tsx:172`、`icon-registry.ts:391-394`）；web 控制台 Agent 详情 header 自己解 key（`OrgAgentDetail.tsx:152-205`），web 部门详情 header 根本不画图标、只画首字（`OrgDepartmentDetail.tsx:110-115`），web 归属选择里的部门是纯色块（`org-placement-field.tsx:80-84`）。共享的 `AgentAvatar` 只接收画好的图标节点，有的调用点提前用 `hasIcon` 过滤，有的根本不传图标。只在统一方块里支持预设不够。

## Actors and user stories

1. As a `用户`，I want 在详情里直接改名字、描述和记号，不用在 header 和下面的表单之间来回找，so that 编辑身份是一处、一眼看全。
2. As a `用户`，I want 挑一个代表职业的 Agent 预设形象，so that 一眼看出这个 Agent 是干什么的，不用自己找图上传。
3. As a `用户`，I want 给 Agent 挑一个代表它所用模型或工具的预设（Claude、OpenAI、DeepSeek…），so that 管理多家模型的 Agent 时能快速区分。
4. As a `用户`，I want 给项目、部门各挑一个预设，so that 会话侧栏和组织页里一眼可辨。
5. As a `web 控制台用户`，I want web 上的身份区和弹层与桌面端相同，挑的预设两端一致，so that 两端不出现「一边有形象、另一边是首字母」。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 一轮交付：身份区合并和预设库放在同一个 spec | 用户决定。Rejected: 拆成两轮、先重构（用户选一次做完）；先在旧选择器上加预设（要在 5 个旧选择器各接一遍，重构时又删掉） |
| 2 | 共享身份区：左边一枚大记号（带编辑角标），右边名字输入框和描述；不再有单独的名称、头像、颜色字段和标签 | 用户要求「不要单独的名称、头像的输入框和选择」；现有项目设置的身份区已经是这个形式（`frontend/packages/agentre-ui/src/project/project-identity-fields.tsx`）。Rejected: 保留表单式字段只换选择器 |
| 3 | 统一的记号弹层，两端共用；web 控制台也改用弹层 | 用户选 web 方案 2。Rejected: web 沿用铺开式——mockup 里 Agent 的头像一项约占 220px 高（`config-options.html`，1400px 视口实测） |
| 4 | 颜色收进弹层。Agent 和项目：每一档都显示颜色；部门：只在「图标」「字母」档显示 | Agent 头像色还用在两端的 @提及 chip 和提及菜单（`chat-input/mentions/build-sources.ts:22-26`、`mention-node.ts:79-84`）和 web 概览页的 Agent 排行色点（`agentre-server/frontend/src/pages/Overview.tsx:766-782`）；项目颜色还用在对话 tab 的颜色条、溢出菜单色条、tab 提示（`chat-tabs/tab-strip.tsx:269-271`、`tab-overflow-menu.tsx:83-84`、`tab-tooltip.tsx:68-69`）和 @提及。部门主题色只用在部门记号上（已核对两端）。用户决定 Agent 与项目一样始终显示。Rejected: Agent 选预设时隐藏（会藏起仍有效果的设置）；置灰；部门保持可点（点了没反应） |
| 5 | 桌面端 Agent 的上传图片收进弹层的「图片」档；web 控制台没有这一档 | 合并后不再有单独的上传按钮；web 没有承载头像图片的接口 |
| 6 | 部门没选图标时，两端统一显示首字加主题色；部门弹层也有「字母」档 | 现在两端不一致（桌面拼图、web 详情首字、web 归属选择色块），共享组件只能有一种。用户选首字：和 Agent、项目一致，信息量比拼图大。这是有意的外观变化：桌面端没有图标的部门从拼图变成首字。Rejected: 统一成拼图 |
| 7 | 统一的贴纸画风：粉彩底、深色外轮廓、白色贴纸边、少内线 | 用户在 13 种生成风格里选定（证据见 Mockups）。Rejected: 程序化 SVG + 跟随主题色、动物、像素、黏土、职业动物角色（「像游戏」）、玻璃、Fluent、抽象标记 |
| 8 | Agent 职业预设 = 同一个 AI 小精灵的 16 种职业造型 | 「Agent 是角色、项目和部门是物件」，侧栏里两者分得开。Rejected: 职业物件贴纸——和项目物件同类，侧栏里分不清 |
| 9 | 部门预设 = 几个小精灵挤在一起举着团队标志 | 和 Agent 成套，表达「部门是一群 Agent」。Rejected: 徽章（题材不对、撑满被裁）；部门房间（24px 以下都是一样的小房子） |
| 10 | 项目预设 = 16 种工程类型物件，粉彩色调 | 用户选定和 Agent、部门同一色调。Rejected: 饱和色加粗描边 |
| 11 | Agent 另有一组「模型 / 工具」预设：官方 logo 加贴纸外框，logo 形状和颜色不改 | 用户要求认得出、且是「符合我们风格的 logo」。Rejected: 品牌色小精灵（认不出）；小精灵加 logo 角标或胸前 logo（用户否决）；让生成模型画 logo（画不准）。品牌风险由用户知情接受 |
| 12 | 预设自带配色，不跟随颜色字段 | 生成的形象是多彩位图，无法重新上色；颜色字段原样保留 |
| 13 | 预设存成带命名空间的 key，复用现有的图标字段 | Agent 的 `avatar_icon`、项目和部门的 `icon` 本来就是两端同步的词表 key。长度：桌面端只有 Agent `avatar_icon` 有 32 字节上限（`agent_entity/agent.go:164`），项目和部门的 `icon` 不限长；server 浏览器写接口三类都是 `max=64`（`internal/api/workspace/org.go`）。最长的预设 key 25 个字符，都在范围内。预设与图标因此天然互斥。Rejected: 新增预设字段——要加迁移、改同步载荷和 server 接口 |
| 14 | 身份区、弹层、预设素材和注册表都放在共享包 `@agentre-hub/agentre-ui` | 两端渲染同一份（跨宿主前端归属规则）。上传、保存、校验等宿主能力通过 props 或端口传入。Rejected: 各宿主各写一份 |
| 15 | 不改新建时的默认值，不自动分配预设 | 用户选择「预置头像库」而不是「自动分配默认身份」 |
| 16 | 新建对话框换成同一个身份区后，新增描述、记号、颜色的编辑 | web 新建部门目前只有名称、新建 Agent 只有名称和部门（`OrgCreateDialogs.tsx:29-41`、`Org.tsx:475-499`），桌面新建部门没有描述（`new-department-dialog.tsx:63-67`）。server 创建接口本身已有这些字段，不改接口。用户没设置的字段照旧不提交，不因为界面上显示默认蓝色就写进请求 |
| 17 | 桌面端今天只画 Agent 首字母加颜色的位置，全部改为按完整身份显示（上传图片 ▸ 预设 ▸ 图标 ▸ 首字母） | 这些位置从来不画图标和上传图片：会话索引的行和组头、对话 tab、转录消息头像、聊天 header、Agent 列表、退出确认、通知、组织索引行等（`index-group-row.tsx:185`、`chat-tabs/use-tabs-view.ts:111`、`use-transcript-row-view.tsx:100`、`chat-panel-header.tsx:117`、`agent-list.tsx:172`、`quit-confirm-dialog.tsx:105`、`notification-toast.tsx:99`、`packages/agentre-ui/src/org/org-agent-row.tsx:119`）；web 端对应位置已经画图标。用户决定统一成完整身份。Rejected: 只传图标不传上传图片（上传过图片的 Agent 会和别处不一致）；这些位置保持只画首字母（预设在会话里看不到） |

## 身份区

**适用位置**：桌面端的 Agent 详情、部门详情、新建 Agent、新建部门、项目设置、新建项目；web 控制台的 Agent 详情、部门详情、新建 Agent、新建部门、项目设置、新建项目。

**布局**：一行。左边是这个对象的记号（52px，右下角一枚编辑角标），右边从上到下是名字输入框（大号加粗，就是这个面板的标题）、该位置原有的附属信息、描述输入框。附属信息原样保留：桌面端 Agent 详情的「负责某部门」，web 控制台 Agent 详情的系统、负责人、所属部门、汇报对象徽标，以及保存状态提示。详情面板原有的删除、关闭按钮仍在这一行的右侧。

**移除**：这些位置原来单独的「名称」「描述」「头像」「上传图片」「头像颜色 / 主题色 / 颜色」字段及其标签全部去掉；header 里只读的名字和第二个头像选择器也去掉。其余字段（归属、执行目标、行为、结构、成员、项目路径等）位置和行为不变。

**编辑和保存**：身份区本身不决定什么时候保存，只把名字、描述、记号、颜色的变更交给宿主；各宿主沿用现有的保存机制：

| 位置 | 名字、描述 | 记号、颜色 |
|---|---|---|
| 桌面端 Agent / 部门详情 | 输入停 600ms 自动保存，失焦和卸载时立即保存；整份快照校验通过才保存，保存中再改会排队合并 | 点了立即保存当前整份快照 |
| 桌面端和 web 的项目设置 | 失焦时提交，值没变不提交；名字的错误显示在名字输入框下面 | 点了立即保存 |
| web 控制台 Agent / 部门详情 | 失焦时提交，值没变不提交 | 点了立即保存 |
| 两端的新建对话框 | 只改草稿，点「创建」时一起提交 | 只改草稿 |

- 名字为空、重名等校验沿用各处现有的规则和提示位置；失败提示、保存状态、重试沿用各宿主现有的做法。
- 新建对话框：现在没有的字段（web 新建 Agent / 部门的描述、记号、颜色，桌面新建部门的描述）随身份区一起加入；用户没设置的字段照旧不提交。web 新建对话框沿用「点创建先关窗、再异步写入」的现有流程。
- 默认值不变：桌面新建 Agent 是 `agent-1`、不带图标；桌面新建部门是 hammer 图标加 `agent-2`；新建项目颜色为 `agent-1`；web 新建 Agent / 部门默认不带图标和颜色。

**无障碍**：名字、描述输入框的可及名分别是「名称」「描述」（替代原来的可见标签）；记号按钮的可及名是「更改头像」（Agent）或「更改图标」（部门、项目）。

## 记号弹层

**打开**：点击身份区的记号，在它下方弹出。默认停在当前生效的那一档：存的是预设就停在「预设」，是图标就停在「图标」，否则停在「字母」；桌面端 Agent 有上传图片时停在「图片」。点击弹层外面或按 Esc 关闭。

**档位**：

| 对象 | 档位（从左到右） |
|---|---|
| Agent（桌面端） | 预设 · 图标 · 字母 · 图片 |
| Agent（web 控制台） | 预设 · 图标 · 字母 |
| 部门 | 预设 · 图标 · 字母 |
| 项目 | 预设 · 图标 · 字母 |

每个档位按钮下面一行小字显示当前状态：预设名、图标名、首字母，或「未选」「未设置」「已上传」「未上传」。

**预设档**：Agent 分「职业」「模型 / 工具」两组；部门、项目各一组；6 列网格。点击一个预设：写入它的 key，关闭弹层，记号立即变成这枚预设。底部有「取消预设」，清掉后回到「没有图标」的样子（Agent 显示首字母，项目、部门显示首字）。

**图标档**：沿用现有的搜索框、分类网格和「取消图标」。

**字母档**：沿用现有的首字母预览。

**图片档**（桌面端 Agent）：沿用现有的上传、替换、删除和「PNG / JPG / WEBP · 最大 2MB」约束；新建 Agent 时还不能上传，沿用现有的「创建完成后才能上传」提示。

**颜色**：放在档位按钮下面。Agent 和项目在每一档都显示（包括「图片」档），并标注颜色还用在哪里：Agent 是「@提及也用这个颜色」，项目是「对话标签、@提及也用这个颜色」。部门只在「图标」「字母」档显示。

**互斥**：选预设会替换掉已选的图标，选图标也会替换掉已选的预设（同一个字段）。上传图片仍然优先于预设和图标；已上传图片时，非「图片」档底部沿用现有提示：「当前生效：上传图片。」加上「预设 / 图标作为图片删除后的备用。」

**搜索**：图标档的搜索框可以按预设名（当前语言的中文或英文名、品牌名）搜到预设，搜到的预设显示在结果最前面；没有匹配时沿用「没有匹配项」。

**格子的交互和无障碍**：悬停时显示预设名提示；选中有明显的选中态；每个格子是单选项，可及名是当前语言的预设名，选中态通过 `aria-checked` 读出；可以用键盘聚焦和选中。

## 预设清单

每个预设有一个稳定的 key、一张素材和中英文名字。key 一旦发布就是持久化值，不能改名。下表的英文名就是 key 的后半段。

**Agent · 职业**（`preset:agent-<name>`）：assistant（CEO 助手）、architect（架构师）、reviewer（代码审查）、tester（测试）、debugger（调试）、frontend（前端）、backend（后端）、ops（运维）、security（安全）、writer（文档）、analyst（数据分析）、refactor（重构）、performance（性能优化）、planner（产品规划）、translator（翻译）、shell（终端）。

**Agent · 模型 / 工具**（`preset:brand-<name>`）：claude、anthropic、openai、codex、pi、openclaw、hermes、deepseek、glm、gemini、qwen、kimi、minimax、mistral、xai、meta。显示名是品牌名（Claude、Anthropic、OpenAI、Codex、Pi、OpenClaw、Hermes、DeepSeek、GLM、Gemini、Qwen、Kimi、MiniMax、Mistral、xAI、Meta），中英文相同。

**项目**（`preset:project-<name>`）：terminal（终端）、git（Git）、web（网页）、mobile（移动端）、server（服务器）、database（数据库）、api（API）、package（包）、containers（容器）、cloud（云）、chip（芯片）、ai（AI）、docs（文档）、dashboard（仪表盘）、game（游戏）、plugin（插件）。

**部门**（`preset:team-<name>`）：dev（研发）、frontend（前端）、backend（后端）、qa（测试）、design（设计）、ops（运维）、security（安全）、data（数据）、product（产品）、docs（文档）、marketing（市场）、support（客服）、research（研究）、platform（平台）、mobile（移动端）、leadership（管理）。

**素材规格**（交付要求）：正方形，粉彩底铺满整个方块，主体按自身边界裁紧后四周统一留白，不贴边、不被裁切；128px webp。体积预算：全部 64 张合计不超过 320KB（mockup 阶段的候选素材实测约 260KB）。

- 职业、项目、部门三组共 48 张，用 gpt-image-2.5-sunburst 生成后挑选、切图、清理边缘杂色。
- 模型 / 工具 16 张由仓库里现有的官方 logo 文件（`frontend/packages/agentre-ui/src/engine/assets/brands/`）加贴纸外框得到，logo 占方块约 75%。官方 logo 文件更新时，这组素材要跟着重新生成；生成方法要随素材一起记录在仓库里。

## 显示

**前提**：某个 Agent、部门或项目的图标字段存的是一个认得出的预设 key。

**结果**：所有显示它**身份记号**的地方，都把这一格整块画成预设图片，圆角和尺寸与该位置现有的记号相同，不叠颜色底。身份记号指现在会画图标、首字母或拼图的那一枚方块（包括今天只画首字母的那些位置，见决策 17），包括：会话索引的行和组头、对话 tab、转录消息头像、聊天 header、Agent 列表、退出确认、通知、会话侧栏的项目组头和会话行、会话详情和新会话里的项目与 Agent、项目设置和新建项目的预览、看板与任务里的项目、组织页的部门和 Agent 行、部门徽标、详情 header、Agent 详情里的所属部门、归属选择、聊天转录里的 Agent 头像、身份区的记号，以及 web 控制台的对应位置。完整的调用点清单在计划阶段逐一列出。

**不受影响的颜色装饰**：对话 tab 下面的颜色条、tab 溢出菜单的色条、tab 提示和聊天 header 里的项目文件夹图标、@提及的 chip 和菜单标记、web 概览页的 Agent 排行色点，继续按颜色字段显示，不画预设。

优先级：

| 对象 | 顺序 |
|---|---|
| Agent | 上传图片 ▸ 预设 ▸ 图标 ▸ 首字母 |
| 项目 | 预设 ▸ 图标 ▸ 项目名首字 |
| 部门 | 预设 ▸ 图标 ▸ 部门名首字（主题色底） |

预设图片是装饰，记号的可及名仍然是这个对象的名字，不念预设名。

**认不出的 key**：新版本里，以 `preset:` 开头、但注册表里没有的 key（例如以后新增的预设被较早的新版本读到），按「没有图标」显示：Agent 显示首字母，项目、部门显示首字。不显示空白或破图。已存的 key 原样保留，用户不重新挑就不改写。

**旧版本**：旧桌面端和钉版之前的 web 控制台不认识预设 key，按各自旧代码的兜底显示：有的位置是首字母，有的是拼图，web 旧选择器会把 key 原文标成「自定义」。都不会空白或破图，但不能保证统一成某一种样子。

## 兼容性

- 数据：只是往已有字段里写一个新形态的字符串，不改表结构；长度在两端的上限之内。
- 跨端：先在 `agentre` 发布带新身份区和预设的共享包，web 控制台钉到这个推送后的提交（`package.json` 里 `agentre-ui` 和 `agentre-wire` 钉同一个提交，并提交更新后的 `pnpm-lock.yaml`，因为构建用 `--frozen-lockfile`），再切换身份区、删掉自己的 `OrgIconPicker` / `OrgColorPicker`。
- 主题：亮色、暗色下都直接显示预设图片；粉彩底在两种主题下的效果已在 Mockups 截图里对照过，入库后在真实应用里再核对一次。

## Mockups

本地证据（不提交）：`.dev-kit/artifacts/2026-11-02-preset-avatars/mockups/`。
- `identity.html`：合并后的身份区和统一弹层（桌面 Agent、部门、web Agent、项目设置、新建 Agent），可交互，旁边放了合并前的桌面 Agent 详情做对照。
- `final-v2.html`：四套预设在选择器、组织页、会话侧栏里的效果，以及 14px、24px 整排对比。
- `config-options.html`：颜色处理和 web 布局的方案对比。`styles-gen.html`：13 种风格对比。`brand.html`：被否决的 logo 角标方案。

## Out of scope

- 不替换模型药丸、后端列表、子代理卡等处的官方 logo。
- 不根据 Agent 绑定的 harness 或模型自动套用形象，不改新建时的默认值。
- 「模型 / 工具」预设不开放给项目和部门。
- 不支持用户上传或自定义预设；web 控制台不加图片上传。
- 不删除、也不替换现有的 30 个线条图标。
- 身份区以外的字段（归属、执行目标、行为、结构、成员、项目路径等）不调整。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 共享包预设注册表（含守卫） | 每个 key 唯一、以 `preset:` 开头、不超过 32 字符，都能解析出真实存在的素材文件和中英文名；四组各 16 个；认不出的 `preset:` key 解析不到素材 | `packages/agentre-ui/src/org/icon-registry.test.tsx` |
| 共享包记号（`AgentAvatar`、项目字形、部门记号） | 预设 key 画整块图片；四档优先级；部门没有图标时画首字加主题色；认不出的预设 key 退回首字母 / 首字；可及名是对象名字 | `packages/agentre-ui/src/ui/agent-avatar.test.tsx` |
| 共享包身份区 | 只把变更交给宿主、不自己决定保存时机；新建模式只改草稿；附属信息插槽渲染；没有单独的名称 / 头像 / 颜色字段；输入框可及名 | `packages/agentre-ui/src/project/project-identity-fields.test.tsx` |
| 共享包记号弹层 | 各对象的档位集合；默认停在当前生效档；预设点击写 key 并关闭；取消预设；颜色行按对象和档位显示或隐藏（Agent、项目每档都有，部门在预设档隐藏）；图片档只在提供上传端口时出现；搜索能搜到预设；已上传时的提示 | `packages/agentre-ui/src/project/project-glyph-picker.test.tsx` |
| 桌面端宿主接线 | Agent / 部门详情、新建对话框、项目设置用新身份区；header 不再重复名字和头像；组织详情仍是 600ms 防抖加失焦保存、整份快照校验；上传经宿主端口；新建部门的默认值仍是 hammer 加 `agent-2`；部门徽标、详情 header、所属部门显示预设和首字兜底；会话行和组头、对话 tab、转录头像、聊天 header、Agent 列表、退出确认、通知按完整身份显示（上传图片、预设、图标、首字母四种情况各有断言） | 桌面端组织页现有测试、`org/use-auto-save` 的测试 |
| web 控制台宿主接线 | Agent / 部门详情、新建对话框用新身份区和弹层，写出同一个 key；详情失焦提交、值没变不提交；新建时用户没设置的图标和颜色不进请求；徽标和保存状态仍在；部门详情 header 显示预设或首字；`OrgIconPicker` / `OrgColorPicker` 已删除 | `agentre-server/frontend/src/__tests__/org-page.test.tsx` |
| 文案 | 新增文案中英文齐全 | `frontend/src/__tests__/i18n.test.ts` 与共享包的文案测试 |

不能自动化的部分：素材的视觉质量（边缘是否干净、14px 是否可辨、暗色主题是否协调）和身份区在真实窗口宽度下的排版。收尾时在隔离验证台上打开真实桌面应用，逐一走一遍 Agent 详情、部门详情、新建 Agent、新建部门、项目设置的身份区和弹层，亮色、暗色各截图对照 Mockups；web 控制台抬钉后在本地起一次，做同样的核对。

## Open questions

