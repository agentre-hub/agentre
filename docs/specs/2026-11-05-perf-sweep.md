# 性能整治（2026-11 扫描批）

> Status: Approved
> Owner: agentre 桌面端 + agentred
> Last updated: 2026-11-05

**Objective:** 消掉 2026-11-05 四路只读扫描确认的性能问题：流式输出按设计的节奏合帧、长轮次与长会话不再随长度二次方变慢、启动与切会话不再被一次性的大扫描或子进程卡住、后台轮询与远程连接不做重复的昂贵工作。

**Hard invariant:** 转录的可见内容、顺序与落库结果（消息 / 块行的最终内容）与改动前一致；不改 agentre ↔ agentred 协议（`pkg/wire` 版本不变）、不改 `pkg/syncwire`；不新增迁移改动已有迁移；现有测试与 `make lint` 全绿。

## Problem

证据来自 2026-11-05 的四路只读扫描（流式热路径 / 持久化 / 前端渲染 / 远程与后台），其中标「抽查核实」的由主会话对照源码复核过。库体量取自用户本机库的只读快照：3.0 GB、155 万块行、4.15 万消息、4700 会话。

1. **前端 90ms 文字合帧从未生效。** 宿主每个 `chunk` / `thinking` 先调 `clearLiveRetry`，它走 `flushingSet`，即使 `liveRetry` 本来就是 null 也先把缓冲刷进 store（`frontend/src/components/agentre/chat-streams-host.tsx:118`、`frontend/src/stores/chat-streams-store.ts:246`）。store 按 chunk 频率提交，下面 D 组的重渲染成本全被放大。（抽查核实）
2. **Go 端文字合帧器把每批第一段压到下一个事件才发。** 新批 `since = now`，是否到期只在下一个 delta 到来时判断，没有定时器（`internal/service/chat_svc/coalescing_emitter.go:93`）；停顿前的最后一段一直不可见，慢模型成对蹦字。现有测试 `TestCoalescingEmitter_FlushesOnceIntervalElapsed` 断言的正是这个错误行为。（抽查核实）
3. **轮内每次 checkpoint 全量重编码整条 assistant 消息。** `SetBlocks(acc.Snapshot())` 全量 marshal，`DiffBlocks` 再把新旧两份全量 JSON 反解比较（`internal/service/chat_svc/dispatcher_runtime.go:16`、`internal/model/entity/transcript_entity/message_block.go:199`），每个 ToolResult 一次，在持 `t.mu` 的事件循环上跑；一轮成本 O(N²)。轮末与 steering 再用 `replaceBlocks` 删掉整条消息的块行全量重插（`internal/repository/transcript_repo/message.go:308`）。
4. **每轮收尾把整个会话的工具历史至少读两遍。** `LoadSessionBlocksByType` 每次 reload 读全会话（`internal/service/chat_svc/read_path.go:126`，前端 `use-chat-session.ts:473` 每轮收尾触发），`SessionWrittenPaths` 经 `WorkRoots` 再读一遍（`internal/service/chat_svc/written_paths.go:37`）。真实会话一次 7–13k 块、5–10 MB，SQL 本身 0.3–1.3 s，再过 Wails 桥。
5. **启动时同步扫全库 subagent 卡。** `SettleOrphanedAgrctlCards` 在 `registerChatService()` 之前同步执行（`internal/app/app.go:241`、`:129`），本机库冷读 23–26 s；这期间 `chat_svc.Chat()` 为 nil，UI 调到聊天绑定只会拿到错误。（抽查核实）
6. **导入本地会话在整段回放期间持有 SQLite 写锁。** 一个 `RunInTx` 包住 `tr.Turns(...)`，远程读每 8 轮一次 RPC（`internal/service/chat_import_svc/import.go:104`）；其它会话的轮末写入等满 5 s busy 后失败，而 `turn_run.go:561`、`autonomous_turn_run.go:637` 用 `_ = …Update(...)` 丢掉错误——轮末 assistant 消息会无声丢失。（抽查核实）
7. **斜杠命令列表每次挂载 composer 都起一次 `claude plugin list`。** `ListAgentSkillPacksForTarget` 的 refresh 参数被忽略（`internal/service/skill_svc/skill.go:147`），无缓存；实测每次 3.0–3.3 s，远程会话在远端起同样的 CLI。（抽查核实）
8. **会话进程轮询每 5 s 每个可见面板跑一次全量进程扫描 + `lsof`。** `lsof -iTCP -sTCP:LISTEN` 单次 0.6–1.1 s，分屏按面板数翻倍（`internal/service/chat_svc/session_process.go:120`、`internal/pkg/procscan/os_darwin.go:28`）。
9. **每轮 claude 收尾前遍历整个 `~/.claude/projects` 找 `<sid>.jsonl`。** 本机 4.6k 文件，0.2–0.7 s，随历史增长（`pkg/claudecode/jsonl.go:76`）。
10. **piagent 每行 stdout 处理 6–8 遍**，`message_update` 行随整条回复增长，诊断净化无论日志级别都跑（`pkg/piagent/stream.go:241-367`、`pkg/piagent/client.go:514`）。**claudecode 读循环每行解码两次**（`pkg/claudecode/session.go:356`）。
11. **对端（web 控制台）挂上后每个 token 单独发一帧，会话帧历史永不释放**（`internal/service/chat_svc/peerstream/history.go:93,145,362,454`）。
12. **前端每次流式更新重渲染整个 ChatPanel**：内联 props 让 composer 的 `memo` 失效，TipTap 每次 `setOptions`（`frontend/src/components/agentre/chat-panel.tsx:163`、`frontend/packages/agentre-ui/src/chat-input/index.tsx:239`）；会话级派生（变更、后台任务、任务进度）每个文字 delta 全量重扫（`chat-panel.tsx:323,418,426`、`chat-context-sidebar/index.tsx:88`）；隐藏 tab 照样随自己会话的 delta 重渲染和布局（`chat-tabs/chat-panel-host.tsx:583`）。
13. **启动时把所有恢复的 tab 全量挂载**，每个各发 5+ 个请求（含第 7 条的技能扫描）（`frontend/src/stores/chat-tabs-store.ts:381`、`chat-tabs/chat-panel-host.tsx:113`）。
14. **启动包是单个 3.8 MB 文件**，只有 Monaco 拆出去了。**侧栏任一会话状态变化重建全部分组**（`session-index/index-group-row.tsx:135`）。**可见消息观察器每次 DOM 变化全量 `querySelectorAll`**（`frontend/src/hooks/use-visible-message-id.ts:70`）。
15. **远程连接**：池连接空闲 30 s 就关、watcher 另持一条，冷借用要重走 keychain + 拨号 + 握手；并发冷借用各拨一次（`internal/service/remote_device_svc/conn_pool.go:127,328`）。**中继路径拨号没有期限**，服务端不可达时 `Borrow` 永久挂起（`internal/service/server_svc/relayclient.go:142`）。**watcher 每 5 s 每设备写一次 `last_seen_at`**（`internal/bootstrap/remote_device_watcher.go:229`）。**空闲终端每秒唤醒 100 次**（`internal/service/terminal_svc/service.go:479`）。
16. **库空间不回收。** `auto_vacuum=2` 但从不 `incremental_vacuum`，空闲页约 556 MB；无 `journal_size_limit`，`-wal` 停在 1.38 GB 不缩（`internal/bootstrap/cago.go:393`）。

## Actors and user stories

1. 作为看 agent 流式输出的用户，我想文字按稳定节奏出现、停顿前的最后一段马上可见，输入框在 agent 输出时也不卡。
2. 作为跑长轮次 / 长会话的用户，我想第 800 次工具调用和第 10 次一样顺，轮次结束不再卡一下。
3. 作为打开应用、切换会话的用户，我想聊天立即可用、`/` 菜单立即有内容。
4. 作为同时开多个会话 / 用远程设备的用户，我想后台轮询和远程连接不拖慢本机、远程操作不会永远转圈、并发会话的结果不会无声丢失。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 一个整治轮、一个分支，按领域切成写路径不相交的任务，由 subagent 并行实现；每项先写失败测试 / 基准再改 | 用户要求「派发 subagent 处理所有问题」（2026-11-05）。Rejected: 每项一个 PR——30 项过碎 |
| 2 | Go 合帧器：距本流上次发送 ≥16ms 的 delta 立即发；否则挂一个每流定时器发尾巴；每流串行，非文字事件不能越过定时器的那次发送 | 修正第 2 条且保持合帧收益。Rejected: 删掉合帧——回到每 token 一次 webview 求值 |
| 3 | checkpoint 只编码新增与被改动的块，直接把这些块交给仓库 upsert；轮末与 steering 走同一增量路径，`replaceBlocks` 只留给此前从未落库的消息 | 块是追加为主，改动可在累加器里标脏。Rejected: 只缓存上次的解码结果——仍要全量 marshal |
| 4 | 收尾 reload 改增量：前端带上已持有的游标，后端只返回游标之后的消息与刚收尾的那条；写过的路径随块写入增量维护或按会话转录版本缓存 | 绝大多数数据自上轮未变。Rejected: 只在后端加缓存——仍过桥 10 MB |
| 5 | 启动清理 agrctl 孤儿卡挪到 `registerChatService` 之后的后台 goroutine；查询范围不收窄 | 卡被收成已取消晚几秒对用户无可见差别；收窄范围要先证明「父轮已结束的卡不可能仍在跑」，本轮不做。Rejected: 收窄到本次重置的会话——语义未核实 |
| 6 | 导入：先在事务外读完 / 拉完并回放成消息列表，再用一个短事务写入；导入仍是全有或全无 | 写锁只覆盖写库本身。Rejected: 每页一个事务——要引入「导入中」中间态与失败清理 |
| 7 | 轮末 / 中止 / 出错路径的落库失败一律记 Error 日志（带 sessionId、assistantMsgId），不再 `_ =` 丢弃；不加重试 | 按 `docs/observability.md` 的必埋点；根因（长写锁）由决策 6 消除。Rejected: 加重试——掩盖锁持有方的问题 |
| 8 | 技能发现按（机器、后端类型、CLI 路径）缓存；并发请求合并成一次；插件注册文件 mtime 变化或 ctlskill 安装变化时失效；兜底 TTL 5 分钟；`refresh=true` 强制重扫 | 新装插件最晚在下次失效或 5 分钟内出现，设置页手动刷新立即生效。Rejected: 不设 TTL——漏掉无法观测 mtime 的远程变更 |
| 9 | 会话进程：轮询节奏与胶囊行为不变；一次进程快照在 ~5 s 内跨会话 / 面板共享；`lsof` 只查带会话标记的候选 PID | 胶囊需要在面板可见时持续知道有无进程（`docs/specs/2026-11-03-session-process-ports.md` 决策 9）。Rejected: 只在弹层打开时扫——胶囊不知道该不该出现 |
| 10 | 恢复的 tab 首次被激活时才挂载面板，之后保持挂载；tab 条上的状态 / 注意力标记不依赖面板挂载（它们来自全局流宿主与 store） | 已核实：`chat-streams-host` 是全局宿主，流状态按会话存 store。Rejected: 限制恢复的 tab 数——改变用户的 tab 集合 |
| 11 | 非活动 tab 只订阅「是否在跑」，不随文字 delta 重渲染；切回时内容是最新的、滚动位置与离开时一致 | 维持既有切 tab 恢复修复的行为。Rejected: `content-visibility:hidden`——与已修的滚动恢复交互未验证 |
| 12 | 共享包 `@agentre-hub/agentre-ui` 里的 composer 编辑器选项 memo 化，本轮只在 agentre 提交；agentre-server 抬钉另起 | 跨仓顺序：agentre 先提交推送，server 后钉（`AGENTS.md`）。Rejected: 同轮改 server——本轮不改协议，server 无须配对 |
| 13 | 远程池：并发冷借用合并成一次拨号；watcher 报告设备在线期间池连接不因空闲关闭；中继路径拨号阶段有期限（与 watcher 的 15 s 一致） | 消除冷连接与永久挂起。Rejected: 把 watcher 并进池连接——改动面大，本轮只做保温 |
| 14 | watcher 的 `last_seen_at` 最多每 60 s 落库一次，在线 / 错误状态变化时立即落库 | 在线判定窗口是 5 分钟，60 s 粒度足够。Rejected: 只存内存——重启后丢失最近在线时间 |
| 15 | 库连接加 `journal_size_limit`（64 MiB）；空闲时与删除会话后分批 `incremental_vacuum` | 回收已存在的 556 MB 空闲页与 WAL 高水位。Rejected: 启动时 `VACUUM`——3 GB 库长时间独占 |

## 流式输出

- **前提**：一条流正在输出文字 / 思考 delta。
- **前端**：只有当前确实显示着重试提示时，新 delta 才清除它；否则 delta 进入 90ms 合帧窗口，store 在一个窗口内最多提交「首个 + 尾巴」两次。
- **Go 端**：一条 delta 距本流上次发送已 ≥16ms 时立即发出；否则在 16ms 内由定时器发出。任何 delta 不会因为后续没有事件而一直不可见。非文字事件之前攒下的文字先发出，顺序与逐帧发送一致。
- **对端发布**：挂了 web 控制台时，预览文字帧按同样节奏合帧；会话的最后一个订阅者离开后，该会话的发布历史释放。
- **CLI 读取**：claudecode 每行 stdout 只解码一次；piagent 每行只做一次主解码，诊断净化只在调试日志开启时执行；Pi 普通工具的执行进度帧不做完整解码。
- **失败行为**：合帧定时器在流结束 / 中止时先发出尾巴再发终态，不丢字。

## 长轮次与长会话

- **轮内 checkpoint**：在含 N 个块的消息上追加一个工具调用后 checkpoint，编码与写入的块数与 N 无关（只含新增与被改动的块）。落库结果与改动前逐块一致。
- **轮末 / steering**：只写入相对已落库版本发生变化的块，不再整条删除重插；此前从未落库的消息照旧整条写入。
- **轮末落库失败**：记 Error 日志（含 sessionId、assistantMsgId、错误），界面行为不变。
- **收尾 reload**：前端只取上次之后新增或刚收尾的消息并合进已有数据；合并后的派生视图（变更列表、后台任务、任务进度）与全量重读结果一致。工作根的「写过的路径」不再每轮全量读块。
- **claude 收尾**：查找会话 JSONL 先试 cwd 推导的项目目录，同一会话第二次查找不再遍历目录；找不到时退回全量遍历。

## 启动与切换会话

- **启动**：聊天服务在启动清理之前就绪；孤儿 agrctl 卡的收尾在后台完成，失败只记日志。
- **恢复的 tab**：只有活动 tab 立即挂载并发请求；其它 tab 首次点开时才挂载，之后保持挂载。tab 条状态、注意力标记、后台会话的流式状态照常更新。
- **技能 / 斜杠命令**：同一机器与后端的第二次请求在缓存有效期内不起子进程；并发请求只起一次；插件注册变化或设置页手动刷新后返回新结果。远程会话同理（缓存在桌面端按设备区分）。
- **启动包**：非聊天路由、设置对话框、命令面板、终端面板按需加载；首屏聊天视图不受影响，按需加载失败时显示现有的错误边界。

## 渲染

- 流式更新只重渲染转录区与依赖流状态的头部元素；composer（含编辑器）在 agent 输出期间不因流更新重渲染或重设编辑器选项。
- 会话级派生（变更、后台任务、任务进度）只在块集合变化时重算，文字 delta 不触发。
- 非活动 tab 不随自己会话的文字 delta 重渲染；切回时内容最新、滚动位置不变。
- 侧栏一个会话的状态变化只重渲染该会话所在的行。
- 可见消息观察器的开销与转录总长度无关。

## 后台与远程

- **会话进程**：多个面板 / 会话在 5 s 内的列表请求共用一次进程快照；端口查询只针对候选进程。列出结果与改动前一致。
- **远程池**：同一设备的并发冷借用只拨一次；设备在线期间池连接保温；中继拨号在期限内失败返回错误，界面显示现有的失败态而不是一直转圈。
- **watcher**：`last_seen_at` 最多每 60 s 落库一次，状态变化立即落库；在线判定行为不变。
- **终端**：没有待发输出时不唤醒。
- **导入**：导入期间其它会话的写入不被阻塞超过一次正常写事务的时间；导入失败时库中不留半截会话。
- **库空间**：WAL 文件在检查点后不超过 64 MiB；空闲时与删除会话后逐步回收空闲页，回收不阻塞正常写入超过一次短事务。

## Out of scope

- 中继大帧队头阻塞（分块 + 流控）与中继帧多余拷贝：涉及 `pkg/wire` 共享模块与 agentre-server 配对，另开一轮。
- 轮末 `StreamDone` 事件携带整条消息：前端与对端都依赖它对账，收益低于契约改动风险，另议。
- 收窄 agrctl 孤儿卡的扫描范围、`subagent_state` 块改为不压缩存储。
- agentre-server 抬钉 `@agentre-hub/agentre-ui`：本轮推送后另起。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `chat-streams-store` + 宿主顺序（vitest 假时钟） | 宿主调用顺序下一个窗口内最多两次 store 通知 | `frontend/src/stores/__tests__/chat-streams-store.test.ts` |
| `coalescingEmitter`（注入时钟 / 定时器） | 单个 delta 无后续事件也在 16ms 内发出；终态前先发尾巴 | `internal/service/chat_svc/coalescing_emitter_test.go`（修正错误断言） |
| checkpoint 基准 + 计数编码钩子 | 追加 1 块后编码块数 ≤ 常数；B/op 不随 N 增长 | 无（新增） |
| `transcript_repo` sqlmock | 轮末走增量 upsert，不出现无条件 `DELETE … WHERE message_id = ?` | `transcript_repo` 既有 sqlmock 测试 |
| chat_svc mockgen 服务测试 | 增量读只取游标之后；写过路径不重复全量读；轮末落库失败记日志 | chat_svc 既有 mock 测试 |
| app 启动测试（慢 settle 假实现） | `Startup` 返回时 `chat_svc.Chat()` 非 nil | `internal/app` 既有 seam `settleOrphanedAgrctlCards` |
| chat_import_svc 假 TxRunner + 假网关 | `Turns` 不在 `RunInTx` 内被调用；失败不留半截 | chat_import_svc 既有测试 |
| skill_svc / 发现器计数 runner | 第二次、并发请求只起一次；失效与 refresh 生效 | 无（新增） |
| procscan 计数假 OS | TTL 内两次不同会话列表只快照一次；lsof 带 PID 过滤 | `use-session-processes.test.ts`、procscan 既有测试 |
| piagent / claudecode 基准 | 每行分配 ≤ ~1.5× 行长；单次解码 | `pkg/piagent/frame_size_test.go` |
| `findSessionJSONL` 计数 fs | 同会话第二次查找 0 次 ReadDir | 无（新增） |
| conn_pool / relayclient 假拨号 | 并发冷借用拨一次；在线保温不重拨；中继阻塞时 `Borrow` 在期限内返回 | `conn_pool` 既有测试 |
| watcher 假时钟 | 12 次心跳 ≤2 次落库，状态变化立即落库 | watcher 既有测试 |
| 前端 RTL 渲染计数 | composer / 派生函数 / 非活动 tab / 侧栏行在流更新下不重渲染；恢复 tab 只请求活动 tab | 既有 ChatPanel / tabs 测试 |
| `sqliteDSN` 单测 | DSN 含 `journal_size_limit` | `internal/bootstrap` 既有测试 |

不能自动化的：真实 3 GB 库上的启动耗时、WAL 实际缩小、真实 CLI 的 `/` 菜单耗时、长会话流式手感——由收尾的运行时验证在隔离验证台上观测（用合成数据，不拷真实对话）。

## Open questions
