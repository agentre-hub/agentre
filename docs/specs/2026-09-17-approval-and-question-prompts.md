# Approval and question prompts for Hermes and OpenClaw

> Status: Approved
> Owner: desktop app maintainers
> Last updated: 2026-09-22

**Objective:** Hermes 与 OpenClaw 在 Agentre 发起的一轮里请求审批或向用户提问时，用户在转录里看到卡片并作答，后端立即拿到答复继续；Agentre 无法承接的请求立即跳过并留下可读提示；OpenClaw 让出（yielded）的一轮在后续 run 结束前保持进行中。任何这类请求都不再让一轮无声空等到超时。

**Hard invariant:** claudecode / codex / piagent / builtin 的审批与提问卡片行为不退化；秘密答案（OpenClaw `isSecret` 问题）不进入转录、同步、日志与 RPC 回显。

## Problem

以下问题 1–3 出自 hermes-agent `f5d19261` 源码，4–7 出自 openclaw/openclaw `847aef50` 源码。2026-09-21 在临时实例（hermes-agent main `07888c880f`、OpenClaw 2026.9.5）上用调用 Agentre 真实 hermes / openclaw 客户端代码的探针复现，标「已复现」的为当日实测结果。

1. **Hermes 审批送不到用户，命令被撤回。** Hermes 桌面契约 v7 把审批与提问做成 server→client 请求 `approval` / `clarify`（`tui_gateway/contracts/server_requests.py`）。上游 `f9d178f78e` 起，连接未发 `client.capabilities {server_requests: true}` 时 Gateway 不发出请求帧、直接按「无人可答」处理；客户端答 -32601 也一样。Agentre 两者都是：从不声明该能力（`internal/pkg/agentruntime/runtimes/hermes/client.go`），收到反向请求一律回 -32601（`runtimes/hermes/frame.go:233`）。结果是审批立即以「attached client cannot answer approval requests」撤回（不是拒绝），命令不执行，用户看不到卡片。（已复现：约 11.6 秒撤回、Agentre 未收到任何请求帧，一轮约 20 秒结束。`f5d19261` 上撤回路径尚不存在，审批会空等 `approvals.timeout` 300 秒——该症状上游已修）
2. **Hermes 提问被静默跳过**：同一 -32601 让 `clarify` 立即得到空答案（`tui_gateway/server_requests.py`）；未声明能力时同样不发帧、直接返回空。（已复现：0.03 秒得到空 `user_response`）
3. **Hermes 其余反向请求空等**：`sudo`、`secret`、`vault.*`、`terminal.read`、`preview.*`、`window.read`、`tour`（`contracts/server_requests.py:98-208`）。（源码推断）
4. **OpenClaw exec approval 送不到 Agentre。** 自 2026.8 起 Gateway 只把审批投递给声明了审批能力或特定客户端 id 的连接（`src/gateway/server-request-context.ts:179-205`）；Agentre 以 `client.id:"cli"` 连接、connect 不带 `caps`（`internal/pkg/openclawgateway/client.go`），`canDeliverApprovals` 判为不可投递。无其他审批端时 exec 立即被拒（「Headless runs cannot wait for interactive exec approval」），有其他审批端时 Agentre 看不到卡片。（已复现：Agentre 一轮 17 秒结束、无审批事件。对照：同一客户端仅在 connect 加 `caps: ["approvals"]`，6.7 秒收到 `ExecApprovalRequested`，现有 exec approval 处理可接住）
5. **OpenClaw ask_user 无界面空等。** Agentre 未申请 `operator.questions`、忽略 `question.*`（`runtimes/openclaw/translator.go:19-60`）；一轮挂起直到默认 900 秒后以 no_answer 继续。（已复现：问题在 Gateway 上保持 `pending`、900 秒过期，Agentre 一轮无任何事件、空等至探针 261 秒截止）
6. **OpenClaw 插件与系统代理审批未处理。** 统一审批 `approval.*` 的 `plugin`、`system-agent` 两类（`packages/gateway-protocol/src/schema/approvals.ts:19-21`）Agentre 不认识；修复问题 4 后它们会送达却无人答复（插件审批最长 600 秒）。（源码推断）
7. **OpenClaw 让出的一轮被当作结束。** 终态带 `yielded:true`（`schema/logs-chat.ts:359`）表示父任务仍在等待；Agentre 结束本轮并断开，后续 run 的输出不进入对话。（已复现：`sessions_yield` 后 Agentre 11.5 秒收到 Done；随后 `announce:requester-settle…:yield-1` run 的「FINAL: SUBAGENT-RESULT-42」只出现在 Gateway 会话历史中）
8. **（待定，可能与问题 4 同源）OpenClaw 以 `agent` 方法提交的一轮无法等待审批。** Agentre 在带模型覆盖且连接具备 `operator.admin` 时改用 `agent` 提交；在 OpenClaw 2026.9.4 上曾以探针观察到这样启动的 run 遇到 exec 审批立即被拒（「Headless runs cannot wait for interactive exec approval」）。2026-09-21 实测：不带模型覆盖（`chat.send`）时出现同一报错，connect 声明 `approvals` 能力后消失，故该报错的直接原因是无可投递审批端（问题 4），不是提交方式。「带模型覆盖 + 声明能力」组合未测；修复问题 4 后需补测，确认问题 8 是否独立存在。
9. **能力声明不含审批与提问。** Hermes 只声明 abort（`hermes/runtime.go:80`）；OpenClaw 只声明 abort 与 exec approval（`runtimes/openclaw/runtime.go:66-71`）。（已验证）

## Actors and user stories

1. As a `使用 Hermes 或 OpenClaw 的用户`, I want 在转录里看到后端要执行的操作并选择允许一次 / 本会话允许 / 永久允许 / 拒绝, so that agent 不必等超时就能继续。
2. As a `使用 Hermes 或 OpenClaw 的用户`, I want 回答后端提出的单个或成组问题（含需要保密的输入），或选择跳过, so that agent 按我的意图继续。
3. As a `使用 Hermes 的用户`, I want 在 Hermes 请求了 Agentre 无法提供的输入时看到说明, so that 知道 agent 为什么没拿到它。
4. As a `使用 OpenClaw 的用户`, I want 等待子任务的一轮在真正完成前保持进行中, so that 我能看到完整回答，也能随时停止。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 审批统一走 exec approval 生命周期，并把它与卡片泛化为后端无关的「审批」：Hermes 审批、OpenClaw exec / plugin / system-agent 审批共用 | 用户决定。决定集由后端给出、有 resolved / expired 终态（`internal/pkg/agentruntime/event.go:113-137`、OpenClaw `approvals.ts:25-65`）。Rejected: 接 tool permission——没有过期状态，表达不了永久允许 |
| 2 | 永久允许按后端给出的决定集如实呈现，文案说明影响范围 | 用户决定。Rejected: 隐藏 |
| 3 | 提问统一接入现有 answer-user-ask 卡片 | Hermes clarify 与 OpenClaw question 的单问 / 成组、单选 / 多选与现有卡片形状一致。Rejected: 新卡片 |
| 4 | Hermes 无对应能力的反向请求立即答复空值并在转录中留提示 | 用户决定。Rejected: 静默跳过；为 sudo/secret 做输入卡 |
| 5 | OpenClaw `isSecret` 问题以遮蔽输入作答，答案只发给 Gateway，转录中显示为已作答但不含内容 | 否则秘密问题只能取消，用户无法在 Agentre 里完成需要凭据的步骤。Rejected: 取消秘密问题——ask_user 结果为 no_answer |
| 6 | OpenClaw 终态 `yielded:true` 时本轮保持进行中，直到后续 run 结束 | 用户决定。Rejected: 结束本轮、后续作为自主轮次——需要会话级持续监听，归入后续能力补全 |
| 7 | 只支持 Hermes 契约 v7、OpenClaw ≥ 2026.8；移除 Hermes v6 事件路径 | 尚未发布，不考虑兼容（用户决定） |
| 8 | Hermes 有选项的问题不提供「其他」自由输入；OpenClaw 按问题的 `isOther` 决定 | Hermes 拒绝非选项文本（`tools/clarify_gateway.py:160-183`）；OpenClaw 问题自带是否允许其他（`schema/questions.ts:44`） |
| 9 | OpenClaw chat status（准备工作区、限流重试）不进本 spec | 用户决定：只影响观感，呈现形态需单独设计 |

## Approval cards

- **前置**：在 Agentre 发起的一轮中，Hermes 发出 `approval` 请求，或 OpenClaw 为该轮的会话产生 exec / plugin / system-agent 审批。
- **送达**：Hermes 连接必须在每条连接上声明 `client.capabilities {server_requests: true}`，否则 Gateway 不发审批与提问请求；OpenClaw 连接必须让 Gateway 把本会话的三类审批投递给 Agentre；Agentre 连着时审批不得以 no-route 被拒，也不得只出现在其他审批端。无论 Agentre 以哪种方式提交这一轮（含带模型覆盖的情形），审批都必须能等待用户作答，不得因提交方式被立即拒绝。
- **呈现**：卡片显示后端给出的已脱敏内容——Hermes：命令、说明、工具名；OpenClaw exec：命令与警告；plugin：插件、工具与说明；system-agent：动作类别及其范围（发送消息的目标与人数、付款金额与对象、对外发布的目标与可见性、长期授权的自动化与命令）。按钮只包含后端允许的决定：允许一次、本会话允许（仅 Hermes `session`）、永久允许（Hermes `always` / OpenClaw `allow-always`，附影响范围说明）、拒绝。
- **作答**：用户点选后 Agentre 以该决定答复；卡片进入已处理并显示所选决定。重复点击或多端同时作答只生效一次，各端收敛到同一终态。
- **撤回与过期**：后端因超时、中断、会话关闭、重启撤回请求时卡片进入已过期；在别处已答复时显示为已处理及其决定。一轮以任何方式结束时仍未答复的卡片显示为已过期。
- **重连**：OpenClaw 连接断开重连后，仍未答复的审批恢复为可操作卡片，已结束的收敛到终态（沿用现有 exec approval 对账语义，扩展到三类）。

## Question cards

- **前置**：Hermes 发出 `clarify`（单问或成组，每问有可选选项与是否多选），或 OpenClaw 为本轮会话产生 `question.requested`（1–3 个问题，每问最多 4 个带描述的选项，可多选、可允许其他、可为秘密输入）。
- **呈现**：现有提问卡片逐项呈现；有描述的选项显示描述；多选题允许勾选多个；允许其他或无选项的问题显示文本输入；秘密问题显示遮蔽输入。
- **作答**：一次提交全部答案。Hermes：单选答复所选选项文本，多选答复所选选项文本的 JSON 数组字符串，无选项答复输入文本，成组按 `qid` 汇总。OpenClaw：每个问题答复字符串数组。
- **跳过**：Hermes 单问答复空串、成组答复整组取消；OpenClaw 以取消答复。卡片显示已跳过。
- **撤回与过期**：后端撤回或过期时卡片显示为已跳过，不再可操作；在别处已答复时显示已作答。
- **秘密答案**：只发往 Gateway；转录、持久化、同步、日志与多端回显中只记录「已作答」，不含内容。

## Unsupported Hermes requests

- **前置**：Hermes 发出 `sudo`、`secret`、`vault.unlock_prompt`、`vault.save_login`、`vault.code`、`terminal.read`、`preview.read`、`window.read`、`preview.act`、`tour`，或 Agentre 不认识的反向请求方法。
- **结果**：立即答复——已知方法按契约答 `{value:""}`，未知方法答复方法不存在——并在转录中插入一条提示：「Hermes 请求了〈用途〉，Agentre 暂不支持，已跳过」。用途按请求类别给出（如「sudo 密码」「密钥」「读取终端输出」），不贴协议方法名或原始参数；`sudo`、`secret`、`vault.*` 的参数不进入转录与日志。

## OpenClaw yielded turns

- **前置**：本轮收到带 `yielded:true` 的终态。
- **结果**：本轮保持进行中，继续接收同一会话后续 run 的输出，直到后续 run 以非让出的终态结束；期间出现的审批与提问照常呈现。用户停止时，当前正在运行的 run 被中止，本轮结束。连接中断时沿用现有重连对账；无法确认后续 run 状态时本轮以可读错误结束，不无限等待。

## Capabilities and documentation

- Hermes 声明 abort、审批、回答提问；OpenClaw 声明 abort、审批、回答提问。其余能力留给后续能力补全。
- `docs/agent-backend.md` 能力矩阵、运行时特征与 §2.8 / §2.9 同步更新：审批改为后端无关描述，记录两后端的决定映射、撤回与跳过语义、Hermes 不支持请求的处理；§2.8 中「以 `agent` 提交」更正为代码实际的 `chat.send`。

## Out of scope

- steer、审批模式、压缩、分支 / 重生成、图片输入、上下文窗口、goal、自主轮次（含 OpenClaw heartbeat / cron）→ 后续能力补全（C2 / D2）。
- OpenClaw chat status 呈现（决定 9）。
- Hermes sudo / secret / vault 的真实输入卡片。
- Hermes 连接中断后经 `session.events.since` 恢复未答复请求。
- Hermes / OpenClaw 在 agentred 上执行（卡片跨设备转发随之生效）→ spec B。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| Hermes runtime 对假 gateway 帧脚本 | `approval` / `clarify` 产生事件；答复形状（决定、单选、多选 JSON 数组、成组、跳过、整组取消）；`request.cancel` → 过期 / 已跳过；轮次结束未答复项过期；不支持的方法立即答复并产生提示，敏感参数不出现在事件与日志 | `runtimes/hermes/client_test.go`、`runtime_test.go` |
| OpenClaw 客户端与 runtime 对假 Gateway | 连接声明使三类审批与提问可投递；三类审批与问题的事件、答复与终态；秘密答案不出现在事件、日志；重连对账覆盖三类审批与问题；`yielded:true` 保持本轮直到后续 run 终态，停止中止当前 run | `openclawgateway` 与 `runtimes/openclaw` 既有测试 |
| 两后端能力矩阵测试 | 声明与实现一致 | `TestXxxCapabilities` |
| chat_svc 审批 / 提问作答入口 | 泛化审批入口接受 `allow-session`；秘密答案不落库 | `chat_svc/exec_approval` 与 ask 既有测试 |
| 共享包审批卡片（Vitest） | 按允许决定渲染按钮、三类 OpenClaw 与 Hermes 的内容呈现、已处理 / 已过期、永久允许说明 | `openclaw-exec-approval/card.test.tsx` |
| 共享包提问卡片（Vitest） | 选项描述、其他输入按后端规则出现、遮蔽输入、秘密答案回显为已作答 | `canonical-tool/user-ask` 既有测试 |

不能自动化、由运行时验证覆盖：修复前的复现已于 2026-09-21 完成（结果见 Problem）。在临时实例（coding.local 上以隔离 HOME 运行的上游 main `hermes serve`、docker.local 上以容器运行的最新 OpenClaw Gateway）上，修复后先补测问题 8（带模型覆盖提交的一轮能否等待 exec 审批），再用桌面端依次触发 Hermes 审批（允许一次、拒绝、超时过期）、单选 / 多选 / 成组提问与跳过、一个不支持的反向请求，以及 OpenClaw exec 审批、ask_user（含秘密问题）、一个会让出的任务，并抓帧核对答复被后端接受。

## Open questions
