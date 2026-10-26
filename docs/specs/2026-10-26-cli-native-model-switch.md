# CLI 自带模型：选择与运行中切换

> Status: Approved
> Owner: agentre 会话体验 / agent runtime；agentre-server web 控制台
> Last updated: 2026-10-26

**Objective:** 走 CLI 自身登录态（未绑 Agentre 供应商）的 Claude Code / Codex / Pi，在 Agent 后端设置与会话里都能看到并选择**该 CLI 自己提供的模型**；会话内切换不重启 CLI、不丢上下文；本机与远端 agentred 行为一致；什么都没选时也写出 CLI 实际会用的模型及其品牌标识。

**Hard invariant:**

1. 绑了 Agentre 供应商的后端与会话，模型解析、网关改写、`--resume` 重启的既有行为不变。
2. 会话没有任何新选择、后端也没设默认模型时，每轮下发给 CLI 的参数与今天逐字相同（不下发 `--model` / `model`）。
3. 切换失败绝不静默：用户看到的「当前模型」永远是 CLI 实际在用或下一轮实际会用的那个。
4. 新 UI 文案走 i18n（`zh-CN` / `en` 同时补齐），控件用 `@agentre-hub/agentre-ui` 的 shadcn primitive；桌面端与 web 控制台共用同一套展示组件。

## Problem

证据标注：【核实】= 读代码或在真实 CLI 上实测确认；【推断】= 机制确认、后果未实测。

1. **CLI 登录态下看不到、也选不了 CLI 自己的模型。**【核实】会话 composer 的模型 pill 在未绑供应商时只写「跟随 Agent 绑定 · CLI 自身登录态」（`agentre-ui/src/engine/model-target-picker/provider-pill-trigger.tsx`），弹层里只有 Agentre 供应商目录；模型列表的唯一来源是供应商 `/v1/models`（`model-target-picker/catalog.ts`）。
2. **后端设置里只有 Claude Code 能指定 CLI 模型，且是自由文本。**【核实】`DefaultModel` 仅 claudecode 可填（`agent-backends-fields.tsx` `DefaultModelField`），codex / piagent 的 `ValidateExtra` 对非空 `DefaultModel` 直接报错（`agent_backend_entity/kinds.go`）。
3. **没有任何运行中换模型的通道。**【核实】`pkg/claudecode`、`pkg/codex`、`pkg/piagent` 均无 `set_model` / `SetModel`；今天唯一的「切换」是换 Agentre 供应商目标，靠改变 launch identity 让 CLI 以 `--resume` 重启（`chat_svc/session_provider.go` `SetChatSessionModelTarget`）。
4. **三个 CLI 都已提供结构化的模型列表、实际模型查询与热切换。**【核实，2026-10-26 本机实测，均未消耗 token】

   | CLI（版本） | 模型列表 | 实际会用的模型 | 运行中切换 |
   |---|---|---|---|
   | Claude Code 2.1.296 | `initialize` 应答 `models[]`（`value` / `displayName` / `resolvedModel` / `supportedEffortLevels`），13 条 | `get_settings` → `applied.model`；`ANTHROPIC_MODEL=sonnet` 时返回 `claude-sonnet-5-5`（`models[]` 里的 `default` 项此时仍写 Opus，**不可用来判定**） | `set_model` 控制请求：合法值成功且 `get_settings` 立即反映；非法值返回 `Model '…' not found`；不带 `model` 回到默认 |
   | Codex 0.162.1 | app-server `model/list`（`id` / `displayName` / `isDefault` / `defaultReasoningEffort`） | `config/read` → `config.model`；`-c model=gpt-6-luna` 时返回 `gpt-6-luna` | `turn/start` 的 `model` 参数，schema 描述为「Override the model for this turn and subsequent turns」 |
   | Pi 1.1.0 | RPC `get_available_models`（按 pi 自己的供应商分组） | RPC `get_state` → `model` | RPC `set_model {provider, modelId}`：成功返回模型对象；不存在返回 `Model not found: …` |

   旧规格 #26（`06eec4fa`，已被 #33 替代）决策 7 以「三种格式不一致、要加 per-backend 发现逻辑」拒绝 CLI 自有目录；该依据已不成立。

## Actors and user stories

1. 作为用 Claude Code / Codex / Pi 自身登录账号的用户，我希望在会话里直接挑 CLI 自己的模型，并在会话进行中随时换，不必重开会话或去改 Agent 配置。
2. 作为配置 Agent 后端的用户，我希望为 CLI 登录态的后端选一个默认模型（从 CLI 给的列表里选），三种 CLI 都能设。
3. 作为没做任何选择的用户，我希望一眼看出这一轮实际会跑哪个模型。
4. 作为远端 agentred 会话的用户，我希望以上一切在远端同样成立，且列表来自那台机器上的 CLI。
5. 作为 web 控制台用户，我希望看到与桌面端一致的模型 pill 与后端设置。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 只覆盖 **CLI 登录态**（后端与会话都未落到 Agentre 供应商）下的 claudecode / codex / piagent | 供应商模式下网关逐请求改写 `body.model`（`httpgateway/llmforward.go`），CLI 侧换模型会被覆盖。Rejected: 供应商模式也走 CLI 热切换——与网关两套真相 |
| 2 | 会话里**同一颗模型 pill** 承载（用户决定，mockup 方案 A） | 一个控件回答「这一轮跑什么」。Rejected: 另加一颗「CLI 模型」pill——两颗说同一件事的两半，切到供应商后还要消失 |
| 3 | 模型列表取自 CLI 自身接口（见 Problem 4），不硬编码、不走供应商目录 | 列表随 CLI 版本与账号权限变化，CLI 自己最清楚。Rejected: 硬编码清单——每次 CLI 升级都要发版；只给自由文本——#26 的做法，无从选择也无从校验 |
| 4 | 「跟随 CLI 默认」的解析结果取 CLI 的**生效配置**查询（claude `get_settings.applied.model`、codex `config/read`、pi `get_state`） | 已实测反映环境变量与 CLI 自身配置的覆盖。Rejected: claude `initialize.models` 的 `default` 项——实测不反映覆盖，会写错 |
| 5 | CLI 侧的选择是三态：**跟随 Agent 绑定**（会话无选择）/ **跟随 CLI 默认**（不下发模型）/ **固定某个 CLI 模型** | 与既有「跟随 Agent 绑定 / 跟随供应商默认 / 固定模型」同构，弹层与 pill 复用同一套视觉语义（↻ = 跟随默认）。Rejected: 只有「固定」——无法表达「随 CLI 升级换默认」 |
| 6 | 会话内切换**不重启 CLI**：空闲时立即作用于在跑的 CLI；一轮进行中则挂起，下一轮开始前生效（用户未反对推荐项） | 三个 CLI 都有热切换接口；codex 只能在 `turn/start` 生效，统一成「下一轮」才对三者说同一句话。Rejected: 轮内立即生效——codex 做不到，三者语义分裂 |
| 7 | 后端设置的模型绑定 Picker 吸收 CLI 自带模型；claudecode 的「自定义模型」自由文本框删除，手输入口收进 Picker；codex / piagent 获得同一能力，值存在既有同步键 `config.defaultModel` | 一个字段讲完「这个后端默认跑什么」；沿用已同步的键，不新增同步契约。Rejected: 保留自由文本框并另加下拉——同一件事两个入口 |
| 8 | 「跟随 Agent 绑定」时 pill 与弹层首项的图标改为**解析到的模型的品牌标识**（用户决定），供应商模式同样适用 | 品牌标识比人形图标更快认出模型；文案「跟随 Agent 绑定」保留作模式信号。Rejected: 只写模型 + 尾部人形小标——与「会话自选」难以区分 |
| 9 | 远端会话的列表、解析、切换在**执行该会话的设备**上完成，经新增的 wire 调用；随之抬协议版本号 | 列表与登录账号属于那台机器；wire 兼容判据是协议版本**逐字相等**（`pkg/wire/README.md`「版本与 pin」），版本不同两端根本握不上手，因此不存在「旧 daemon 收到新调用」的中间态，不另设能力位。Rejected: 用本机 CLI 列表代替——两台机器 CLI 版本/账号不同时给出跑不了的选项；加能力位兜旧 daemon——握手已挡住，多一条永远走不到的分支 |
| 10 | 切换落库时追加一条持久 notice（复用既有「会话设置切换」notice 骨架，与换供应商、换推理强度同一形态） | 逐条消息的 model 字段看不出分界；与既有切换一致。Rejected: 只改 pill 不留痕——事后回看对话不知从哪换的 |

## 模型选择的三态与解析

- **跟随 Agent 绑定**（会话从未在 CLI 段做过选择）：本轮模型 = 后端设置的默认模型；后端也是「跟随 CLI 默认」时 = CLI 生效配置里的模型。
- **跟随 CLI 默认**：不向 CLI 下发模型参数；显示值 = CLI 生效配置里的模型。
- **固定某个 CLI 模型**：值是 CLI 认的那个标识（claude 的别名或完整名、codex 的 id、pi 的 `provider/id`），每次启动 CLI 与每次切换都下发它。
- 会话从 CLI 段切到 Agentre 供应商（或反向）走今天的供应商切换路径（`--resume` 重启），两套选择互斥：会话一旦选了供应商目标，CLI 段的选择即清除，反之亦然。
- 后端若绑了 Agentre 供应商，CLI 段不出现，后端的 `defaultModel` 不再有任何效果（与今天 claudecode 「绑定供应商期间保留但被忽略」一致）。

## 会话：composer 模型 pill

- **脸**写「这一轮实际会跑的模型」：
  - 跟随 Agent 绑定且已解析：`[模型品牌标识] 跟随 Agent 绑定 · <模型展示名>`；
  - 跟随 Agent 绑定但尚未解析（首次打开、本机无缓存，正在问 CLI）：`[转圈] 跟随 Agent 绑定`，问到后补上模型；
  - 固定模型：`[品牌标识] <模型展示名>`；
  - 跟随 CLI 默认：`[品牌标识] <解析到的模型展示名> ↻`；
  - 一轮进行中做了新选择：`<当前模型> → <新模型>`（新模型用 primary 色），直到下一轮开始。
- 品牌标识按解析出的模型 ID 匹配（Claude / GPT / GLM / Grok / DeepSeek 等，复用既有模型标识组件）；匹配不到时依次回落供应商标识、后端标识，最后人形图标，不留空位。
- **弹层**自上而下：搜索框 → 「跟随 Agent 绑定」特殊项（首图标为解析模型的品牌标识，副行写 `→ <CLI 名> · <解析结果>`）→ 「<CLI 名> 自带 · CLI 登录账号」段（段头带刷新按钮；首行「跟随 CLI 默认 → <解析模型>」用跟随默认的强调底；其后「固定模型」列表，行 = 展示名 + 等宽模型 ID；Claude 钉版本号的旧型号折叠进「其他版本 · N」；Pi 按 pi 自己的供应商分小节；末行「输入其他模型 ID…」）→ Agentre 供应商分组（与今天相同）。
- 一轮进行中，弹层里当前在用的那行标「本轮在用」，底部常显一行「这一轮仍用 X 跑完，下一轮起用 Y」。

## 会话：切换的行为

- **空闲时选择**：选择落库，同时作用于在跑的 CLI 进程（claude / pi 立即发送切换请求；codex 在下一次 `turn/start` 携带），CLI 进程不重启，下一条消息即用新模型；追加切换 notice。
- **一轮进行中选择**：选择落库并追加 notice，正在跑的这一轮不受影响；下一轮开始前把新模型发给 CLI。连续多次选择只以最后一次为准。
- **CLI 进程不在**（从未启动、被空闲清扫、应用重启）：下次启动 CLI 时以会话当前选择启动（固定模型下发模型参数，跟随 CLI 默认不下发）。
- **CLI 拒绝**（立即切换时返回错误）：会话选择不变、pill 不变，弹层底部显示「切换没有生效：<CLI 原文>」，不追加 notice。
- **下一轮开始前应用失败**（挂起的选择或 codex 的 `turn/start` 被拒）：这一轮不开始，会话里显示带 CLI 原文的错误；pill 进入既有的「失效」态，提示重新选择；不得静默回落到旧模型继续跑。
- **CLI 静默用了别的模型**：一轮结束后，若会话是固定模型且 CLI 上报的实际模型与所选不同，追加一条持久提示「所选模型 X 未生效，实际使用 Y」。
- **新建会话（发送第一条消息前）**：同一颗 pill 可选，选择随第一条消息落到新会话，首轮即以它启动 CLI；不追加 notice（没有「之前」）。
- 切换不影响权限模式、推理强度、工作目录与供应商绑定。

## 模型列表与解析的获取

- 列表与「跟随 CLI 默认」的解析由该（设备, 后端）的一个**不带模型参数**、只做查询的短命进程回答（环境与 CLI 路径同该后端在 CLI 登录态下的真实启动），不发送任何用户消息、不消耗 token，问完即退。不问会话里在跑的进程：它可能是以某个模型启动或已切换过的，答出的是该会话的模型而不是 CLI 默认。
- 会话 pill 上「这一轮实际会跑的模型」取自该会话自己的选择与其 CLI 进程上报的当前模型，不取自上面的默认解析。
- 结果按（执行设备, 后端）缓存，弹层段头的刷新按钮与后端配置变化（CLI 路径、环境变量、绑定）使其失效；缓存只在内存，不落库、不同步。
- 列表获取中：段内显示「正在向 <CLI 名> 读取可用模型…」。
- CLI 不支持列表接口（版本过旧）或调用失败：段内显示警示条（CLI 名 + 原始错误）+ 手输模型 ID 框 + 「重试」；手输的值照常可选、照常经 CLI 校验。
- 解析不出生效模型时，「跟随 CLI 默认」行副行写「由 CLI 决定」，pill 脸写「跟随 Agent 绑定」不带模型名，不猜。

## Agent 后端设置：模型绑定

- 模型绑定 Picker（backend 场景）顶部原「CLI 自身登录态」一项展开为「<CLI 名> 自带 · CLI 登录账号」一段：「跟随 CLI 默认」+ 固定模型列表 + 「输入其他模型 ID…」，其下仍是 Agentre 供应商。
- 触发按钮：主行 `[后端标识] <CLI 名> 登录账号 [跟随默认|固定]`，副行 `生效模型：<展示名> · <模型 ID>`（跟随默认时写「CLI 默认变化会跟着变」）。
- claudecode 的「自定义模型」输入框删除；它已有的值按固定模型显示在 Picker 里（列表里没有的显示为手输值，不报失效）。
- codex / piagent 在 CLI 登录态下同样可保存默认模型；绑定供应商时该值被忽略且不显示。
- 后端在远端设备上执行时，Picker 顶部沿用既有「在 <设备> 上执行」说明，列表来自那台机器。
- 保存的值通过同步到达其它端（既有 `config.defaultModel` 键），web 控制台编辑同一字段的行为与桌面端一致。

## 远端 agentred

- **桌面端发起、在 agentred 上执行的会话**：会话的 CLI 模型选择与今天的供应商目标一样随每轮运行请求带到 agentred，agentred 据此启动或续用 CLI；空闲时的即时切换经一条新的会话级运行时调用送到 agentred 上该会话的 CLI（与切换权限模式同一路径），应答失败按「CLI 拒绝」处理。
- **列表与生效模型**：桌面端向执行设备发起一条新的设备级查询（入参为后端类型与该设备上的 CLI 路径），由那台机器上的 CLI 回答；连接不上或查询失败时 CLI 段显示错误态（「CLI 不支持列表接口」同一外观，原文为连接错误），不回退到本机 CLI 的列表。
- 弹层与后端 Picker 顶部沿用既有「在 <设备> 上执行」说明。
- **协议版本**：新增的调用改变方法集，协议版本号随之抬升；桌面端、agentred、agentre-server 必须同批升级，版本不同的两端按既有规则拒绝握手。

## agentre-server web 控制台

- **会话模型 pill**：web 会话详情与新建会话草稿使用与桌面端相同的共享 pill / 弹层，行为与桌面端一致（三态、挂起、错误、品牌标识）。
  - 列表与生效模型经中继向**承载该会话的机器**（桌面端或 agentred）查询；
  - 切换经一条新的会话级设置调用写到承载机器，再按 web 既有做法同步写回会话的发起机器（与今天切换供应商目标、推理强度同一套「两处写、全失败才回滚」规则）；承载机器上的 CLI 在跑时立即生效，规则同桌面端；
  - 新建会话草稿的选择在首条消息派发成功后写入新会话（与今天草稿选供应商目标相同）。
- **会话镜像**：服务端镜像的会话摘要新增「CLI 模型选择」字段，web 刷新 / 换设备打开会话时 pill 显示与承载机器一致；新增列走 `agentre-server` 自己的追加迁移，DDL 须同时在 MySQL 8.4（prod）与 9.7（dev）上可执行。
- **后端设置**：web 设置页渲染的就是共享后端编辑器；codex / piagent 的 `config.defaultModel` 随既有载荷保存与同步，服务端不新增字段。编辑器里的 CLI 模型列表经中继向该后端所在设备查询，设备不在线时 CLI 段显示错误态并允许手输。
- **中继**：服务端中继对 RPC 帧不透明转发，不为新调用写路由代码；只需抬 `pkg/wire` 与 `@agentre-hub/agentre-wire`、`@agentre-hub/agentre-ui` 的钉。

## 交付顺序

1. `agentre`：共享 UI、wire（含协议版本）、桌面端、agentred 一起改完、验证、推送。
2. `agentre-server`：钉到上一步推送的不可变 revision，接 web 会话 pill 与镜像字段（含迁移），验证。
3. 两个仓库各自提交；部署时 agentred、桌面端、服务端同批升级（协议版本相等才握手）。

## Out of scope

- 推理强度随模型联动（CLI 列表里的 `supportedEffortLevels` / `defaultReasoningEffort`）：推理强度 pill 不变，后续单独处理。
- Claude Code 供应商模式下的 OPUS / SONNET / HAIKU 分级路由。
- builtin / hermes / openclaw 后端。
- subagent 的模型选择。
- pill 以外的切换入口（如 slash 命令 `/model`）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 协议包（`pkg/claudecode` / `pkg/codex` / `pkg/piagent`）对 fake CLI 的帧 | 列表 / 生效模型查询 / 切换请求的编码与应答解析，含「not found」错误 | `pkg/claudecode` 的 `set_permission_mode` 测试 |
| 各 runtime | 三态 → 启动参数（不下发 / 下发）；空闲切换不重启进程；进行中切换挂起到下一轮；launch identity 不因 CLI 模型变化而逐出进程 | claudecode `launchedEffort` 相关测试 |
| `chat_svc`（mockgen repo mock） | 选择落库 + notice；拒绝不落库；下一轮应用失败使轮次失败；实际模型偏离提示 | `session_provider` 既有测试 |
| 远端 wire + daemon / 桌面端入站 handler | 新调用的编解码 round-trip；daemon 与桌面端入站路由到 runtime / chat_svc；方法集指纹守卫与 wireinbound 契约表随新调用更新 | `set_model_target`、`set_session_reasoning_effort` 的 wire / handler 测试；`wireversion/methodset_test.go`；`contract_guard_test.go` |
| `agentre-server` 镜像（sqlmock） | 会话摘要新字段的镜像写入与读出 | `agent_session_repo/summary` 既有测试 |
| `agentre-server` web（vitest） | 会话 pill 经中继查询与切换、两处写回滚规则、草稿首发后写入 | `SessionDetailView` / `DraftSession` 既有模型目标测试 |
| 共享 UI（vitest） | pill 各脸（含转圈、挂起、↻、品牌标识回落链）；弹层三种 CLI 的分段与折叠；加载 / 错误 / 手输；后端 Picker 的 CLI 段与旧 `defaultModel` 值的显示；i18n 覆盖 | `model-target-picker/picker.test.tsx`、`provider-pill-trigger.test.tsx` |
| 不可自动化 | 真实 CLI 的热切换与「下一轮生效」，三种 CLI × 本机/远端各走一遍，记录到验证报告 | `docs/verification.md` |

## Open questions

<!-- Must be empty before approval. -->
