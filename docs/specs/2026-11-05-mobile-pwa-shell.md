# 共享 UI 包：控制台手机形态所需的可选档位

> Status: Approved
> Owner: 共享 UI 包（agentre-ui）；消费方是 agentre-server 控制台，桌面端不变
> Last updated: 2026-11-06

**Objective:** 给 `@agentre-hub/agentre-ui` 补上 agentre-server 控制台手机改版（agentre-server `docs/specs/2026-11-05-mobile-pwa-shell.md`，已批准）需要的导出与可选档位，让控制台在手机宽度下用包里的真组件，而不在宿主另造一份。

**Hard invariants:**
- 桌面端零变化：本轮每一项都是新导出或可选入参，不传时渲染、交互、文案与今天逐字一致；桌面宿主代码不改。现有组件测试不改断言即全绿，就是这一条的证据。
- 包不 import 任何宿主、不 import wire；新导出补进 public-api 名单。
- 文案走包内 i18n，zh-CN 与 en 同步；颜色只用现有 token。
- 不改 `pkg/wire`、`pkg/syncwire`，不加迁移。

事实标注：**[已核]** 对照 agentre `be445dee` 的包源码核过；需求依据见控制台规格，本文件只写包这一侧的契约。

## Problem

1. **控制台手机改版要的几样东西，包里没有对应档位。** [已核] 统计行 `MessageMeta` 是可换行的一组（`transcript/transcript-row-view.tsx` 里 `flex-wrap`），明细只在悬停 tooltip 里；`ChatComposer` 草稿为空时提交位只渲染停止键，插话发出后发送键消失、停止键挪位（`composer/chat-composer.tsx`）；排队条按钮 `size-5`、间距 `gap-0.5`，撤回禁用原因只在 `title`；审批卡在触屏上也画 ↵ / Esc 键帽；`SessionRow` 只有桌面密度一种；看板只有四列横滚、筛选与范围是 Popover、任务表单带底部按钮行；组织行只有一行；供应商面板是左右双栏、模型表最小 560px；后端编辑器只在弹窗里。
2. **几样现成的件没导出。** [已核] `IndexGroupHeader`、`ProjectScopePopover`、`BoardCardMenu`、`BoardFilterPanel` 等不在 `src/index.ts`。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 全部做成可选档位 / 新导出，桌面不传 | 跨宿主规则：两端都画的概念只有一份实现。Rejected：控制台宿主自己画手机版 — 违反单一实现；改默认行为 — 会改到桌面 |
| 2 | 统计行的「放不放得下」判定抽成纯函数（输入各段宽度与可用宽度，输出让到哪一档、是否出「›」） | 可单测、不依赖 DOM 量测。Rejected：直接在组件里改 DOM（原型的做法） |
| 3 | 规整模型名、紧凑用时用新函数，不改现有 `shortenModelName` / `formatTurnDuration` | 现有函数桌面在用。Rejected：改现有函数 |
| 4 | 审批停靠是新的导出组件，复用审批卡内部的提交逻辑；转录里「是否画待决审批卡」由渲染上下文开关 | 提交逻辑只能有一份。Rejected：宿主复制审批按钮 |

## 契约（每项不传时与今天一致）

### 对话：输入与排队
- **`AIChatInput`** 接受 `enterKeyHint`，写到编辑区属性上。
- **`ChatComposer` 触屏档**（`inputMode="touch"`）：回车插入换行，包括只有图片的草稿；只有按钮发送；输入法组词回车照旧不动作；提示文案不带快捷键（不写「(Esc)」「Enter 运行」）；运行中提交位始终是两格 ■ 与 ↑（无字时 ↑ 置灰），间距 8px、各 44px 命中区，发出插话前后两格位置不变；草稿为空时呈胶囊形，有字时呈卡片形，切换时输入框不重挂载、焦点不丢。物理键盘 Esc 停止照旧。
- **`QueuedMessagesBar` 触屏档**：撤回 / 删除各 44px 命中区、间距 8px；撤回禁用的原因以可见文字出现在条内。轮末未送达的条目提供「说明行」呈现：「N 条插话没送到，已放回输入框 · 清掉」，由宿主决定何时显示；`ChatComposerHandle` 提供移除最近一次追加文字的方法，仅当输入框自那次追加后未被改动时生效，否则不动作。

### 对话：转录
- **统计行紧凑档**（渲染上下文 `metaDensity="compact"`）：一行；数字 k / M 一位小数；用时 `1m32s` / `12m05s` / `49s`；模型名去厂商前缀与日期后缀；放不下依次藏 tok/s → 藏用时 → 截模型名（至少 6 个字符）；↑↓ 与复制永不藏；有藏或截时行尾「›」；首 token 只在明细里；复制是 44px 的纯图标。点行（复制除外）打开「这一轮」底部弹层（`DialogShell`）：模型完整 id、发送、接收、缓存命中、首 token、耗时、输出速度，数字完整；明细行与悬停 tooltip 共用同一份构造。宽度变化与运行中刷新时重新判定。
- **用户消息右对齐**：消息行接受对齐选项（`align="end"`）。
- **审批停靠**：新导出一个停靠形态的工具权限决策组件——「允许」整宽，「本会话始终允许」与「拒绝」并排，可选的停止槽；提交与现有审批卡同一套逻辑与端口。渲染上下文开关（`decisionPlacement="docked"`）让转录不画待决的工具权限卡（已决的照常显示）；提问卡不受影响。
- **快捷键提示开关**：渲染上下文 `hideShortcutHints` 时，审批卡与决策面板不画键帽。

### 对话索引
- **`SessionRow` 手机档**（`variant="phone"`）：44px Agent 头像；第一行标题 + 时间；第二行「Agent · 项目路径」+ 状态牌（等你 / 运行中 / 出错）或未读点；行高 ≥ 64px；按压态；可选 `highlight` 高亮搜索命中；行菜单可追加「停止这一轮」「只看这个 Agent」「只看这台机器」（各自回调存在时才出现），「删除」保留。
- **分段纯函数**：按 需要你 / 进行中 / 今天 / 更早 分段并排序（判据：`waiting_for_input`；运行中且不在等你；按「今天开始时刻」切分），空段不返回。
- **筛选胶囊**：可传筛选项列表（含「需要你」）与计数（>0 才显示），触屏尺寸 44px 命中区。
- **范围清单**：导出 `IndexGroupHeader`；新增范围清单组件，三档 项目（可折叠树）/ Agent（按部门）/ 机器（在线 / 离线），项带计数、等你点，离线半透明，搜索输入 16px。

### 看板
- `IssueBoard` 阶段档（一次一个阶段，顶部四个阶段带条数，卡片满宽）；`BoardCardMenu` 触屏档（⋯ 常显，「移动到」平铺四个阶段，编辑 / 删除另一组，长按同样打开）；`BoardFilterBar` 与 `ProjectScopePicker` 弹层档（底部弹层、chips、底部按钮写出会看到几条、输入 16px）；`TaskFormShell` 整页档（自带头部「✕ · 面包屑 · ⋯ 保存」，无底部取消 / 保存行，标题与描述 16px 以上，pill 行贴底可折两行、每颗 44px 命中区）。

### 设备与引导
- `GuideStepRail` 紧凑档；`AgentredServiceSection` 紧凑档（一张命令卡）；`AgentredInstallSection` 紧凑档（chips 选择、一张命令卡）。

### 组织
- `OrgAgentRow` 两行档（第二行「机器 · 后端」，待配置原因以可见文字写在第二行）；`OrgGroupHeader` 摘要档（「N 人 ›」）。

### 设置
- `LlmProvidersPanel` 逐层档（受控选中：列表页与详情页分开渲染；详情里连接信息一项一行 +「测试连接」；模型一行一个、开关在右，不再有最小宽度表格）；`AgentBackendsPanel` 编辑器整页档（按 基本 / 运行方式 / 高级 分组）。

## Out of scope

- 桌面端的任何可见变化与宿主代码改动。
- 控制台宿主侧的外壳、路由、页面组合、前台重连、账号页 UA 解析、登录页同意提示（在 agentre-server 落地）。
- `DialogShell` 断点不改。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 统计行紧凑档 | 缩写、规整、纯函数让位顺序与「›」；点行开明细、点复制不开；不传档位时输出不变 | `transcript/transcript-message-meta.test.tsx` |
| 输入框触屏档 | 回车换行（含仅图片）、组词回车、两格提交位稳定、无快捷键文案、胶囊 ↔ 卡片不丢焦点；不传时不变 | `composer/chat-composer.test.tsx`、`editor-options-stability.test.tsx` |
| 排队条 | 触屏命中区、禁用原因可见、说明行与「清掉」只移除未改动的追加 | `composer/queued-messages-bar.test.tsx` |
| 审批停靠与上下文开关 | 停靠组件提交走同一端口；docked 时转录不画待决权限卡、提问卡照常；无键帽 | `tool-permission/card.test.tsx`、`registry.test.tsx` |
| 索引 | 手机行内容与菜单项条件；分段纯函数；筛选胶囊；范围清单 | `session-index/*` 现有测试 |
| 看板 / 引导 / 组织 / 设置档位 | 各档位的结构与命中区；不传时不变 | `board/__tests__/*`、`onboarding/*`、`org/*`、`agent-backends-list.test.tsx` |
| public API | 新导出在名单里 | `public-api.test.ts` |

桌面不变由桌面前端全量测试（`pnpm typecheck && pnpm test`）兜底；手机上的观感由控制台一侧的真机验收覆盖。

## Open questions

（无）
