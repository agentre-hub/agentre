# LLM Provider 多模型与统一 ModelTarget

<!-- File: docs/specs/2026-08-11-llm-provider-models.md -->

> Status: Approved
> Owner: LLM configuration / chat runtime
> Last updated: 2026-08-11

**Objective:** 将本地 LLM 配置从“一条 Provider 直接承载一个模型”改为 `LLMProvider 1 → N LLMProviderModel`，并让 Agent Backend、Claude Tier Route、新建会话和已有会话使用同一套可验证的 Provider/Model 目标语义，在本机与远端 agentred 上得到一致的下一轮执行结果。

**Hard invariants:**

1. 延续 #39 已发布到当前主线的会话切换语义：已有会话在运行中也可选择新 Provider/Model，选择立即保存，当前正在运行的回合不受影响，新目标从下一轮生效。
2. Provider、Models、BaseURL 与 API Key 由账号同步托管；`agentre-server` 的 `llm_provider` 同步对象保存完整 Provider/Model 正文，并且只向设备 JWT 下行明文 Key。
3. 已登录账号的桌面端与 agentred 自动接收账号凭据；不再有逐机「复制凭据到这台机器」确认。浏览器永不回显明文 API Key。
4. OpenClaw 继续使用 Gateway-native agent/model 数据源，不消费 Agentre ProviderModel。
5. 所有执行路径通过唯一的 EffectiveLLMConfig 解析口决定实际 Provider 与 Model，不允许 Runtime、Gateway、Goal 或展示路径各自重新拼装优先级。

## Problem

1. **Provider 与 Model 目前被压在同一行。** `internal/model/entity/llm_provider_entity/llm_provider.go` 的 `LLMProvider` 同时保存 Provider 级 `Type/APIKey/BaseURL` 和单模型 `Model/MaxOutput/ContextWindow`；`migrations/202608080001_llm_providers.go`、`internal/service/llm_provider_svc/types.go` 与 Provider 管理 UI 都沿用这一形状，因此同一凭证无法持久管理多个可选择模型。
2. **最新会话切换只持久化 ProviderKey。** HEAD `dfe6e262` 通过 `internal/service/chat_svc/session_provider.go::SetChatSessionProvider`、`chat_repo.UpdateProviderKey` 和 `ProviderPill` 支持已有会话切换供应商并从下一轮生效，但 `chat_sessions` 没有稳定 ModelKey，同一 Provider 内无法表达动态默认与固定模型两种意图。
3. **Backend 与 Claude Route 不能稳定引用具体模型。** `AgentBackend.LLMProviderKey` 只引用 Provider；`ModelRoutes` 当前是 `alias → providerKey` 字符串映射；Claude 的自由文本 `DefaultModel` 又只属于 CLI-native 场景。若继续复用 Provider 单模型字段，Backend、Route 与会话会产生互相冲突的模型来源。
4. **当前 Gateway 与 Runtime 只在 Provider 维度完成了统一。** #39 已将 ProviderKey 贯穿 token 路由与 Claude/Codex 进程重建，但 Gateway 仍以 Provider 的单一 `Model` 重写请求模型，Runtime 的 effective model 也没有会话级稳定 ModelKey。一对多后仅增加数据库字段会导致“选择已保存但真实上游仍使用默认模型”。
5. **远端协议无法表达固定模型。** `wire.RunParams` 只携带 LLMProviderKey，`wire.GoalParams` 连会话级 ProviderKey 都未携带；daemon 只能按本机 Provider 默认模型解析，无法兑现远端 fixed-model，会让 Run 与 Goal 对同一 CLI Session 使用不同启动身份。
6. **远端可用目录与 desktop 本机目录可能不同。** API Key 与 Provider 配置由执行侧本地保存；现有 desktop Provider 列表不能证明目标 daemon 上存在相同 Provider/Model。若 Picker 不感知执行位置，用户可以保存远端必然失败的目标。
7. **失效、停用与删除没有足够的领域区分。** 当前 Provider `status` 同时承担可用性和软删除，且 `DeleteProvider` 不保护 Backend、Session、Route 引用。多模型后需要区分可恢复的停用与不可继续引用的删除，并明确 fixed-model 失效是否允许静默降级。
8. **模型发现目前只是瞬时列表。** `ListModels/PreviewModels` 会实时读取上游目录并用 cago catalog 富化，但不会持久化稳定 ModelKey；直接把发现结果当同步真相会因分页、权限或临时缺失错误覆盖本地配置。
9. **选择 UI 的既有语义已经分裂。** Provider 管理、Backend、Claude Routes 和 Chat 使用不同形状；此前 Mockup 还把 Chat 限定为新会话，与 #39 已有会话常显 Picker、运行中预设下一轮和持久 switch notice 的实际行为冲突。

## Actors and user stories

1. 作为 Provider 管理者，我希望一个 Provider 下维护多个有稳定身份的模型，并明确一个默认模型，以便复用同一组凭证而不复制 Provider。
2. 作为 Agent Backend 配置者，我希望选择 CLI-native、动态跟随 Provider 默认模型或固定模型，以便在自动升级和可重复执行之间主动取舍。
3. 作为已有会话用户，我希望在当前回合运行时预设下一轮的 Provider/Model，而不打断正在输出的内容或影响其他会话。
4. 作为 Claude Code 用户，我希望 OPUS/SONNET/HAIKU 各自继承主绑定、动态跟随 Provider 默认或固定到具体模型。
5. 作为远端 agentred 用户，我希望 Picker 明确告诉我目标是否存在于执行设备，并且只有我确认时才把 API Key 同步过去。
6. 作为维护者，我希望所有执行入口使用同一个 EffectiveLLMConfig，以免 Send、Regenerate、Goal、Gateway 和远端运行出现不同真实上游。
7. 作为审计会话的用户，我希望主动切换目标在 transcript 中留下可读边界，同时全局默认模型变化不会向大量历史会话写入无意义旁白。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | Provider 与 Model 真正拆表，Provider 保存连接配置和 `default_model_key`，Model 保存稳定 `model_key` 与实际 `model_id`。 | Provider 连接与模型生命周期不同。拒绝继续在 Provider 行追加模型数组 JSON，因为无法建立稳定引用、唯一约束和引用保护。 |
| 2 | `model_key` 创建后不可修改；`model_id` 可编辑，有引用时必须展示影响并二次确认。 | 稳定引用不能依赖可修改或可重名的上游 ID。拒绝直接用 Model ID 作跨实体主键，也拒绝完全禁止运维修正 Model ID。 |
| 3 | Provider 与 Model 使用独立 `enabled` 表示可运行状态，既有 `status` 只承担软删除。 | 停用可恢复、删除不可恢复，是两件事，单一 status 无法表达。拒绝把停用伪装成删除。 |
| 4 | ModelTarget 只持久化 `providerKey/modelKey`，不持久化可推导的 mode。 | 避免 mode 与空 key 组合自相矛盾。拒绝保存冗余枚举。 |
| 5 | Backend 支持 `native/provider-default/fixed-model`；Chat 支持 `inherit-agent/provider-default/fixed-model`；Claude Route 支持 `inherit-main/provider-default/fixed-model`。 | 三处复用同一 Picker，但顶部继承来源不同。拒绝为每个场景维护独立选择器和不同模型语义。 |
| 6 | 已有会话延续 #39：运行中可选择、立即原子持久化、当前轮不受影响、下一轮生效。 | 用户明确批准，且当前代码已有 token 路由、进程重建与 notice 基础。拒绝首条消息后锁死，也拒绝立即中断当前轮。 |
| 7 | fixed-model 失效严格阻止下一轮；provider-default 的 Provider 缺失保留 #39 回退 Agent 绑定，但 Provider 存在而默认模型非法时阻止。 | 固定目标承载能力、成本与合规预期，静默换模型更危险；旧 Provider-only 会话需要延续现有兼容行为。拒绝所有失效都无条件回退。 |
| 8 | 所有消费点使用唯一 EffectiveLLMConfig；执行侧解析凭证和实际 ModelID。 | #39 已证明分散读取 Backend ProviderKey 会造成真实上游漂移。拒绝让各 Runtime 单独查询 Provider/Model。 |
| 9 | Gateway token 字符串保持不变，路由目标升级为 ProviderKey+ModelKey。 | token 已烤进长驻 CLI 子进程；换目标不能重签并使当前进程 401。拒绝每轮重签 token。 |
| 10 | Claude/Codex 下一轮按 ProviderKey+ModelKey+解析后 ModelID 判断是否重建，并优先 resume 原 provider_session_id。 | Provider 与启动模型都是启动期身份。拒绝无条件重建，也拒绝模型变化后静默创建全新原生会话。 |
| 11 | Run 与 Goal 的远端 wire 都携带 ProviderKey+ModelKey；固定模型需要 daemon capability。 | 当前 Goal 缺 ProviderKey 已是已知漂移点。拒绝只补 Run 或让旧 daemon 静默忽略 ModelKey。 |
| 12 | 远端 Picker 以目标 daemon 为可运行事实源，并合并 desktop 目录提供显式同步入口。 | desktop 本机配置不代表远端可用。拒绝只显示本机目录，也拒绝选择时自动同步凭证。 |
| 13 | Claude `DefaultModel` 保留为 native 模式的独立自由文本 CLI 字段，不进入 ProviderModel。 | 它描述 Claude CLI 登录态的 `--model`，与 Agentre ProviderModel 不是同一数据源。拒绝强行统一 Claude/Codex/Pi 的本地模型目录。 |
| 14 | Claude Route 在同一 TEXT 列中保存结构化 target；alias 缺失表示 inherit-main。旧字符串数据一次性 migration，运行时不保留旧 parser。 | 功能尚未发布，长期双格式没有用户价值。拒绝为三个固定 alias 增加分散列，也拒绝永久兼容旧字符串。 |
| 15 | 模型发现只是人工导入建议，不自动覆盖、停用或删除本地模型。 | `/models` 可能分页、过滤或短暂缺失。拒绝把一次上游响应当完整同步快照。 |
| 16 | Provider 顶部测试默认模型与 Model 行内测试使用同一测试能力。 | 凭证可用不代表每个模型有权限，但不需要两套测试系统。拒绝只测试目录接口。 |
| 17 | 被引用的 Model 与 Provider 都可以删除，但删除前必须披露引用影响并二次确认；删除后引用保持原样，降级为「目标已失效」。默认模型仍必须先替换。 | 2026-08-19 修订（原文：有引用不可删除、只能停用）。原理由「拒绝删除后让 Backend/Session/Route 静默悬空」不成立：停用本就无条件放行且在所有消费侧产生同一失效态，而悬空并不静默——决策 7 的失效语义已明确承接（fixed-model 严格阻止下一轮、provider-default 回退 Agent 绑定、路由解析失败显式报错）。拒绝：删除时清理引用方——一次删除静默改写多张表多行，用户反而失去按失效提示重选的线索。 |
| 18 | 修改 Provider 默认模型只做影响确认并从下一轮动态生效，不向所有会话批量写 switch notice。 | provider-default 本身承诺动态跟随，批量旁白会污染历史并产生时序问题。拒绝 fan-out 更新会话。 |
| 19 | 最近使用只存本机 localStorage，按执行设备隔离，成功持久化后才记录。 | 它是 UI 偏好而非业务实体。拒绝进入账号同步或存储名称、ModelID、凭据快照。 |
| 20 | Provider/Models/API Key 通过 `llm_provider` 同步对象进入账号；浏览器只见掩码，设备 JWT 可接收明文 Key。 | 账号同步让控制台与已登录设备共享同一份配置。 |
| 21 | 功能尚未发布，不增加 Bundle/Sync V2 兼容层；本地开发数据通过追加 migration 搬迁，旧运行时格式直接退出。 | 用户明确允许破坏未发布格式。拒绝制造无实际消费者的版本兼容债务。 |
| 22 | OpenClaw 保持 Gateway-native 模型语义，只复用基础视觉，不进入 Agentre ModelTarget 数据源。 | OpenClaw 的 agent/model 和认证生命周期由 Gateway 拥有。拒绝把不同所有权的数据伪装成 ProviderModel。 |

## Domain model and lifecycle

一条未删除的 Provider 始终在管理页可见。`enabled=false` 表示它不可被新选择、不会用于执行，但仍可编辑和重新启用；软删除不被引用阻挡：存在有效 Backend、Session 或 Route 引用时需要显式二次确认，删除后这些引用保持原样并降级为「目标已失效」。Provider 启用前必须至少拥有一个启用 Model，且 `default_model_key` 必须指向属于该 Provider 的启用 Model。

一条 Model 使用永久稳定的 `model_key` 承担 Backend、Session 与 Route 引用。`model_id` 是执行时发送给上游的字符串；同一 Provider 内按精确、大小写敏感的 ModelID 去重，不跨 Provider 合并同名模型。用户修改被引用 Model 的 ModelID 时，界面先展示受影响 Backend、Session 与 Route 数量，确认后从下一轮生效，当前回合保持旧配置。

普通 Model 可停用；已保存的 fixed-model 随后保留原 target 并显示失效。默认 Model 不能直接停用或删除，用户必须先指定另一个启用 Model。被引用 Model 禁止删除。无引用 Provider 的删除事务同时移除其 Models，已使用的 ProviderKey/ModelKey 永不复用。

## ModelTarget contract

ModelTarget 的业务身份只有 ProviderKey 与 ModelKey。Backend 中两个 key 都为空表示 native；Session 中都为空表示 inherit-agent；Claude Tier alias 不存在表示 inherit-main。ProviderKey 非空且 ModelKey 为空表示 provider-default，运行时每轮解析 Provider 当前默认模型；两个 key 都非空表示 fixed-model，运行时解析指定 Model 记录。

Backend 主绑定增加稳定 ModelKey。builtin 必须使用 Agentre target；claudecode、codex 与 piagent 可使用 native。claudecode 只接受 anthropic Provider，codex 只接受 openai-response，piagent 接受 anthropic、openai-chat 与 openai-response。Pi Agent 选择 Agentre Provider 后，provider-default 或 fixed-model 都必须最终解析到可用 Model。

Claude native 的 `DefaultModel` 继续只控制 Claude CLI 的自由文本 `--model`。Backend 切换到 Agentre Provider 时保留但忽略该值，切回 native 后恢复使用。会话和 Tier Route 不获得自由输入 CLI-native 模型的能力。

Claude Tier Route 使用结构化对象保存 ProviderKey/ModelKey；缺少 alias 即继承主绑定。服务 DTO 暴露类型化 Route Target，前端不读写原始 JSON 字符串。

## Provider management, discovery and testing

Provider 连接配置与 Model 工作区是两个表面：连接配置负责协议、BaseURL、API Key 与连接状态；Model 工作区负责搜索、默认、启用状态、引用和单模型操作。API Key 在 List/React 状态中继续只返回掩码和是否存在，不把明文暴露给日志、IPC trace 或 React DevTools。

发现模型时，上游结果按同一 Provider 内精确 ModelID 去重并区分“已导入/新发现”。已导入 Model 保持原 ModelKey，不覆盖用户维护的显示名、上下文窗口或最大输出；仅当本地字段为空时允许用 catalog 元数据补齐。本次未返回的本地 Model 不自动停用或删除。用户始终可以手工添加上游未列出的私有、preview 或代理别名模型。

新建 Provider 时，连接记录、用户选中的 Models 与 defaultModelKey 作为一个业务操作成功或失败；已有 Provider 的批量导入同样不能产生半批状态。只有一个候选模型时 UI 可预选默认，多模型时用户明确确认。

测试能力使用明确目标执行真实最小调用：未指定 ModelKey 时测试 Provider 默认模型，指定 ModelKey 时测试该 Model。Provider 顶部“测试连接”和 Model 行内“测试”只是同一能力的不同入口；模型目录拉取成功不等同于实际模型调用成功。

## Backend and Route flow

Backend 编辑器先确定执行位置，再展示与实际 Backend 类型和执行设备兼容的 ModelTarget。新建会话改选 execution target 后，Picker 必须跟随改选 Backend 类型；已有会话使用 Session 已钉住的 Backend，而不是 Agent 主 Backend。

Backend 选择 provider-default 时保存 ProviderKey 和空 ModelKey，界面同时展示当前解析出的生效模型以及“默认变化会从下一轮动态跟随”。选择 fixed-model 时保存完整 key，界面说明 Provider 默认变化不会影响它。切换 native 不清除 Claude 的 native DefaultModel 等类型专属配置。

Claude OPUS、SONNET、HAIKU 每个 tier 使用同一 Picker。顶部特殊项是 inherit-main；每个 Provider 组内先列 provider-default，再列 fixed-model。最终生效路由摘要必须显示 Provider 与实际 Model，不得只显示 Provider 名称。

修改 Provider 默认模型前，界面展示将动态影响的 Backend、Session 与 Route 数量。保存不会改写这些引用，也不会批量插入会话 notice；当前回合继续使用旧模型，下一轮重新解析默认模型。

## New and existing chat flow

新会话 Picker 的顶部特殊项是“跟随 Agent 绑定”。Agent Backend 已绑定 ProviderModel 时展示其解析结果；未绑定的 Claude/Codex/Pi Backend 明确说明将使用 CLI 登录态。新会话的选择保持瞬态，首条 Send 创建 Session 时与 ProviderKey/ModelKey 一起持久化。

已有会话始终渲染同一 Picker。用户选择不同 target 后，后端先根据 Session 实际 Backend 校验 Provider/Model 存在、启用、归属和类型兼容，再以单个原子操作更新 Session 的 ProviderKey 与 ModelKey。选择当前相同组合是 no-op，不写数据库、不追加 notice。前端可乐观更新，但持久化失败必须回滚并展示错误。

运行中允许执行同一切换操作。已经开始的回合保留其启动时 EffectiveLLMConfig，不被 evict、重启、重签 token 或中断；新 target 立即成为 Session 的下一轮配置。普通 Session 收尾更新不得用轮次开始时的旧 ProviderKey/ModelKey 覆盖用户的新选择。

切换成功后 transcript 追加独立持久 `switch` notice，表明从下一轮起使用 fixed model、动态跟随 Provider 默认或跟随 Agent 绑定。notice 继续使用现有 `switch` kind，仅扩展 Provider/Model 负载，以维持 notice-only message、ActiveStream、审批 Overlay 和生成状态的现有透明处理。

Send、Regenerate、Edit、Compact 与 Goal 都使用 Session 当前 target；它们不分别保留不同模型覆盖规则。

## Effective configuration, Gateway and Runtime

执行侧唯一 EffectiveLLMConfig 解析结果包括目标模式、ProviderKey、ModelKey、Provider 类型、实际 ModelID、BaseURL、API Key、上下文窗口与最大输出。展示侧使用同一解析规则但不暴露 API Key。任何消费点不得直接以 Backend ProviderKey、Provider 旧单模型字段或自由文本 ModelID 替代该结果。

Gateway token 字符串保持会话级稳定，token entry 的可变路由目标升级为 ProviderKey+ModelKey。下一轮准备运行时更新 token target，Gateway 在执行侧解析 Provider 凭证和真实 ModelID。fixed-model 必须路由到指定 ModelID；provider-default 必须在本轮解析当前默认。Claude Tier alias 路由同样保存完整 target。

Claude/Codex 的长驻进程以 ProviderKey、ModelKey 和解析后 ModelID 作为启动身份的一部分。目标变化时下一轮 evict 并重建，不变时继续复用。重建优先通过原 provider_session_id resume 上下文；如果真实 CLI 明确不允许在 resume 时切模型，系统返回可理解错误，不静默创建一个看似连续但实际全新的原生会话。Pi Agent 每轮按 EffectiveLLMConfig 组装其 provider/model，不新增不必要的 CLI Session Pool。

LoadSession、上下文窗口展示、用量语义、复制启动命令和消息实际模型元数据都使用同一个解析结果，避免 UI 与真实上游不一致。

## Failure and recovery semantics

inherit-agent 每轮动态解析 Session 实际 Backend；CLI Backend 未绑定 Provider 时保持 CLI 登录态。

provider-default 的 Provider 在执行侧缺失、停用或不兼容时，延续 #39：本轮回退 Agent 绑定、追加 fallback notice，并保留 Session 保存的 ProviderKey，使配置恢复后能够重新生效。Provider 存在但没有合法启用默认模型属于配置损坏，下一轮被阻止，不继续降级。

fixed-model 的 Provider 或 Model 缺失、停用、归属错误、类型不兼容、远端不存在或 daemon 不支持 ModelKey 时，下一轮严格阻止。系统保留原 target，在 Picker 中显示“目标已失效”，不清除 key、不改用 Provider 默认、不回退 Agent。用户修复配置或明确选择新 target 后恢复。

Provider 默认模型或被引用 ModelID 的全局修改只做影响确认和下一轮动态生效，不向所有引用会话 fan-out notice。消息自身继续记录真实执行模型，便于回看。

## Remote execution and credential boundary

远端 Run 与 Goal 请求都携带 ProviderKey 与 ModelKey；wire 不携带 API Key、BaseURL 或 Provider/Model 行正文。daemon 使用自己的本地目录解析目标并返回缺失或 fallback 信号。支持 fixed-model 的 daemon 明确公布 ModelTarget capability；旧 daemon 可继续处理 provider-default，但 fixed-model 在 Picker 中禁用并提示升级，不能静默忽略 ModelKey。

本机执行时 desktop 目录是可运行事实源。远端执行时目标 daemon 目录是可运行事实源；Picker 合并 desktop 目录以展示“本机独有、可同步”的候选，并可以展示 daemon 返回的非敏感远端独有元数据。daemon 离线时保留已保存 target 和其失效提示，但不允许保存新的、未经验证的 fixed-model。

向 daemon 同步 Provider 时，确认界面明确列出 BaseURL、API Key、协议、默认模型和模型目录会被复制到当前设备；用户取消则不发送。同步不经过 agentre-server 业务存储，成功后刷新远端目录，也不自动替用户选择 target。

## Account sync, Server and bundles

Provider、Models、BaseURL 与 API Key 作为一个 `llm_provider` 账号同步对象传播；ProviderKey 是同步标识，模型目录嵌套在载荷中。Agent Backend 身份继续只携带稳定 ProviderKey、ModelKey 和结构化 Route 字符串引用。

agentre-server 以 `sync_objects` 承载 Provider/Model 正文；payload 守卫只对 `llm_provider` 放行 api_key，并拒绝 backend 身份中的 cli_path。浏览器响应不含明文 Key。

该功能尚未发布，Bundle 与同步 DTO 直接切换到新 Provider+Models+defaultModelKey 和类型化 ModelTarget 形状，不增加 V2 或旧格式运行时兼容层。预发布旧 Server 同步数据可以清理并重新同步。

## Migration and compatibility

按照仓库迁移纪律追加 patch migration，不修改已有 migration。迁移在同一事务中创建 Model 表和新引用列、搬迁旧 Provider 单模型数据、转换 Backend/Session/Route 引用，并重建 Provider 表删除旧 model/max_output/context_window 列。

旧 Provider 的 ModelID 非空时生成一条稳定子 Model，搬迁 token 元数据并设为默认，Provider 保持 enabled。旧 Provider 的 ModelID 为空时不伪造模型，Provider 保留为可见但 disabled，defaultModelKey 为空，用户补充默认模型后可重新启用。旧 Backend 与 Session 的 ProviderKey 配空 ModelKey，解释为 provider-default。旧 Claude Route 字符串值转换为对象 target，迁移后运行时只接受新格式。

删除旧 Provider 单模型投影后，编译期和测试必须迫使所有调用方改用 EffectiveLLMConfig 或 Model repository，不允许旧字段继续成为隐含模型来源。

## UI, accessibility and recent targets

统一 ModelTargetPicker 使用搜索、最近使用、Provider 分组、provider-default 首项、fixed model 列表、兼容状态、远端状态和键盘提示。Backend、Chat 与 Claude Route 只替换顶部特殊项，不分叉主体交互。

Picker 和 Provider/Model 工作区覆盖 loading、empty、error、disabled、invalid、远端离线和旧协议状态。禁用原因通过 tooltip 或行内文本表达，不只依赖灰色；支持搜索焦点、方向键、Enter、Esc 和可见 focus ring。布局在 Wails 最小 860×640 窗口与 light/dark 下可用，不引入移动端语义。所有新增静态 UI 文案进入中英文 i18n，动态 Provider/Model/终端内容不翻译。

最近使用只在 localStorage 中保存执行位置、ProviderKey 与 ModelKey，最多五项，并按 local/daemon fingerprint 隔离。native/inherit 特殊项不进入最近；只有 target 成功持久化后记录。当前 Backend 不兼容的项隐藏，失效项禁用；不保存名称、ModelID、API Key，也不进入账号同步。

批准的本地 Mockup 支持以下设计证据（本地 `.dev-kit` 产物，不进入正式规格提交）：

- `.dev-kit/artifacts/2026-08-11-llm-provider-models/mockups/deepseek/index.html?view=chat`
- `.dev-kit/artifacts/2026-08-11-llm-provider-models/mockups/deepseek/index.html?view=chat-invalid`
- `.dev-kit/artifacts/2026-08-11-llm-provider-models/mockups/deepseek/index.html?view=chat-remote`

## Out of scope

- OpenClaw 使用 Agentre ProviderModel。
- agentre-server 托管 Provider、Models、BaseURL 或 API Key。
- 在当前回合中断流并立即切换模型。
- 会话或 Claude Tier Route 自由输入 CLI-native ModelID。
- 选择模型时自动向 daemon 同步凭证。
- 根据一次 `/models` 返回自动停用或删除本地模型。
- fixed-model 失效时静默回退。
- 为未发布旧格式维护长期兼容 parser、Bundle V2 或同步协议 V2。
- 将最近使用升级为跨设备业务数据。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| Provider/Model entity and repository | 稳定 key、唯一约束、enabled/delete 状态、默认模型归属、列表与单列/原子更新；repository 使用 sqlmock | `internal/repository/llm_provider_repo`、`chat_repo/session_test.go` |
| Provider/Model service | 创建、批量导入、默认切换、引用保护、ModelID 影响确认、测试默认/指定模型；service 注入 mock repository，不连接数据库 | `internal/service/llm_provider_svc/llm_provider_test.go` |
| Backend service/entity | 各 Backend target 模式与 Provider 类型兼容；Pi Agent 的 provider-default/fixed 都需解析到模型；Claude native DefaultModel 边界；类型化 Routes | `agent_backend_entity/*_test.go`、`agent_backend_svc/agent_backend_test.go` |
| Chat service | 新建与已有会话三态、完整组合 no-op、运行中切换只影响下一轮、原子持久化、失效/回退、switch/fallback notice、Send/Regenerate/Edit/Compact/Goal 同源 | `chat_svc/provider_key_test.go`、`provider_key_internal_test.go`、`chat_internal_test.go` |
| Chat repository | ProviderKey+ModelKey 同语句更新，普通 Save 不覆盖轮中切换 | `chat_repo/session_test.go` |
| EffectiveLLMConfig | Backend、Session、Provider 默认与 fixed model 的唯一优先级；展示与执行解析一致 | #39 的 `session_provider.go` 与相关内部测试 |
| HTTP Gateway | token 字符串不变而 Provider/Model 路由更新；主模型和 Claude tier route 使用真实 ModelID；缺失/停用错误 | `token_registry_test.go`、`llmforward_test.go` |
| Claude/Codex/Pi Runtime | target 变化触发下一轮重建，未变继续复用；native DefaultModel 边界；Pi 每轮 materialize 指定模型 | `runtimes/claudecode/*_test.go`、`runtimes/codex/*_test.go`、`runtimes/piagent/*_test.go` |
| Remote wire and daemon | Run/Goal 都携带 key；capability gate；远端 ready/missing/offline/old protocol；凭证不进 wire | `internal/pkg/agentruntime/runtimes/remote`、`internal/daemon/handlers` tests |
| Account sync and server payload guard | Backend 同步只带字符串引用；model_key 被允许；api_key 与 Provider 行正文继续拒绝 | desktop `syncwire` guard tests、server `sync_entity/payload_test.go` |
| Import/export | 新 Bundle shape 保留 Provider/Models/default target，旧未发布 shape 明确拒绝 | `internal/service/data_svc` tests |
| Frontend Provider/Model management | 搜索、发现导入、默认/引用保护、停用/删除、默认与单 Model 测试、light/dark/860px | `llm-providers` Vitest 与批准 Mockup |
| Shared ModelTargetPicker | 三个顶部特殊项、provider-default/fixed、最近使用设备隔离、兼容过滤、loading/empty/error/invalid、键盘与 i18n | `provider-pill`、`session-exec-target`、Backend UI tests |
| Transcript projection | 扩展 switch payload 仍被判定为旁白行，不污染 last assistant、ActiveStream、审批和生成指示 | `notice-message`、`transcript-row-view`、chat internal tests |

真实 Claude Code/Codex 在 resume 时更换 Provider/Model 是否保持原生上下文依赖真实 CLI 与供应商凭证，不能由 mock 单元测试充分证明。收尾阶段必须在本机和至少一个远端 agentred 场景观察：当前轮不中断、下一轮进程按目标变化重建、请求命中新上游、上下文连续或得到明确不支持错误，并按 `docs/verification.md` 留下证据。数据库迁移按项目规则不增加独立 migration 单元测试；使用包含旧 Provider、Backend、Session 与 Route 行的真实数据库副本验证数据搬迁和回滚边界。

## Links

- [已有会话切换 LLM 供应商](./2026-08-10-session-provider-switch.md)
- [新建会话选择 LLM 供应商](./2026-08-09-new-session-provider-select.md)
- [Agent backend](../agent-backend.md)
- [Session lifecycle](../session-lifecycle.md)
- [Frontend conventions](../frontend.md)
- [Design system](../design.md)
- [Testing](../testing.md)
- [Verification](../verification.md)

## Open questions

无。
