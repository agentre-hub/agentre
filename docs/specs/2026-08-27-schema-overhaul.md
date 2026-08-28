# 会话块存储重构与三套 schema 的命名对齐

> Status: Approved
> Owner: 桌面端 chat 域 / sync 域，server sync 域
> Last updated: 2026-08-27

**Objective:** 一轮内完成两件事——把 `chat_messages` 的整轮 JSON blob 拆成一块一行、可索引、可压缩的 `chat_message_blocks`；并让同一个概念在桌面端库、agentred 库、server 库与三者之间的线格式上叫同一个名字、用同一种表示。

**Hard invariant:** 转录呈现、subagent 卡片状态、token 统计、fork/regenerate 的截断语义、导入回放结果、同步的胜负判定与冲突处理、回收窗口、设备解析、会话状态流转，在改动前后**完全一致**；服务层现有的 `Message.GetBlocks()` / `SetBlocks()` 契约不变。本轮有三处**刻意的行为变化**，各自由决策载明：决策 20（HTTP 同步契约的墓碑字段由布尔改为时刻）、决策 23（会话整行回写不再能触碰运行态列，`context_window` 因此停止被拍回旧值）、决策 24（无人引用的后端墓碑会被回收）。除这三处外行为完全一致。

## Problem

### A. 块存储

以下数字来自 2026-08-27 对本机真实库（`AppDataDir/agentre.db`，2.25 GB）所做的实测快照，非估算。

1. **整个数据库就是一张 `chat_messages`。** `dbstat` 显示它占 2143 MB / 全库 2251 MB，其余所有表加索引不足 10 MB。`auto_vacuum=0`，删除任何数据都不归还磁盘。

2. **一行 = 一整轮，正文全部内联。** 24,702 条消息共 885,682 个块（均值 35.9 块/条）。assistant 行均值 152 KB，单行最大 12.9 MB、内含 565 对 `tool_use`/`tool_result`。重尾：426 行（1.7%）占 836 MB（42%），3,671 行（15%）占 1,771 MB（89%）。

3. **读侧把整条会话搬到前端。** `LoadSession`（`internal/service/chat_svc/chat.go:616`）走无分页的 `Message().List(sessionID)` 取全列全量，再由 `toChatMessage`（同文件 `:875`）逐条解码后整包过 Wails 桥。最重的会话 26.2 MB / 8,733 块；3,339 条有消息的会话里 492 条 >1 MB、61 条 >5 MB、16 条 >10 MB。`List` 另有 7 处调用方（如 `written_paths.go:36`、`plan_action.go:156`）只需要其中极小一部分，同样付全量代价。

4. **块定位靠全 blob 串匹配。** `FindAssistantBySubagentToolUseID` 与 `rewriteSubagentMessage`（`internal/repository/chat_repo/message.go:333`、`:402`）用 `blocks_json LIKE '%toolUseID%'` 在该会话所有大 blob 上逐行匹配；`PatchSubagentProgress` 命中后是整行读-改-写。`MessageRepo` 里有五个方法存在的唯一理由就是块数组不可寻址。

5. **写放大已被局部治过，根因仍在。** `9070ce2d`（2026-08-26）把 usage/error 改成窄列写，理由正是「为存 6 个 token 计数重写 MB 级 blocks_json」。`subagent_activity.go:128` 那句「没变化就不写 —— 发起消息 blocks_json 动辄几百 KB」是绕过而非解决。

6. **前端三处派生视图依赖完整历史**，使「截断历史」式的分页会静默算错：`background-tasks/derive.ts:69`、`chat-context-sidebar/derive.ts:62` 的 `deriveOutline`、`chat-panel-context-usage.ts:17`。

7. **`chat_messages` 被当控制面用。** pi 转录替换的恢复标记借 `role` 哨兵（`__agentre_pi_recovery__`）、负数 `session_id` 命名空间、`device_id` 装会话 id（`chat_repo/message.go:183`、`:799`）、`model` 装状态机，靠 `ParseReplacementRecoveryMarker` 一组校验兜底。为它建的 `idx_chat_messages_recovery(role, device_id)` 占 712 KB——比正牌的 `idx_chat_messages_session_seq`（328 KB）还大——却只服务一个哨兵 role。该表当前**0 行**。本轮删掉 `blocks_json`（它的 payload 所在列）并给 `device_id` 改名（它的查找键所在列），两根支柱同时消失。

### B. 命名

三套 schema 在同一个工作区里演进，其中 agentred 与桌面端还在**同一个仓库**内。对全部表做了列名清点（桌面端 24 张、agentred 2 张、server 15 张）。

8. **时间戳有两套命名，且分歧在同一个仓库内部。** 桌面端 `createtime`(16 表) / `updatetime`(18 表)，server `createtime`(12 表) / `updatetime`(10 表)，两者一致；唯独 agentred 用 `created_at` / `updated_at`。

9. **「会话最后活动时刻」两个名字，其中一个正在惹祸。** 桌面端 `chat_sessions.last_message_at`；server `mirror_session_summaries.updated_at`，注释明确写着这是「the peer's own record of this session's last activity」。因为叫 `UpdatedAt`，GORM 把它认作自动更新字段，代码不得不写 `autoUpdateTime:false` 并配守卫测试 `TestSessionSummaryEntity_PlainUpdatesNeverRewritesUpdatedAt`——而这一列是索引排序键、分页游标的一半、未读判据的一边。同表里它还与行更新时刻 `updatetime` 并列。

10. **「发起端本地会话 id」两个名字，且与桌面端同名异义。** agentred 叫 `peer_session_id`，server 叫 `session_id`（4 张表）；而桌面端的 `session_id`（`chat_messages`、`issues`）指的是**本机** `chat_sessions.id`。

11. **同一批同步字段有三套叫法。**

    | 概念 | 桌面端库 | server 库 | HTTP 契约 |
    |---|---|---|---|
    | 归属账号 | `sync_account_id` | `user_id` | —— |
    | 最后修改来自哪台设备 | `sync_origin`（text） | `source_device_id`（bigint） | —— |
    | 版本 | `sync_version` | `version` | `version` / `base_version` |
    | 墓碑 | `sync_deleted_at`（时刻） | `deleted_at`（时刻） | `deleted`（bool） |

    「来源设备」在边界上被转了一道：`sync_svc/downlink.go:306` 写的是 `SyncOrigin: strconv.FormatInt(in.SourceDeviceID, 10)`——两个名字装同一个值。

12. **`sync_origin` 的注释与实现不符。** `syncmeta_entity/syncmeta.go:37` 声称它供「决策 4 的字典序打破平局」使用，但全仓对它只有四处引用（两处写入、一处落库、一处声明），**没有任何地方比较过它**。

13. **机器指纹在桌面端有两个名字，且都过度限定。** agentred 实例的指纹，在 `paired_agentreds` / `project_locations` 叫 `daemon_fingerprint`，在 `agent_backend_cli_overlays` / `sync_lost_changes` 叫 `agentred_fingerprint`（server 侧也叫后者）。

14. **`agent_backends.device_id` 名实不符，且已扩散。** 该列实际存放 agentred 指纹（`agent_backend_repo/agent_backend.go:184`）。误名已随 `AgentBackend.device_id` 进入线格式（`protowire/run.go:124`），并顺着 `chat.go:2956`、`:4198` 的 `DeviceID: be.DeviceID` 流进 `chat_messages.device_id`——同一个值在两张表和一个协议字段上用同一个错名。

15. **agentred 的 `updated_at` 兼任两个角色。** `session_repo/session.go:122` 每次 `Upsert` 写 `s.UpdatedAt = now`（行更新时刻），而 `protobuf_registry.go:345` 把这同一列喂进 wire 的 `SessionSummary.updated_at`，server 当「会话最后活动时刻」持久化。今天能对上只因为 `Upsert` 恰好每轮起手调用一次；任何一次非活动性的 upsert 都会顶掉「最后活动」，打乱 server 侧的会话排序与未读判据。agentred 库里没有第二列承担行更新时刻，也没有任何读取方把它当行更新时刻用。

16. **表名有三处分歧，其中一处是把不变量写进了名字。**

    其一，agentred 用**二进制名**做表前缀（`daemon_sessions` / `daemon_notification_logs`），而桌面端与 server 一律用**域**做前缀；且 `daemon` 与产物名 `agentred` 对不上。

    其二，同一行东西三个词：wire 叫 `JournaledNotification`（`wire.proto:314`），agentred 表叫 `notification_logs`，server 表叫 `journal_frames`。

    其三，server 用**两个实现比喻**给同一个域起名——`followed_*`（1 张）与 `mirror_*`（3 张）——而产品词是第三个：`router.go:61` 的变量叫 `savedSessionCtr`、`mirror_ctr` 的方法叫 `SavedSessions`、`architecture.md:180` 写 "saved-session"、server 自己的 web 前端通篇 `row.saved` / `onSave` / 「已保存的对话」。且 `mirror` 作为域名不成立：server 会把删除**反向推回对端**（`mirror_svc/sessions.go:126`），并持有源头没有的状态（`last_read_at` + `/v1/mirror/sessions/read`）——镜像不写回它所镜像的源，也不会有自己的状态。

17. **Go 侧的工具调用标识与线格式对不上。** 线格式统一用 `tool_call_id` / `parent_tool_call_id`（`wire.proto:787`、`:790`），桌面端仓储层的方法与参数却叫 `toolUseID`。

### C. 写入路径与生命周期

18. **会话整行回写的 `Omit` 清单是手工维护的，且已经漏了一个。** `chat_repo/session.go:501` 的 `Save` 挂着 9 列 `Omit` 与一段 20 行注释，逐列记着漏掉它会出什么事（抹掉 `exec_*` 导致远端会话进不了启动补齐、抹掉 `cwd` 导致续跑当场断掉、抹掉 `provider_key`/`model_key` 冲掉用户轮中切好的模型）。该清单的实际含义是「有专用窄更新方法的列」，而窄方法有 6 个——`context_window` 有 `UpdateContextWindow`（运行时轮中上报）却**不在清单里**。注释自己写明收尾用的实体是「轮次开始时读出的那一份」，因此每轮收尾的整行 `Save` 都把轮中上报的上下文窗口拍回旧值。

19. **软删墓碑没有任何回收，悬空引用只增不减。** `agent_backends` 240 行里仅 4 行 ACTIVE，其余 236 条是墓碑；`chat_sessions` 里 137 条指向已软删的 agent。墓碑的成因是扫描-创建-删除循环：`ScanAndCreateAgentBackends` 的撞名判据只看 ACTIVE 行（`scan_create.go:17-21`、`:59`），于是每轮「扫描建 → 被删 → 再扫描建」都留下一条新墓碑——实测 `Claude Code CLI` / `Codex CLI` / `Pi Agent CLI` 各 47 条且 `createtime` 完全相同。引用完整性由 service 校验而非外键，却没有配套的回收或巡检。

20. **一处种子行的时间单位与全仓不一致。** 全仓统一 `UnixMilli`，唯独 `202608080004` 的 CEO agent 种子用 `strftime('%s','now')`（秒），同期 `202608080010` 的标签种子是对的（`*1000`）。那一行的 `createtime` 现在被解释成 1970 年。

21. **两处小的记录缺陷。** `issues` 没有 `session_id` / `assignee_agent_id` 索引（现仅 1 行，但按会话反查工单没有索引可用）；`202608080001` 的迁移注释列了 `model` / `max_output` / `context_window` 三个字段，而表里没有它们（已下沉到 `llm_provider_models`）。

## Actors and user stories

1. 作为**桌面端用户**，我希望打开一条长会话时不再等待整条历史传输完成，也不希望它长期占住内存。
2. 作为**桌面端用户**，我希望后台 subagent 卡片的进度更新不再拖慢正在进行的轮次。
3. 作为**桌面端用户**，我希望应用的数据目录不会因为正常使用而无上限膨胀。
4. 作为**维护者**，我希望定位一个块是索引点查而不是全表串匹配，从而不必再为每种块级操作新增一个专用仓储方法。
5. 作为**维护者**，我希望在一侧读到一个字段名后，不需要再去查它在另外两侧叫什么、是什么类型。
6. 作为**维护者**，我希望列名就是它装的东西，不会再把本机指纹当成远端设备 id、把「对话最后活动」当成「行更新时刻」、或在一个指纹列里读到会话 id。

## Design decisions

### 块存储

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 块表建成**一块一行**：`chat_message_blocks(message_id, idx, type, tool_call_id, codec, data)` | 定位键成为普通列，五个块级仓储方法退化为普通行读写；`type` 列同时让「按类型取块」成立。Rejected：**一条消息一行**只存整个数组—— `LIKE` 全扫原样搬到副表，还需再加一张索引表，两张表办一件事 |
| 2 | 普通 rowid 表 + `UNIQUE(message_id, idx)` | 实测同一份数据：`WITHOUT ROWID` 块表 1583 MB，rowid 表 1203 MB。索引 B-tree 页的行内载荷上限远小于普通表，中等 blob 被过早溢出。Rejected：`WITHOUT ROWID`（凭「主键即行」的直觉选，实测多付 380 MB） |
| 3 | 大块**压缩**存储，不做内容寻址、不外置到文件 | 对 >4 KB 的 92,499 个块按内容哈希去重实测**只省 0.1%（2 MB）**——工具读到的内容几乎不重复。压缩实测 2.17x（2075 MB → 956 MB）。>256 KB 仅 552 块共 419 MB，压完约 95 MB，不值得再引入第二套存储 + GC + 备份语义。Rejected：照搬 `agentre-server` 的 `sync_avatars` 内容寻址（avatar 重复率高，与本场景不是同一回事）；Rejected：超阈值落文件系统 |
| 4 | 压缩阈值 4 KB | 实测该阈值覆盖 74.7% 的字节而只需处理 10.4% 的块。Rejected：全量压缩——793k 个均值 660 字节的小块单独压收益有限；Rejected：64 KB——只覆盖 26.2% 的字节 |
| 5 | 迁移**前台阻塞 + 分批提交** | 用户决策。一次性、原子、可回滚，代码里不留任何双读分支。分批提交是因为单事务实测把 WAL 顶到 572 MB。Rejected：**后台惰性搬迁 + 读路径回落旧列**——需要在读路径上长期供养兼容分支，正是 `AGENTS.md` 第 3 条禁止的形态 |
| 6 | 读侧是**元数据全量 + 块按需取**，不是历史截断 | Problem 6 的三处派生视图依赖完整历史；有了 `type` 列，它们从「前端拿 26 MB 自己遍历」变成后端点查（`subagent_state` 全库仅 14,136 块 / 11.7 MB）。Rejected：`LoadSession` 只返回最近 N 轮的完整消息——会让后台任务面板漏任务、大纲残缺、上下文占用算错 |
| 7 | 实体 API 不变，由仓储在读时填充、写时拆解 | `GetBlocks()` / `SetBlocks()` 在服务层有 30 处调用（`chat.go` 占 13 处）。让 `Message` 在内存里照旧携带块、仅改变持久化形态，可使服务层零感知。Rejected：把块表暴露给服务层并改写 30 处调用点 |
| 8 | pi 恢复标记改存 `app_settings`，key 为 `chat.pi_recovery:<sessionID>` | 它当前借 `chat_messages` 的四个列表达自己（Problem 7），而本轮**删掉 `blocks_json`**（它的 payload 就在那一列）并给 `device_id` 改名（它的查找键就是那一列）——两根支柱同时消失，原地不动已不可能。`app_settings` 是通用键值表且已在存运行态（`update.last_check` / `update.skipped_version`），装一条带键的记录是它的本职而非借道；按 key 查是主键点查，`idx_chat_messages_recovery`（712 KB）随之删除。**连带后果**：隐藏命名空间当前由标记行的自增 id 推出（`ReplacementRecoverySessionID(marker.ID)`），改用键值存储后没有自增 id，改为由会话 id 推出——语义不变（一条会话同时只有一次替换生成在飞），但需要一并调整并回归。Rejected：**独立成表**——用户判定不值得为这个规模的东西加一张表；Rejected：给 `chat_messages` 加查找键与 payload 两列——把两个只服务 pi 撤销的列挂在最热的表上 |

### 命名

| # | Decision | Basis and rejected option |
|---|---|---|
| 9 | 时间戳统一为 `createtime` / `updatetime`，agentred 随大流改名 | 三套 schema 里两套已用它，且是 cago 惯例；改动面最小的一侧是 agentred。Rejected：三侧统一改成 `created_at` / `updated_at`——要动 28 张表，收益相同 |
| 10 | 「会话最后活动时刻」统一为 `last_message_at` | 名字直说它是什么。改名同时消除 GORM 把 `UpdatedAt` 认作自动更新字段的陷阱，`autoUpdateTime:false` 与其守卫测试随之不再需要。Rejected：桌面端改叫 `updated_at`——会把陷阱扩散到桌面端，还与同表的 `updatetime` 混淆 |
| 11 | agentred 的 `updated_at` 改名为 `last_message_at`，承认其真实用途 | 该列的唯一消费方是 wire 的会话摘要「最后活动时刻」（Problem 15），改名后与决策 10 落在同一个词上。agentred 库不需要行更新时刻列——现在也没有任何读取方。Rejected：改名为 `updatetime`——会让一个行更新列继续喂「最后活动」字段，把语义错配固化；Rejected：拆成两列——新列没有读取方 |
| 12 | 「发起端本地会话 id」统一为 `peer_session_id` | 消除与桌面端本机 `session_id` 的同名异义；`peer_` 前缀已在 agentred 与 `peer_fingerprint` 上成立。Rejected：统一为 `session_id`——保留同名异义，是三类问题里最容易酿成事故的一类 |
| 13 | 同步元数据统一**词根**，不强行统一前缀 | 桌面端的同步列**寄生在各业务表上**，需要 `sync_` 前缀与业务列区分；server 的 `sync_objects` 是专表，前缀冗余。前缀差异由结构差异正当产生。Rejected：两侧列名逐字相同——会逼专表接受无意义前缀 |
| 14 | 「来源设备」两端统一为**指纹**，词根 `origin_fingerprint` | 该值当前是 server 的 `devices.id`，到桌面端被 `strconv` 成字符串；而本工作区其余跨机引用一律用指纹。因无任何读取方参与判定（Problem 12），表示变更不产生行为差异。Rejected：统一成数值 `source_device_id`——数值是 server 本地主键，桌面端离线创建的行没有它 |
| 15 | 「归属账号」**不统一**，两侧各自保留 | server 有真实的 `users` 表，`user_id` 是 **12 张表**统一使用的外键；桌面端没有「用户」实体。只改其中 5 张同步语境的表会把 server 自己的约定劈成 5:7，拿跨仓一致换仓内分裂。Rejected：统一为 `account_id`（早先版本的决策，清点 server 全表后撤回）；Rejected：桌面端改叫 `sync_user_id` |
| 16 | 机器指纹统一为 **`device_fingerprint`**（不是 `agentred_fingerprint`） | 远端目标**可以是 agentred，也可以是另一台桌面端**：`device_entity` 有 `KindDesktop` 与 `KindAgentred` 两种（`device.go:11-12`），`relay_svc/relay.go:174` 两种都中转，`internal/peer/` 就是桌面端的入站对端面；`agent_backends.DeviceID` 的实体注释原话是 "the **target machine 的** canonical device fingerprint"。域词以 server 的 `devices` 表（kind ∈ {desktop, agentred}）为准。原错只在 `_id` 那半截（暗示数字主键），`device` 那半截一直是对的。`chat_messages.device_id` 的值直接来自 `agent_backends.device_id`，一并改。Rejected：**`agentred_fingerprint`**（本 spec 早先版本的决策）——把 device-neutral 的名字收窄成一种机器，等于在修名实不符的同时造一个新的；Rejected：只改注释说明它存的是指纹 |
| 17 | Go 侧 `toolUseID` 一律改为 `toolCallID` | 以线格式为准，它是三套 schema 与三个 CLI 后端的共同契约。Rejected：改线格式迁就 Go 命名 |
| 18 | agentred 表名**保留 `daemon_` 前缀**；仅把日志表词汇统一到 `daemon_notification_journal` | 用户判定前缀保留：它与包路径 `internal/daemon/` 一致、grep 友好，属取舍而非对错。日志那行东西有三个词（wire `JournaledNotification` / agentred `notification_logs` / server `journal_frames`），以线格式为准统一词根，这一条与前缀无关。Rejected：去掉前缀（本 spec 早先版本的决策，用户撤回）；Rejected：两侧表名完全相同——会抹掉「谁是源、谁是镜像」这个真实区别 |
| 19 | server 的四张表统一到 **`agent_session_*`** 域前缀；`mirror` 仅作为动作保留 | 「已保存」是**名单**的属性（边界由 `internal/api/follow/guard_test.go` 守），「来自对端」是**行**的属性（`peer_fingerprint` 列）——两者都不该进表名。把 `saved_` 或 `peer_` 焊进名字等于把不变量复制进名字，而这恰是 server 长出本地 agent 能力时会被打破的那条；该退化案例今天已存在：`sessionimport_svc/execute.go:20` 记着 server 以「每进程随机的合成指纹」充当对端，且已撞上「会话没人认领得了」的失败模式。认领机器、attach、拉帧确实是镜像动作，执行该动作的服务保留这个词；数据层（实体、仓储、表、API 路径）退出。Rejected：`saved_session_*`——一旦有 server 本地发起的会话就不再为真；Rejected：裸 `sessions`——server 有浏览器会话鉴权（`SessionOrDeviceAuth`），裸名歧义；Rejected：与桌面端同名 `chat_sessions`——过度声称，桌面端那张是权威记录带运行态；Rejected：只改表名不改包名与路径——留一半比喻，下次还要再来一轮 |
| 20 | HTTP 同步契约的墓碑由 `deleted`（bool）改为 `deleted_at`（时刻） | 时刻在桌面端库、wire proto（`wire.proto:426` 的 `sync_deleted_at`）、server 库三处都是时刻，HTTP 契约的布尔是 3:1 的孤例；压成布尔后落地时只能另行编造删除时间。这是本轮**唯一的信息量变化**。Rejected：三处库改成布尔——会丢掉 30 天回收窗口依赖的时间 |
| 21 | **其余表名、`*_json` 后缀、载荷列名不动** | `chat_messages` 与镜像日志建模的是不同东西（本地权威对话 vs 别处会话的原始通知帧）；`*_json` 后缀在无 json 类型的 SQLite 上承载类型信息，在有 json 类型的 MySQL 上冗余；agentred 的 `payload` 装 protobuf 字节、桌面端 `payload_json` 装 JSON 文本，异名正对应异类型；两侧的复数命名与域前缀本来就一致 |
| 22 | 三侧均未发布/未部署，直接改名，不留兼容路径 | 用户确认三侧数据均可丢弃。Rejected：加过渡期双读—— `AGENTS.md` 第 3 条禁止的兼容分支 |

### 写入路径与生命周期

| # | Decision | Basis and rejected option |
|---|---|---|
| 23 | 会话整行回写由 `Omit` 黑名单改为 `Select` **白名单**，只写配置列 | 失败模式反转：漏掉一个**配置**列 → 那次写入不发生 → 功能当场不工作、测试立刻抓到；而漏掉一个**运行态**列（现状）→ 静默覆盖别人的并发写入 → 就是那段注释里逐条记着的间歇性事故，没有任何东西抓得到。清单已经漏了 `context_window`（Problem 18），说明手工黑名单不可维护。Rejected：**运行态拆进 `chat_session_runtime` 副表**——`agent_status` / `last_message_at` 是运行态却在每次侧栏渲染、每次索引页分页时都要读，拆表会给最热的读路径加一次 join，换来的物理隔离并不比白名单多修正一个缺陷；Rejected：把 `context_window` 补进 `Omit` 就算了——治这一个，下一个新增的窄写列照样会漏 |
| 24 | 无人引用的后端墓碑定期回收，悬空引用有巡检 | 引用完整性靠 service 校验而非外键，就必须有配套回收，否则墓碑与悬空引用单调增长（现状 236 条墓碑、137 条悬空会话引用）。回收判据是「墓碑且无任何会话/执行目标引用且超过保留期」。Rejected：**改成原地 Update 以免产生墓碑**——经核实 `AgentBackendSvc.Update` 一直存在、前端也在用，「改配置=删旧建新」是早先体检的误判，此处没有可修的缺陷；Rejected：加外键级联——本仓一贯以 service 校验维护引用，改用外键是另一个量级的决定 |
| 25 | 扫描创建的撞名判据把墓碑一并计入 | 只看 ACTIVE 行使每轮「扫描建 → 被删 → 再扫描」都留一条新墓碑（Problem 19 的直接成因）。判据改为「同名行存在即跳过，无论其状态」后，回收才不会被新墓碑持续抵消 |
| 26 | 用一条补丁迁移把秒级种子时间戳改成毫秒 | 既有迁移不可修改（仓库硬规矩），只能追加一条把 `< 1e11` 的值 `* 1000`。判据用阈值而非行 id，因为种子是幂等插入的、id 不稳定 |
| 27 | 补 `issues` 的两个索引；改正 `202608080001` 的过期注释 | 前者让按会话/按受理人反查工单有索引可用；后者是注释与表结构不符，按 `docs/documentation.md` 应直接改正而不是留说明 |

## 块存储形态

块表以 `(message_id, idx)` 为自然键，`idx` 是块在原数组中的下标，决定重组顺序。一条消息的块集合与该消息同生共死：消息被物理删除时它的块行随之删除；`DeleteFromSeq` 截断会话尾部时，被截断消息的块一并消失。任何时刻不允许存在没有宿主消息的块行。

`type` 记录块类型（取值与现有块注册表一致）。`tool_call_id` 是**定位键**，由仓储按块类型填充：`subagent_state` 块填它的 `parent_tool_call_id`（`chat_svc/blocks/subagent_state.go:15`），工具类块填它自身或它所应答的工具调用 id，其余类型留空；空值不进索引。

`data` 是块正文的编码字节，`codec` 标记编码方式。正文超过阈值时压缩存储，未超过或压缩后反而变大时原样存储；解码由仓储在读出时透明完成，调用方拿到的永远是解码后的块。

一条消息读出时，其块按 `idx` 升序重组成与重构前逐块等价的数组交给 `Message`；写入时反向拆解。消息元数据留在主表不动——实测迁移后主表从 2143 MB 降到 2 MB。数据库开启增量式空间回收，使后续删除能真正归还磁盘。

块级操作以 `tool_call_id` 索引点查代替 blob 串匹配，作用在命中的**那一个块行**上，不再读出并重写宿主消息的全部块。仓储层现有的按会话串行化锁继续覆盖这些读-改-写。命中不到目标块时保持现有行为：静默返回，不报错。

## 读路径

`LoadSession` 返回**全部消息的元数据**加**按需取到的块**：转录正文按最近若干轮取，用户向上滚动时继续向前取；后台任务面板、大纲这类派生视图各自按块类型取它需要的那一类块；上下文占用只用主表的 token 计数列，完全不读块表。

前端不再持有整条会话的全部块，但**它能看到的信息集合不减少**：Problem 6 里三处依赖完整历史的派生视图，其数据来源从「遍历本地全量消息」换成「后端按类型点查」，结果保持等价。

已有游标分页范式可参照：`agentre-server` 的会话通知日志就是一帧一行 + `ListFramesBySeq` / `ListFramesBefore`（`workspace_svc/session_mirror_read.go:66`、`:140`）。

## 改动清单

合计：**新增 1 张表、改动 20 张表**（桌面端 13 / agentred 2 / server 5），其中 **4 张连表名一起改**。按改动类型分组，同一张表可能出现在多组里。

### 一、新增表（1，桌面端）

| 表 | 形态 | 来自 |
|---|---|---|
| `chat_message_blocks` | `(id, message_id, idx, type, tool_call_id, codec, data)`；`UNIQUE(message_id, idx)`、`INDEX(tool_call_id)`、`INDEX(type, message_id)` | 决策 1/2 |

pi 恢复标记**不新建表**，改存既有的 `app_settings`（key `chat.pi_recovery:<sessionID>`，决策 8）。

### 二、表改名（4，均在 server）

| 现名 | 改后 | 来自 |
|---|---|---|
| `mirror_session_summaries` | `agent_sessions` | 决策 19 |
| `mirror_journal_frames` | `agent_session_notification_journal` | 决策 18/19 |
| `followed_sessions` | `agent_session_saves` | 决策 19 |
| `mirror_delete_todos` | `agent_session_delete_todos` | 决策 19 |

agentred 的 `daemon_sessions` **保留原名**；`daemon_notification_logs` → `daemon_notification_journal`（只统一词根，保留前缀，决策 18）。

### 三、列改名（19 张表）

| 侧 | 表 | 现名 → 改后 | 来自 |
|---|---|---|---|
| 桌面端 | `agent_backends` | `device_id` → `device_fingerprint` | 决策 16 |
| 桌面端 | `chat_messages` | `device_id` → `device_fingerprint`（上一行那个值的下游） | 决策 16 |
| 桌面端 | `project_locations` | `daemon_fingerprint` → `device_fingerprint` | 决策 16 |
| 桌面端 | `chat_sessions` | `exec_daemon_fingerprint` → `exec_device_fingerprint` | 决策 16 |
| 桌面端 | 带 `SyncMeta` 的 9 张表<br>`llm_providers` `agent_backends` `agent_backend_cli_overlays` `agents` `agent_exec_targets` `departments` `projects` `project_agents` `project_locations` | `sync_origin` → `sync_origin_fingerprint`（值由数字字符串改为指纹） | 决策 14 |
| agentred | `daemon_sessions` | `created_at` → `createtime`；`updated_at` → `last_message_at` | 决策 9/11 |
| agentred | `daemon_notification_journal` | `created_at` → `createtime` | 决策 9 |
| server | `agent_sessions` | `session_id` → `peer_session_id`；`updated_at` → `last_message_at` | 决策 10/12 |
| server | `agent_session_notification_journal` | `session_id` → `peer_session_id` | 决策 12 |
| server | `agent_session_saves` | `session_id` → `peer_session_id` | 决策 12 |
| server | `agent_session_delete_todos` | `session_id` → `peer_session_id` | 决策 12 |
| server | `sync_objects` | `source_device_id`(bigint) → `origin_fingerprint`(varchar)；`sync_updated_at` → `updated_at` | 决策 13/14 |

**明确不改**：
- `paired_agentreds.daemon_fingerprint` —— 该表确实只装 agentred（包注释：桌面端 LAN 直连 agentred），限定词名副其实。
- `agent_backend_cli_overlays.agentred_fingerprint`、`sync_lost_changes.agentred_fingerprint` —— 无法从现有代码判定其机器是否可以是桌面端对端；不在证据不足时改名。
- server 12 张表的 `user_id`（决策 15）、桌面端 `server_state.device_fingerprint`（本机在 server 上注册的设备，另一个角色）。

### 四、非改名的结构与数据改动（均在桌面端）

| 表 / 对象 | 改动 | 来自 |
|---|---|---|
| `chat_messages` | **删列** `blocks_json`；不再承载 pi 恢复标记 | 决策 1/8 |
| `idx_chat_messages_recovery` | **删索引**（712 KB，只服务一个哨兵 role） | 决策 8 |
| `app_settings` | 承接 pi 恢复标记（无 DDL，仅新增 key 前缀约定） | 决策 8 |
| `issues` | 补 `session_id`、`assignee_agent_id` 索引 | 决策 27 |
| `agents` 种子行 | 补丁迁移把秒级 `createtime`/`updatetime` 换算成毫秒 | 决策 26 |
| 库级 | 开启增量式空间回收，并做一次回收 | 决策 3 |

### 五、协议与代码标识符

| 层 | 现名 → 改后 | 来自 |
|---|---|---|
| wire proto | `AgentBackend.device_id` → `device_fingerprint` | 决策 16 |
| wire proto | `SessionSummary.updated_at` → `last_message_at` | 决策 10 |
| wire proto | `AgentBackend.sync_origin` → `sync_origin_fingerprint`（`wire.proto:425`） | 决策 14 |
| HTTP 同步契约 | 墓碑 `deleted`(bool) → `deleted_at`(时刻) | 决策 20 |
| HTTP 路径 | `/v1/mirror/*` → `/v1/agent-sessions/*` | 决策 19 |
| Go（桌面端） | 标识符 `toolUseID` → `toolCallID` | 决策 17 |
| Go（server） | `mirror_entity` / `follow_entity` → `agent_session_entity`；`mirror_repo` / `follow_repo` → `agent_session_repo`。执行镜像动作的服务保留 `mirror` 词 | 决策 19 |

### 六、无 DDL 的行为与策略改动

| 改动 | 来自 |
|---|---|
| 会话整行回写由 `Omit` 黑名单改为 `Select` 白名单；`context_window` 不再被拍回旧值 | 决策 23 |
| pi 恢复标记的隐藏命名空间改由会话 id 推出（原由标记行自增 id 推出） | 决策 8 |
| 无人引用的后端墓碑定期回收；悬空引用巡检 | 决策 24 |
| 扫描创建的撞名判据计入墓碑，不再每轮新留一条 | 决策 25 |
| server 去掉 `autoUpdateTime:false` 与其守卫测试（该防御由列名招来，随改名消失） | 决策 10 |
| 改正 `syncmeta_entity.SyncOrigin` 与 `202608080001` 两处与实现不符的注释 | 决策 14/27 |

### 对齐效果

```
agentred  daemon_notification_journal       (peer_fingerprint, peer_session_id, seq, payload, createtime)
server    agent_session_notification_journal(user_id, peer_fingerprint, peer_session_id, seq, payload, createtime)
```

同一份日志的源与镜像，除 `user_id` 与各自的域前缀外逐列同名同义、表名共用词根。「来源设备」改用指纹后，`downlink` 边界上那次 `strconv` 消失，两端存同一个字符串。`remote_device_svc.ExternalDeviceID` 承担的「本机指纹不算远端」判定语义不变。

## 迁移与交付顺序

本轮是一次交付，但内部按**互不相交的改动面**分批落地，每批各自一条迁移、各自一个提交，`git bisect` 粒度不因合并而变粗：

1. **pi 恢复标记迁出 `chat_messages`**（桌面端，0 行，落到 `app_settings`）——必须先于删 `blocks_json` 与 `device_id` 改名，它的 payload 与查找键都在这两处。
2. **块存储**（桌面端）——建块表、拆块、压缩、删 `blocks_json` 列、开启并执行空间回收。
3. **读路径**（桌面端 + 前端）——元数据全量 + 块按需取。
4. **命名对齐**（桌面端 + agentred + proto）——列改名、表改名、线格式字段改名，验证后推送。
5. **命名对齐**（server）——钉住上一步推送的修订，切换列名、表名、包名、API 路径。
6. **写入路径与生命周期**（桌面端）——会话整行回写改白名单、扫描撞名判据计入墓碑、墓碑回收与悬空引用巡检、秒级种子时间戳补丁迁移、`issues` 索引、过期注释改正。

第 6 步与前五步改动面不相交，排在最后是因为它是本轮唯一触碰**行为**的部分（决策 23/24），单独一批便于在出问题时整批回退而不牵动前面的结构改动。

第 4、5 步跨两个仓库，顺序由 `AGENTS.md` 的既定方向决定；两个仓库各自独立提交。三侧均未发布/未部署，改名迁移不需要回填，也不需要保留旧列。

第 2 步在本机 2.25 GB 库实测总耗时 72.8 秒（拆块与写入 60.2s、建索引 1.8s、删列 2.6s、回收 8.2s），库体积 2.1 G → 1.2 G；迁移期间应用不进入主界面。绝大多数用户的库远小于此，耗时为秒级。写入分批提交以控制 WAL 峰值。无法解析的历史 `blocks_json`（若存在）不得静默丢弃：该消息保留其元数据行，并留下可被排查的记录。迁移期间不发起任何网络调用。

## Out of scope

以下两项**看起来同概念，但改名解决不了**，各自另行立项：

- **会话运行态的形状不同**：桌面端用单枚举 `agent_status` ∈ {idle, running, waiting, error}；agentred / server / 线格式用 `lifecycle_state` 加 `waiting_for_input` 两列表达同一件事。统一它要改表示形状，会触碰状态流转。
- **会话标识的类型不同**：线格式 `SessionSummary.session_id` 是 `int64`，server 库里是 `varchar`，agentred 库里是 `TEXT`。统一要改协议类型。

其余不在本轮：

- 除决策 20 外的任何行为变更。
- 补上 Problem 12 里并不存在的「字典序打破平局」判定——本轮只改正注释。
- server 侧会话通知日志的保留策略与压缩（用户已明确不需要保留策略）。
- server 侧 `followed_sessions.device_fingerprint` 与 `peer_fingerprint` 的语义漂移收敛。
- 桌面端 `issues.state` 与 `issues.status` 同表并存的易混命名。
- 会话/消息的保留期与自动清理策略。
- **e2e 夹具漏进真实生产库**：`agent_backends` 里有 25 条名为 `E2E Local Backend` / `E2E Codex Backend` 的行，名字来自 `e2e/fakes/install.go:79`，而 e2e 本应在隔离数据目录中运行。本轮的墓碑回收会清掉这些行，但**泄漏本身是另一个缺陷**，需要单独查 e2e 的数据目录隔离为何失效。
- `chat_sessions` 运行态列拆表（决策 23 选择了白名单方案，物理拆分未做）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `chat_repo.MessageRepo` 公开方法（sqlmock） | 块的拆解与重组逐块等价；`idx` 决定顺序；截断与删除时块不残留；压缩块与未压缩块读出结果一致 | `chat_repo/message_test.go`（`9070ce2d` 为 `UpdateUsage` 建的窄写用例同一形态） |
| 五个块级仓储方法 | 定位改为索引点查后，命中、未命中、并发同会话读-改-写的行为与重构前一致 | 现有 `FlipSubagentStatus` / `PatchSubagentProgress` 用例 |
| 块编码往返（纯函数） | 压缩/解压往返无损；超阈值与未超阈值分支；压缩后变大时回落原样存储 | 无 |
| pi 恢复标记存取 | 迁到 `app_settings` 后，标记的建立、查找、状态流转与借道时期等价；命名空间改由会话 id 推出后仍不与在飞的另一次替换生成相撞 | 现有 `ParseReplacementRecoveryMarker` 用例 |
| `chat_svc` 服务层（repo mock） | `GetBlocks()` / `SetBlocks()` 契约不变，30 处调用点行为不变 | `chat_svc/chat_test.go` |
| `LoadSession` 服务层 | 元数据全量、块按需取；三处派生视图的数据集合与重构前等价 | `chat_svc/chat_test.go` |
| 前端转录与派生视图（vitest） | 向上滚动继续取块；后台任务面板与大纲不因窗口而残缺 | `frontend/src/components/agentre/background-tasks/*.test.ts` |
| 迁移 DDL 快照（三侧） | 改名后的列与表存在、旧名不存在 | `agentre-server/migrations/cleanup_scan_indexes_test.go` 同一形态的 DDL 断言 |
| 同步上行/下行服务层（两侧，repo mock） | 改名前后同一份输入产生同一份落库结果与同一份出站载荷 | `agentre/internal/service/sync_svc/*_test.go` |
| HTTP 契约黄金测试（server） | 请求/响应字段名、墓碑表示与路径的变更被显式记录 | `agentre-server/internal/api/http_golden_test.go` |
| 线格式往返（`protowire`） | `AgentBackend` 指纹字段与 `SessionSummary` 时刻字段改名后编解码往返不变 | `protowire/*_test.go` |
| 会话摘要写入（server） | 去掉 `autoUpdateTime:false` 后，普通更新仍不覆盖 `last_message_at`——即该防御确实是由列名招来的 | 现有 `TestSessionSummaryEntity_PlainUpdatesNeverRewritesUpdatedAt`，改写而非删除 |
| 会话整行回写（sqlmock） | 白名单外的每一列都写不进去，含 `context_window` 这条现存缺陷的回归：拿轮次开始时的旧实体收尾，轮中上报的窗口值不被拍回 | 现有 `chat_repo/session_test.go` 的 `Omit` 用例，改写为白名单断言 |
| 墓碑回收与悬空引用巡检（sqlmock） | 有引用的墓碑不被回收、无引用且超保留期的被回收；巡检报出悬空引用而不擅自改写 | 无 |
| 扫描创建（repo mock） | 同名墓碑存在时不再新建第二条 | 现有 `scan_create_test.go` |
| 种子时间戳补丁迁移 | 秒级值被换算成毫秒，已是毫秒的值不被二次放大 | 迁移不写单测，由「真实体量库副本手工核对」覆盖 |

命名部分是纯改名，正确性主要由**编译期**保证：任何遗漏的引用都无法通过构建；上表覆盖的是编译期抓不到的三类——落库列名与表名、跨进程的线格式字段名、以及决策 10 所声称的「陷阱随列名消失」。

迁移本身按仓库约定不写单元测试；`internal/bootstrap/cago_test.go` 只证明迁移链在**全新库**上跑得通。**在真实体量的库上验证**这件事无法自动化，需按 `docs/develop.md`「When Touching Persistent Data」第 4 步，对一份携带真实行的库副本手工核对：迁移前后消息条数、每条消息的块序列、库体积与主表行宽。本轮已在 2.25 GB 真实库副本上完成一次该形态的探针，结论见 Problem A 与块存储各决策。

## Open questions

无。
