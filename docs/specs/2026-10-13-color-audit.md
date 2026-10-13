# 配色对比度收口：文字角色、选中面、焦点环与控件的边

> Status: Approved
> Owner: desktop app maintainers
> Last updated: 2026-10-13

**Objective:** 浅色与深色两个主题下，界面上要读的文字都达到 4.5:1、要靠轮廓辨认的控件都达到 3:1（输入类字段的静止态边例外，见决策 14），同时按钮的边、hover 反馈与键盘焦点各自有一套全仓一致的写法。

**Hard invariant:** 不新增色相；现有 token 的色值除本文点名的 `--status-idle`（深色）外一律不动；未选中复选框的边保持 `control-border`（对所处表面不低于 3:1）；`tokens.test.ts` 现有的每一条守卫继续通过；深色下状态色与危险色文字的观感不变（它们的 `-text` 角色与填充色本来同值）。

## Problem

对比度均按 WCAG 相对亮度公式由 `tokens.css` 的色值算出，调用点由源码检索得到；对照图见 `.dev-kit/artifacts/2026-10-13-color-audit/mockups/`（本地证据，不入库）。

1. **状态色 / 危险色 / 品牌色被当文字用。** 仓里已有「当文字用」的角色（`status-*-text`、`destructive-text`、`primary-text`），但调用点大多没换：`text-status-running` 64 处对 `-text` 4 处，`text-status-waiting` 66 对 8，`text-destructive` 105 对 7，`text-status-error` 60 对 0（其中一部分是图标）。浅色下绿字对 card 4.04、黄字 3.89，手写状态胶囊 3.83 / 3.72，红字在 `bg-destructive/10` 上 4.14；共享的 `statusConfig.error` 胶囊在自己的底上 4.41。深色下 `bg-primary-soft text-primary` 4.32。（已验证）
2. **弱化文字落在 hover / 选中面上不达标。** `muted-foreground` 对 `accent` 浅色 4.38、深色 3.48；深色悬停行（`accent/60` 叠在 popover 上）3.92。落点包括斜杠命令、@ 提及、模型选择、权限模式、推理强度几个弹层与 `DropdownMenuShortcut`。`tokens.test.ts` 的 `TEXT_SURFACES` 表里没有 `--accent`。（已验证）
3. **outline 按钮的边两个主题不是一档。** 浅色落到分隔线色 `--border`（对 card 1.27，对话框页脚上 1.22），深色用 `control-border`（3.49）。它是用得最多的变体（131 处），对话框的「取消」有 20 处是它。（已验证）
4. **secondary 按钮在同值表面上消失。** 无边、只靠 `--secondary` 填充：对 card 1.10（浅）/ 1.13（深），深色弹层里 1.00。（已验证）
5. **下拉悬停时边变淡。** `hover:border-border-strong` 把边从 3.43 降到 1.48（深色 3.49 → 1.53）。（已验证）
6. **键盘焦点环太淡且不统一。** `ring-ring/50` 合成后约 2.1:1、`/40` 约 1.8:1；37 处 `/40`、36 处 `/50`、6 处实色。基础件另有实色 `border-ring` 兜底，自定义可点元素配了 `outline-none`，只剩这一圈。（已验证）
7. **身份色被当文字用。** 16 个 `--agent-N` 是为「色块 + 白字」调的，深色下当文字对 card 3.47–3.51；浅色落在 `secondary` 上 12 / 16 低于 4.5。落点：会话头部项目路径、组织架构负责人徽标；更新卡片两处把 `agent-1` 当强调色用。（已验证）
8. **终端 ANSI 颜色读不出。** 浅色主题 `brightYellow` 1.65、`brightGreen` 1.93、`brightBlue` 2.26、`white` 1.48；深色 `brightBlack` 2.37。两处 `new Terminal` 都没设 `minimumContrastRatio`。（已验证）
9. **深色空闲状态点对 `secondary` / `popover` 只有 2.77。** 浅色专门为这个落点调到了 3.04，守卫只查 running / waiting。（已验证）
10. **hover 用了静止表面色。** `hover:bg-muted` / `hover:bg-secondary` / `hover:bg-sidebar` / `hover:bg-card` 约 48 处；深色 `muted` ≡ `card`、`secondary` ≡ `popover`，浅色 `secondary` ≡ `sidebar`，落在同值面上反馈为零。逐处落点未核实。（已验证数量与同值关系；每处实际落点为 unknown，实施时逐处判断）
11. **Switch 关闭态滑块在浅色轨道上偏弱。** 滑块对轨道 1.55，没有投影。（已验证）
12. **设计文档过期。** `docs/design.md` §3.5 状态色表仍是旧值，§3.4 仍说 `input` 是字段边框，§3.3 的 accent 对比度只写了深色的数；`tokens.css` 两处注释引用旧值 `#8a8d94`。（已验证）

## Actors and user stories

1. As a `桌面端用户`, I want 状态、报错和说明文字在两个主题下都读得清, so that 不用凑近屏幕或切主题才能看清一条提示。
2. As a `只用键盘的用户`, I want 焦点落在哪个控件上一眼可见, so that 不会在 Tab 之间丢失位置。
3. As a `贡献者`, I want 文字用哪个角色、边用哪一档、hover 用哪个色各有一条写得出来的规则并有守卫, so that 新写的界面不会把同一类问题再攒回来。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 状态色 / 危险色 / 品牌色**当文字**时走已有的 `-text` 角色；图标、圆点、进度条保留填充色 | `docs/design.md` §3.5 / §3.11 已是这条规则，只是没执行；不新增颜色。Rejected: 连图标一起换成 `-text`——规则更好查，但浅色下图标会整体变暗，与文档「fills, dots and icon marks 用填充色」相反 |
| 2 | hover / 选中面（`accent`）上的弱化文字用一个专用 token，取值与现有 `--sidebar-selected-muted` 相同（`#5f5f67` / `#abaeb4`） | 深色下 `muted-foreground` 在任何「看得出的选中面」上都够不到 4.5，`tokens.css` 对 sidebar 选中面已有同样的论证。Rejected: 整体调亮深色 `muted-foreground`——全应用的弱化文字都会变亮，层级变平 |
| 3 | 边分档：分隔线 `border`、可点控件的边 `input`、控件本体 `control-border`（字段的边后来另立一档，见 14） | 用户在 mockup 第 9 节确认。按钮里有文字，边只是辅助，不需要 3:1。Rejected: outline 按钮直接用 `control-border`——用户看过后认为太深 |
| 4 | outline 按钮的边两个主题都用中间档 `input`（浅色 1.27 → 1.62；深色 3.49 → 1.99） | 用户确认，含深色降一档以保持两主题同一规则。Rejected: 深色保持 `control-border`——两个主题规则不一致 |
| 5 | secondary 按钮不加边，填充改为相对表面取色（前景色 8%，悬停 12%） | 相对取色在任何表面上都不会与底同值（浅色 1.18，深色弹层 1.25）。Rejected: 补一圈细边——与 outline 变体长得一样，用户认为偏重 |
| 6 | 下拉悬停不动边，改为与 outline 按钮相同的底色反馈 | 悬停让边变淡是旧边框色的遗留。Rejected: 悬停时把边加深——用户认为偏重 |
| 7 | 键盘焦点统一为 2px 实色 + 2px 间隙（品牌色，对 background 5.58 / 5.03） | 用户看过 mockup 第 3 节后随整体方案确认。Rejected: 保留半透明光晕只调高透明度——仍然随底色变化，达不到稳定的 3:1 |
| 8 | 身份色只用在图形上（图标、色块），文字回到中性色；更新卡片改用品牌色 | 不新增 token，与 `docs/design.md` §3.6b「不要把身份色拿来当文字」一致。Rejected: 每个身份色补一个 `-text` 角色——32 个新 token 只服务三四个调用点 |
| 9 | 终端加 `minimumContrastRatio: 4.5`，主题色表不动 | 由 xterm 按底色自动调整前景，一处选项覆盖 16 色两主题。Rejected: 手工重调亮色主题的 16 个 ANSI 值——要维护两套色表，且仍可能漏 |
| 10 | 深色 `--status-idle` 由 `#6a6d74` 改为 `#747880`；浅色不动 | 对 `secondary` / `popover` 2.77 → 3.24，仍暗于 `muted-foreground`。本文唯一一处改动既有色值 |
| 11 | hover 一律用 `accent`（外壳带上用 `rail-accent`），不用静止表面色 | `accent` 的定义就是「不等于任何静止表面的反馈色」，已有守卫 |
| 12 | Switch 轨道不动，浅色滑块改纯白并加一点投影 | 用户确认轨道用 `control-border` 太深。Rejected: 加深轨道 |
| 13 | 身份色板重排（mockup 第 4 节）与文件图标黄色（第 7 节）不做 | 收益小；色板重排在深色下会让两个色发灰 |
| 14 | 输入框 / 文本域 / 下拉 / 搜索框的静止态边取新增的 `field-border`（浅色 `#ababb1`，对 card 2.28；深色 `#5f626a`，2.64），正好在 `input` 与 `control-border` 中间；未选中的复选框与 outline `Toggle` 留在 `control-border` | 用户在真机截图上看到只有按钮的边变了、字段的边仍偏重，随后在真实界面的四档对照图里选定中间一档（2026-10-13）。字段不再守 3:1：它有标签、占位与固定的位置可辨认，聚焦时边变成实色 `ring`；复选框只有 16px 且没有别的线索，所以不动。Rejected: 与按钮同档——输入框和按钮的边不再有区别，浅色下空输入框在白卡片上只有 1.62；只淡四分之一（2.80 / 3.06）——变化小到看不出 |

## 文字角色

- 当一段文字或文字胶囊用状态色、危险色或品牌色着色时，它的颜色取对应的 `-text` 角色：运行 / 成功用 `status-running-text`，等待 / 警告用 `status-waiting-text`，错误 / 危险用 `destructive-text`，品牌色用 `primary-text`。结果：浅色下这些文字对 card、`secondary` 及各自的软底都不低于 4.5；深色下外观不变（品牌色那一类在 `primary-soft` 上由 4.32 升到 7.10）。
- 图标、圆点、进度条、边框继续用填充色（`status-*` / `destructive` / `primary`）。
- 共享的错误状态胶囊（`statusConfig.error`）的文字同样走 `destructive-text`。
- 危险色文字直接写在 card / background 上本来达标（4.83 / 4.63），也一并换到 `destructive-text`，以免同一屏上出现两种红字。

## 选中面与悬停面上的弱化文字

- 当一行处于选中或悬停（底色为 `accent`，含半透明的 `accent/NN`）时，行内的弱化文字（说明、快捷键、右侧路径）用专用 token，对该行实际底色不低于 4.5，两个主题都成立。
- 同一行回到静止态时，弱化文字回到 `muted-foreground`，与其它静止行一致。
- 该 token 与它的落点进入 `tokens.test.ts` 的文字表面守卫。

## 控件的边与填充

- **outline 按钮**：边取中间档 `input`，两个主题一致；填充与悬停反馈不变。对话框页脚的「取消」随之生效。
- **secondary 按钮**：填充相对脚下表面取色，在 card、background、`secondary`、popover 上都与底可分（不低于 1.15）；悬停时再深一档；文字色不变。
- **下拉触发器**：悬停时边保持静止态的取值，底色出现与 outline 按钮相同的悬停反馈；打开态与聚焦态不变。
- **Switch**：浅色下滑块为纯白并带投影；轨道色、开启态与深色外观不变。
- **输入框、文本域、下拉、搜索框**：静止态的边取 `field-border`，四者同一个取值；它比 outline 按钮的边实、比复选框的边轻，对字段会落在的每种表面（card、background、`secondary`、popover）不低于 2:1。全仓手写的字段随之用同一档：原先和基础件同档的四处（看板的范围与筛选触发器、模型目标选择器、Hooks 页的搜索框）跟着调淡；原先停在分隔线 `border` 那档的四处（技能选择器的搜索框、组织页的归属下拉、图标选择器、Hooks 脚本页的文本域）由 1.27 / 1.19 提到这一档——这四处是**变实**，由规格复查按「同一个取值」补上，待用户看过真机截图确认。不在此列、保持现状的：对话输入框的外框、标题栏的命令面板入口、组织页的筛选入口、任务表单与对话输入框里的胶囊式选择器。
- **复选框、outline `Toggle`**：未选中 / 未按下时的边保持 `control-border`。

## 键盘焦点

- 任何可聚焦的按钮类控件（共享包的 `Button` 及全仓手写的可点元素）在键盘聚焦时显示 2px 实色环，与控件之间留 2px 间隙，颜色为 `ring`；鼠标点击不显示。
- 例外：贴着裁剪容器的按钮类元素——填满标签条的对话标签页与文件预览标签页、贴着列表裁剪边的会话行——同一条 2px 实色环改为**向内**画，不留间隙。向外的环在这些位置会被容器切掉（真机验证：标签页只剩右边一条线，会话行缺右边）。向内的环压在元素自己的填充上，对每种行底色不低于 3:1。（用户在验证报告之后决定，2026-10-13）
- 输入类控件（输入框、文本域、下拉、搜索框）保持现状：聚焦时边变为实色 `ring` 并带光晕。手拼的搜索框（Hooks 侧栏、技能选择器）由外框接手同一套画法——它们此前键盘聚焦时没有任何画面，而字段的边低于 3:1 之后，聚焦换成实色环是这条边成立的前提。
- 全仓不再出现 `ring-ring/40`、`ring-ring/50` 两种写法并存的按钮类焦点样式。

## 身份色

- 会话头部的项目路径、组织架构的负责人徽标：图标保留项目 / 成员的身份色，文字用中性色。
- 更新卡片的标题、边框、软底与进度条改用品牌色（`primary-text` / `primary-soft` / `primary`）。
- 头像色块（`AgentAvatar`）与身份色板的 16 个色值不变。

## 终端

- 交互终端与只读命令输出两处 xterm 都以 4.5 的最小对比度渲染：任一 ANSI 前景色对终端底色不足 4.5 时由 xterm 自动调整；已达标的颜色不变。
- 终端主题色表、选区颜色与字体不变。

## 状态点与 hover

- 深色下空闲状态点对 `card`、`secondary`、`popover` 都不低于 3:1；该 token 进入状态填充色的图形守卫。
- 任何行或按钮的悬停反馈都用 `accent`（窗口外壳带上用 `rail-accent`）。逐处核对现有约 48 处 `hover:bg-muted` / `hover:bg-secondary` / `hover:bg-sidebar` / `hover:bg-card`：落在同值表面上的改为 `accent`；本来就落在不同值表面上、反馈可见且属于有意设计的（例如 sidebar 行悬停提亮）保留并在实施记录里列明。

## 文档

- `docs/design.md` 同步本文改动的每一条规则与数值（四档边、文字角色的执行范围、选中面弱化字 token、焦点配方、`--status-idle`），并修正 Problem 12 列出的过期内容。
- `tokens.css` 中引用旧值的注释随之更正。

## 兼容性

- `agentre-server` 的前端经固定提交引用 `@agentre-hub/agentre-ui` 并导入同一份 `tokens.css` / `base.css`。共享包内的改动（token、`Button` / `Select` / `Switch`、`statusConfig`、包内的弹层与终端）在它升级固定提交后自动生效；它自己源码里的同类写法不在本文范围内（见 Out of scope）。
- 共享包不新增对宿主的依赖；新增 token 同时声明浅色与深色值并有 `--color-*` 映射。

## Out of scope

- `agentre-server` 前端自己的调用点（`text-status-*` 16 处、`text-destructive` 17 处、`ring-ring/NN` 8 处等）与升级固定提交：桌面仓推送之后在 `agentre-server` 另起一轮。
- 身份色板重排、文件类型图标的黄色（mockup 第 4、7 节）。
- 状态圆点增加形状区分（色觉障碍下 waiting 与 error 接近）。
- 深色实心红底靠各处写 `dark:bg-destructive/60` 的现状（已有 `destructive-fill.test.ts` 守卫）。
- 清理零引用的 token（`chart-*`、`traffic-*`、若干 `sidebar-*`）。
- 语法高亮注释色（浅色对 `secondary` 4.14）与滚动条滑块对比度。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `packages/agentre-ui/src/tokens.test.ts` | 选中面弱化字 token 对 `accent` 与悬停合成面 ≥4.5；`--status-idle` 对 `card` / `secondary` / `popover` ≥3；新增 token 两主题齐全且有映射 | 同文件的 `TEXT_SURFACES` 与状态填充色守卫 |
| 设计 token 的 lint 规则（`frontend/eslint-rules/design-tokens.js`，由 `make lint` 执行）及其守卫测试 `src/__tests__/eslint-design-tokens.test.ts` | 同一类名串里「软底 + 填充色文字」的组合被拦下（如 `bg-status-running-bg` 配 `text-status-running`、`bg-destructive/NN` 或 `bg-destructive-soft` 配 `text-destructive`、`bg-primary-soft` 配 `text-primary`）；`text-agent-N` 只允许出现在身份色类名表里；`ring-ring/NN` 被拦下；`hover:bg-muted` / `hover:bg-secondary` 被拦下，保留项逐处带理由豁免。守卫测试钉住规则拦得住、放得过，且生效配置用的是同一份来源 | 同文件现有的颜色 / 字号两组规则；`docs/testing.md` 要求源码约定优先用 lint 规则而不是文本扫描测试 |
| 组件测试：`Button` / `Select` / `Switch` / `statusConfig` / 字段一族 | outline 的边类名两主题同一档、secondary 的填充、下拉悬停不改边、错误胶囊用 `destructive-text`；`Input` / `Textarea` / `SelectTrigger` / `SearchInput` 的静止态边是同一个 token，落在 outline 按钮与复选框两档之间且不低于 2:1 | `ui/checkbox.test.tsx`、`ui/switch.test.tsx` |
| 终端构造参数 | 两处 `Terminal` 都带 `minimumContrastRatio: 4.5` | 无，新增 |

不能自动化的部分：没有软底、直接写在卡片上的状态色文字，源码扫描分不清它是文字还是图标，靠实施时逐处判断加收尾时的源码复查；两个主题下的实际观感（焦点环、secondary 填充、终端颜色、Switch 滑块）靠收尾时在隔离验证台里各看一遍并留截图。

## Open questions

（无）
