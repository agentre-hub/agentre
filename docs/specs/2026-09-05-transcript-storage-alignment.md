# 转录存储对齐：两个宿主一套块存储、一套投影、一份持久编号

> Status: Approved
> Owner: 桌面端 chat 域 / agentred daemon 域
> Last updated: 2026-09-06

**Objective:** 让 agentred 与桌面端用同一套转录存储和同一份投影——**帧退回成纯线上格式，
落库一律是块**；并把对端帧编号从"每进程现算"改成"发布时分配、随块持久化"，使宿主重启不再
改写已发布帧的身份。

**Hard invariant:** 五条不得回退。

1. **补齐的可观察结果不变。** 对端仍能从任意有效游标补回它缺的那一段，仍不产生跳号；
   游标失效时仍能自愈（复位重来），不出现"没有错误也没有内容"的静默冻结。
2. **实时流式不变。** 挂在一条运行中对话上的对端（浏览器控制台 / 另一台桌面端）仍逐 token
   看到文本增长，不退化成"整块整块地跳出来"。
3. **隐私边界不变。** 未保存的对话在 server 上仍然一个字都不落库；`R19`（cwd 不下行）不变。
4. **桌面端本地转录仍是它自己的真相源。** 远端执行不改变这一点：桌面端照旧持有自己那一份
   `chat_messages` / `chat_message_blocks`，离线可读。
5. **local-first 不变。** 未登录、离线、纯 LAN 直连（Mode A/B）下行为一字不改。

本轮有**一处刻意的行为变化**，由决策 4 载明：补齐回放的粒度从"逐事件"收敛为"逐块"，与桌面端
做宿主时今天的行为一致。

## Problem

以下事实均于 2026-09-05 对工作区当前代码与本机运行数据核实，非估算。

### A. 两个宿主对同一个协议给出了两种实现，agentred 选了贵的那一侧

1. **桌面端做会话宿主时不存任何帧日志。** 桌面端的迁移里没有 journal 表
   （`migrations/202609040106_chat.go` 只建 `chat_sessions` / `chat_messages` /
   `chat_message_blocks`）。对端来补齐时，`chat_svc.attachPeerTranscript`
   （`internal/service/chat_svc/peer_session_history.go:253`）读回消息与块，交给
   `synthesizePeerHistory`（同文件 `:366`）**现折成帧**：按块类型摊成
   `UserAskRequest`/`UserAskResolved`、`SubagentDone`/`SubagentModel`、
   `ToolPermissionRequest`/`ToolPermissionResolved`……认不出的块走
   `agentruntime.UnrecognizedBlock` 原样往下送，`UsageUpdate` 从消息的 token 列现折。
   这份"块 → 帧"投影是完整的、在线上被使用的。

2. **agentred 做同一件事却把每条 event 落一行。**
   `internal/daemon/handlers/runtime.go:860` 的 `fanout` 对 backend 事件通道里的
   **每一条**事件调一次 `em.emit`，而 `sessionEmitter.emit`（同文件 `:1127`）逐条
   `journal.Append`。落库形态见 `internal/daemon/migrations/202609040101_daemon_baseline.go`：
   `daemon_notification_journal` 主键 `(conversation_id, seq)`，`payload` 是裸
   protobuf、不压缩，且注释明写"只追加、永久保存——agentred 不再回收任何一行"。

3. **被逐行落库的事件里，有相当一部分连桌面端自己都不落。** `TextDelta` / `ThinkingDelta`
   是**逐 SSE 片段**一条；`OutputActivity`（`agentruntime/event.go:39` 注释自陈"不进
   accumulator、不落库、不推 UI 内容"）、`Retry`（`handlers/retry.go` 注释"no acc / no
   persist"）、`RuntimeStatus` 同样只作过场。它们在 journal 里各占一整行。

4. **两种实现的差距不是常数级。** 桌面端一轮对话落成几十个块行；同一轮在 agentred 上是
   几千条事件行，每行还要付一次 `INSERT ... SELECT COALESCE(MAX(seq),0)+1` 的自分配
   写锁（`notification_repo/notification.go:117` 的 `appendSQL`）。

### B. 帧编号是每进程现算的，宿主重启即改写同一份内容的身份

1. **桌面端实时按"事件"编号。** `turn_run.go:136` 的 `consumeEvents` 对每条 canonical
   事件调 `publishPeerEvent`，`peerSessionPublication` 顺序发号；该结构注释自陈
   "deliberately in-memory"（`peer_session_history.go:25`）。`AttachPeerSession`
   交回的 `LatestSeq` 就是这个内存计数器（`peer_session.go:229`）。

2. **重启后同一份内容按"块"重新编号。** 重启后 publication 从零初始化，
   `synthesizePeerHistory` 按块摊帧、`Seq = index + 1`。同一条对话的同一段内容，
   编号从"每事件一号"（数千）变成"每块一号"（数十）。

3. **常态后果：每次宿主重启，对端把整条对话删掉重拉。** server 侧
   `mirror_svc.dropCursorAboveHighWater`（`agentre-server/.../mirror.go:693`）在游标越过
   对端高水位时复位，并**删掉该对话已存的全部帧**再从 0 重拉——它的注释写明这条删除
   "不是打扫卫生，而是全部要点"。高水位从数千跌到数十必然触发它。

4. **少见但静默的后果：编号错位。** 若 server 的游标停在新高水位**之下**（上一次补齐中途
   断开），复位不触发。此时表里 1..cursor 是旧编号（细粒度事件）的内容，从 cursor+1 拉回的
   是新编号（块级）的内容；而 `WriteFrames` 是 `ON CONFLICT DO NOTHING`
   （`agent_session_repo/journal_frame.go:67`），重叠段旧行胜出。转录于是既重又漏，
   **没有任何一处会报错**。

5. **这不是 agentred 的问题——恰恰相反。** journal 的持久、无洞、不可变 seq 是它今天唯一
   做对而桌面端没做的事。把 agentred 对齐到桌面端而不先解掉这一条，等于把上面两条后果
   扩大到 agentred。

### C. 事件级帧同时把 server 的镜像撑大

`mirror_svc.writeFrames`（`agentre-server/.../mirror.go:767`）把对端 journal 的每一帧原样
`proto.Marshal` 落 `agent_session_notification_journal`，一帧一行、不压缩、不回收。所以
agentred 的事件级放大**逐字传导到 server**：同一段内容，桌面端发起的对话在 server 上是块级
帧（因为桌面端宿主本来就现折），agentred 发起的是事件级帧。同一张表里两种粒度，成本相差
一个数量级。

### D. 参考量级（桌面端本机实测，2026-09-05）

`~/Library/Application Support/agentre/agentre.db`：3735 会话 / 28116 消息 /
**990719 块** / 1.51 GB，其中 `chat_message_blocks` 数据页占 93.7%。这是**块级**存储的体量；
同样的对话量若按事件级存，行数是它的两个数量级以上。本轮不改这 1.5 GB 的编码方式
（见 Out of scope），它是"块级存储值多少"的基准，不是本轮的优化目标。

## Actors and user stories

1. 作为**桌面端用户**，我希望重启 App 之后，挂在同一条对话上的浏览器控制台不必把整条转录
   重新拉一遍，也不会看到重复或缺失的段落。
2. 作为**从浏览器控制台派发任务的用户**，我希望 agentred 上那条对话的转录占用与桌面端上
   同样一条对话相当，而不是高一个数量级。
3. 作为**运维自己那台 agentred 的用户**，我希望 daemon 的数据目录随对话内容增长，而不是随
   事件条数增长。
4. 作为**在这个仓库里加新块类型的开发者**，我希望"事件 → 块"与"块 → 帧"两份投影各只有一处
   实现，不必在桌面端和 daemon 各写一遍、各测一遍。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **帧只是线上格式，落库一律是块。** agentred 与桌面端使用同形的转录存储（消息行 + 块行），`daemon_notification_journal` 退役。 | 桌面端已经这样运行且投影完整（`peer_session_history.go:366`），agentred 没有任何需要不同的理由。Rejected：保留帧存储、只做物理层优化（分段打包 + 字典压缩）——它把成本降一个常数因子，但保留了"同一份内容两种存储形态、两套编号"这个真正的问题，而问题 B 正是由它派生的。 |
| 2 | **"事件 → 块"的累积与"块 → 帧"的投影下沉到 `internal/pkg`，两个宿主共用一份。** | 已核实为纯搬迁：`chat_svc/blocks`、`chat_svc/turn`、`chat_svc/handlers` 的依赖闭包里没有任何 service / repository（`go list -deps`），且 `internal/pkg/*` import `internal/model/entity` 是既有惯例（`agentruntime` / `agentskill` / `agentprovider` 等十余处）。先例：`internal/pkg/turnstats` 的注释即"与 chat_svc 共用一份"。Rejected：在 daemon 侧另写一份投影——块类型每演进一次要同步两处，而漏同步的表现是转录静默少一张卡。 |
| 3 | **seq 在帧被发布的那一刻分配，并随块持久化。** 宿主重启后按持久化的 seq 重放，与"第几个块"无关。 | 这保住了 journal 今天唯一做对的那件事（持久、无洞、不可变的编号），同时不必为此保留事件级存储。原地修补产生的 resolved 帧拿到的是**新**分配的 seq（排在末尾），不挤动已发布的编号——而这反而与实时流序一致：`UserAskResolved` 本来就是在 request 之后、可能隔了很多帧才到。Rejected A：高水位带 epoch，不匹配就全量重拉——正确但把问题 B 的"每次重启重拉"固化成设计。Rejected B：seq 由 (消息 seq, 块下标, 变体) 编码成字典序——天然稳定但有编码上限，且 resolved 帧落在中段而非末尾，与实时流序不符。 |
| 4 | **帧分两级：预览帧不带编号、不参与补齐；持久帧带 seq、参与补齐与镜像。** 对端收到覆盖同一内容的持久帧时以持久帧为准。 | 不分级就只能二选一：要么给每个 delta 发持久号（回到事件级存储），要么实时也降到块级（不变量 2 回退，控制台不再逐 token 流式）。分级把"流式预览 / 落库为准"这个桌面端 UI 今天已有的心智模型显式化到协议上。**预览帧必须在协议上可区分**，不能靠 `seq=0` 表达——消费侧把 `seq <= cursor` 当重复丢弃，`seq=0` 会被无条件丢掉。 |
| 5 | **在途那一轮靠 checkpoint，不另立 WAL。** | 桌面端今天就是这么做的（`chat_svc/dispatcher_runtime.go:16`，每个 `ToolResult` 后落一次差分），崩溃后能看到已完成的部分。daemon 照抄同一条即可；再立一个"在途帧 WAL"就是把刚退役的 journal 换个名字请回来。 |
| 6 | **本轮只交付 `agentre` 仓；server 侧跟随另起一轮。** | AGENTS.md 的跨仓交付顺序是强制的：`agentre` 先落地、验证、推送，消费方再 pin 不可变 revision。server 那侧的改动（只存持久帧、清理旧的事件级帧）依赖本轮定下的帧契约，不能并行。 |
| 7 | **本轮不动块正文的编解码，也不重压存量。** | 用户已就此明确"先不做处理"。压缩是纯存储层的正交问题：wire 上传的仍是明文 protobuf，各库可各自决定，不构成跨仓耦合，因此可以在对齐之后单独一轮做。 |
| 8 | **转录的存储层（消息 + 块的实体与仓储）也共用一份，抽成独立域 `transcript_*`；`chat_repo` 只留会话。** | 机制上成立且已核实：`internal/daemon` 只被 `cmd/agentred` import（桌面端从不在进程内跑 daemon），所以两个宿主是**两个进程各一个 SQLite 库**；daemon 的仓储层已按 `db.Ctx(ctx)` 写、句柄由 `db.WithContextDB` 在 Run 的 ctx 边界注入（`internal/daemon/daemon.go:82`）。因此一个按 `db.Ctx(ctx)` 写的仓储包在两个进程里各自连到自己的库，一行不用改。必须共用的理由：块拆分与 `CheckpointBlocks` 的差分是踩过事故才写对的一层（`dispatcher_runtime.go:16` 记着 723,550 行 / 910 MB 的重写），复制一份等于把那次事故的修复留在一侧。Rejected A：daemon 直接 import `chat_repo`——分层方向合法（daemon → repository）且改动最小，但让 daemon 依赖一个名字写着桌面端 chat 域的包，下一个人会当成误用并在 daemon 侧另写一份"干净的"，正是要防的结果。Rejected B：只共用纯逻辑、存储各写一份——耦合最小，但把最不该有两份的那一层留了两份。 |
| 9 | **agentred 为每条对话补一个本地数字主键，与桌面端 `id` / `conversation_id` 的拆分同形。** | `chat_entity.Message.SessionID` 是本地数字主键，而 `daemon_sessions` 主键是 `conversation_id`（TEXT），没有数字 id；不补这一格就无法共用同一个消息实体。补它不是迁就实现：`chat_entity/session.go:34` 已明写"本地主键与全局标识是两件事"并刻意不合并（2026-08-31 决策 12），给 daemon 同一个拆分之后两个宿主才**真是**同一形状，而不是长得像。Rejected：在共用实体上并列 `SessionID` 与 `ConversationID` 两格、各宿主留空一格——空值有歧义，且第一个写错的人不会有任何一处报错。 |

## 转录存储

### 宿主与它的转录

一条对话在任一时刻由**一个宿主**执行。宿主对这条对话负两件事：把 backend 事件累积成块并
持久化；把块投影成帧交给订阅它的对端。桌面端与 agentred 都是宿主，两者行为一致。

- 前置：一条对话在宿主上开始一轮。
- 动作：宿主消费 backend 事件流。
- 可观察结果：这一轮的内容落成消息行 + 块行；对端按订阅收到帧。
- 失败：宿主在轮中崩溃时，已 checkpoint 的块保留，未 checkpoint 的尾部丢失；该轮在生命
  周期上收敛为中断，而不是停在"运行中"。

agentred 不再持有 `daemon_notification_journal`。它持有的是与桌面端同形的消息行与块行；
`daemon_sessions` 保留（会话身份与元数据），其"最新 seq"不再取自 journal 的 `MAX(seq)`，
改由持久编号回答（见下）。

远端执行时同一段内容在两处各存一份（agentred 一份、桌面端一份）：这是有意的，两者服务不同
的读者（控制台可在桌面端离线时读，桌面端可在 daemon 离线时读），且两份都是块级，总量低于
今天"一份块 + 一份事件级 journal"。

### 复用边界：共用什么、不共用什么

转录这件事在两个宿主上是同一件事，因此**只有一份实现**；宿主之间真正不同的东西各自持有。
这条边界是本轮的产物之一，不是顺带的整理。

**共用一份（`internal/pkg` 与 `transcript_*` 域）：**

- 事件 → 块的累积与各类型 handler；
- 块 → 帧的投影，含未知块的 `UnrecognizedBlock` 兜底；
- 消息与块的实体、仓储：块拆分、`CheckpointBlocks` 的差分、正文编解码、按定位键与类型的点查。

**各自持有（不共用，也不应共用）：**

- **会话行**：`chat_sessions` 有项目归属、未读、执行位置；`daemon_sessions` 有对端指纹、生命
  周期。列与生命周期都不同，强行合并会造出一张两边各留一半空列的表；
- **持久化适配器**（usage / error / context window / permission mode 的写入）与**发射器**
  （Wails 事件 vs RPC 通知）：host 接线，本就该留在各自宿主；
- **会话创建与生命周期**：桌面端归 `chat_svc.EnsureSession`（见 `session-lifecycle.md`），
  agentred 归它自己的握手建行。

判据：**同一段内容在两个宿主上必须由同一行代码写进库、由同一行代码折成帧。** 若某处出现第二份
块拆分、第二份 checkpoint 差分或第二份投影，即视为本轮的回归，由守卫测试判红（见 Testing
decisions）。

### 帧编号

每条对话持有一个单调计数器。宿主每发布一个**持久帧**就从中取下一个号，并把这个号与产生该
帧的块一起持久化。

- 前置：宿主要向对端发布一个持久帧。
- 动作：分配下一个 seq，与块一并落库，然后发布。
- 可观察结果：同一个帧在宿主重启前后拥有同一个 seq；对端的游标跨宿主重启仍然有效。
- 失败：分配与落库不可分——若落库失败则不得发布，否则对端会持有一个宿主认不回来的号。

块被**原地修补**（如一次 `UserAskRequest` 被回答、`subagent_state` 的进度推进）时，修补后
新增的那个帧取**新**的 seq，排在当时的末尾；已发布过的帧的 seq 一律不变。补齐因此按 seq 顺序
重放，`resolved` 类帧出现在其 `request` 帧之后若干位——与实时流序一致，对端按 requestID
归并的既有行为不变。

存量数据（桌面端已有的近百万块行、agentred 已有的 journal）没有编号。存量按**惰性分配**处理：
一条对话第一次需要发布或被补齐时，宿主按块的自然顺序确定性地补齐编号并落库；未被访问的
对话不付出任何代价。这与决策 7"不重压存量"不冲突——惰性分配只写一个整数，不触碰块正文。

### 两级帧与补齐

- **预览帧**：`TextDelta` / `ThinkingDelta` 这类逐片段增量，以及 `Retry` / `RuntimeStatus`
  这类过场状态。不带 seq，不落库，不参与游标推进与去重，丢失即丢失。它们的存在只为不变量 2。
- **持久帧**：块级帧（一个块投影出的一到两帧）与消息级派生帧（`UsageUpdate` / `Done`）。
  带 seq，参与补齐与镜像。

对端规则（2026-09-06 修订，理由见本节末）。「以持久帧为准」不是一句态度，它落成三条具体行为：

1. **预览帧只用于即时呈现。** 对端拿它渲染正在生成的内容，**不得把它累积进自己的转录**，
   也不据它推进游标。
2. **持久帧是转录与游标的唯一来源。** 对端只累积持久帧，并据其 seq 推进游标。
3. **宿主必须实时发布持久帧**，不得只在补齐时才交出。

协议上两者必须可区分，且预览帧不得被当作"seq 小于等于游标"而进入去重路径。

- 前置：一条对端挂在一条运行中的对话上。
- 动作：宿主产出一轮内容——边流式发预览帧，边在块落库时发持久帧。
- 可观察结果：对端逐 token 看到文本增长（不变量 2）；断连重连后补齐只交付它缺的那一段，
  转录不重不漏（不变量 1）。
- 失败：持久帧落库失败则不得发布（见「帧编号」）；预览帧丢失即丢失，不补。

第 3 条是必需的而非可选：一条持续在线的对端若收不到持久帧，它的游标永不前进，重连补齐就会
从头重放它已经看过的内容——那正是不变量 1 禁止的「重」。

> **修订理由（2026-09-06）。** 本节原文只写了「收到覆盖同一内容的持久帧时以持久帧为准」这条
> 规则，未定谁实现它。实现轮据此各自填空：agentred 选择不实时发布持久帧（因为桌面端消费方
> 当时按追加语义累积，发了会重复计数），而消费方仍在累积预览帧。两者都合乎字面，合起来却
> 使补齐重发对端已看过的内容——收尾的规格核验轴实测投递文本为 `"onetwoonetwothreefourfive"`，
> `onetwo` 出现两次，违反 Hard invariant 1。本次修订把责任落到上面三条可观察行为上，
> 不再留给实现方推断。

补齐（`runtime.session.pull` 与其桌面端对称实现）只返回持久帧。**这带来一处刻意的行为变化**：
补齐回来的转录是块级的，不重放逐 token 的生成过程。桌面端做宿主时今天已经如此
（`synthesizePeerHistory` 一个块一帧）；本轮把 agentred 收敛到同一行为。

游标自愈规则不变：高水位低于游标时复位重来；增量拉回的最老 seq 高于"游标 + 1"时把游标复位
到"最老 seq − 1"并记一次告警。这两条今天就在（`session-lifecycle.md`），本轮不改其语义。

### 生命周期与删除

`runtime.session.delete` 的可观察结果不变：daemon 侧该对话的身份行与它的全部转录一并消失，
且按认证对端的指纹收窄——另一台桌面端的同名对话不受影响。落地对象从"身份行 + journal 全部
行"变成"身份行 + 消息行 + 块行"。

会话列表 RPC 报出的"最新 seq"语义不变（对端据此判断自己是否落后），来源从 journal 的
`MAX(seq)` 换成持久编号计数器。

### 共享包边界

**本轮不新建任何共享包。** 四处归属如下，越界即违反 AGENTS.md 的跨仓不变式：

- **`internal/pkg/*`（仓内，不是跨仓共享包）**——决策 2 下沉出的累积器与投影落在这里。桌面端
  与 agentred 同属 `github.com/agentre-hub/agentre` 一个 module（`internal/daemon` 就在其中），
  所以下沉不产生新 module、不需要 pin。它在 `internal/` 下，Go 可见性挡死 agentre-server 引用
  ——这正是要的结果：**server 不需要这份投影**，它收到的已经是帧。
- **`github.com/agentre-hub/agentre/pkg/wire`（跨仓 Go 共享 module，已存在）**——决策 4 的两级
  帧要在协议上可区分，改的是 protobuf。真理源是
  `frontend/packages/agentre-wire/proto/agentre/wire/wire.proto`，`pkg/wire/agentrewire` 是生成
  产物，不在那里手改。这是唯一批准的跨仓 Go 依赖，server 那轮的第一步即 bump pin。
- **`@agentre-hub/agentre-wire`（前端共享包，已存在）**——生成的 TS 侧与 `event-kinds.gen.ts`。
  词表真理源是 `internal/pkg/agentruntime` 的 `EventKind` 常量，生成器在
  `runtimes/remote/wire/tsgen_test.go`。
- **`@agentre-hub/agentre-ui`（前端共享包，已存在）**——它自带**第二份** `event-kinds.gen.ts`：
  agentre-server 通过 git 依赖只取这一个子目录，tarball 里没有兄弟包，因此它不能 import
  `agentre-wire`（理由写在该文件头）。词表变了这一份同样要重新生成。若两级帧改变了控制台转录
  的渲染契约，改动归这个包，**不得**在 server 仓另写一份。

### 兼容性

帧的两级化与补齐语义的收敛改变了线上契约，握手窗口必须相应抬升：`wireversion.Protocol` 与
`MinSupported` 一并上抬，`agentre-wire` 前端包版本同步（`internal/pkg/wireversion` 的方法集
指纹守卫会在方法集变动时判红，按它的注释处置）。不做跨版本降级分支：旧构建握不上手，而不是
握上手后在第一条新帧上炸。

**已镜像的存量帧必须可被显式作废，本轮要把这个信号交出去。** 升级后同一条对话的编号从事件级
换成块级，高水位断崖下跌，多数对话会被消费方既有的"高水位低于游标即复位重拉"规则自动修复；
但游标停在新高水位**之下**的那些不触发复位，落进 Problem B.4 的静默错位。因此消费方不能押在
游标位置的巧合上，必须能判定"这条对话已存的帧属于旧编号世代"。本轮据此定契约：**协议版本跨过
这一档即是该判定的依据**——握手报出的版本已经携带这一信息，消费方按它一次性作废并重拉，不需要
本轮再引入第二个世代标记。消费方那一侧的落地属于 server 轮（决策 6）。

agentred 已有的 `daemon_notification_journal` 数据不迁移、不回放。该表在本轮退役；已存在的
行随对话删除而消失，或在升级时一并丢弃——它记录的是事件级过程，而对齐之后的转录来源是块，
两者不存在需要保留的信息差（块由同一批事件累积而来）。

## Out of scope

- **块正文的编解码与存量重压。** 桌面端那 1.5 GB 不因本轮变小。实测：每块 deflate 相对今天
  −27%，per-block zstd + 训练字典 −49%。单独一轮做，见决策 7。
- **journal / 镜像的保留与回收策略。** 决策 8（"永不回收"）本轮不动。对齐之后"桌面端发起的
  对话在 agentred 上是一份副本"这件事才成立，回收窗口该怎么定要在那之后单独议。
- **server 侧的落地。** 依赖本轮的帧契约，按 AGENTS.md 的依赖序另起一轮（决策 6）。那一轮已知
  的三件事，记在这里作为起点，不在本轮做：bump `pkg/wire` pin；让预览帧在持久化路径上被**静默**
  丢弃（现状是 `mirror.Apply` 对无 seq 的通知打一条 `Warn` 后返回，而预览帧是逐 token 的，照旧
  会把日志淹掉）；按协议版本一次性作废旧编号世代的存量帧并重拉（见"兼容性"）。
- **控制台与桌面端两份转录投影的收敛。** 帧变成块级之后，控制台的"帧 → 转录"与桌面端的
  "块 → 转录"事实上成了同一件事，而今天是两份实现（桌面端在 Go 侧投影成 DTO，控制台在 TS 侧
  自己折）。这是对齐**之后**才成立的收敛机会，按 AGENTS.md 归 `@agentre-hub/agentre-ui`，
  单独一轮。
- **`image` 块内联。** 本机 153 行占 32 MB、单行最大 4.67 MB，是独立的存储形态问题。
- **WAL 长期不收敛。** 本机 `agentre.db-wal` 1.4 GB 而 `wal_autocheckpoint=1000`（4 MB），
  指向一个长命读者卡住 checkpoint，与存储格式无关，按 bug 单独查。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `internal/pkg` 下的"事件 → 块"累积器 | 同一条事件序列在两个宿主上累积出同一份块（含 thinking 穿插顺序、原地修补、孤儿 tool_result 丢弃） | `chat_svc/turn/accumulator_test.go` 整套可随包迁移 |
| `internal/pkg` 下的"块 → 帧"投影 | 每种块类型投影出的帧数与内容；未知块走 `UnrecognizedBlock` 不丢 | `chat_svc` 现有 `replay_canonical_test.go` / `character_*_test.go` |
| 帧编号的持久性（宿主级） | 同一段内容在宿主重启前后 seq 不变；原地修补新增的帧取末尾新号，不挤动既有编号 | 无——这是本轮新增的不变量，也是问题 B 的回归锚 |
| 补齐 RPC（daemon handlers + 桌面端对称实现） | 任意游标补回缺失段且不跳号；高水位复位与最老 seq 复位两条自愈路径 | `internal/daemon/handlers/session_catchup_test.go`、`chat_svc/remote_catchup_test.go` |
| 两级帧的协议边界 | 预览帧不推进游标、不进入去重；持久帧覆盖预览内容后对端呈现一致 | 无 |
| 转录仓储（sqlmock，一份，两个宿主共用） | 消息 / 块的写入、checkpoint 差分、按定位键与类型的点查、按对端指纹收窄的删除 | `chat_repo/message_block_test.go` 整套可随包迁移 |
| 复用的防漂移守卫 | 仓里不存在第二份块拆分 / checkpoint 差分 / 块→帧投影；`chat_repo` 不再持有消息与块 | 先例：AGENTS.md 记载的"第二个 `wire.pb.go` 出现即判红"守卫 |
| 集成（daemon ↔ 桌面端） | 一轮远端执行落成的转录，在两个宿主上内容等价；断连重连后对端不重不漏 | `internal/daemon/integration_test.go` |
| 共享包的既有守卫 | 方法集变动必须伴随协议版本抬升；两份 `event-kinds.gen.ts` 与 Go 词表同步；`agentre-ui` 不反向依赖 `agentre-wire` | `internal/pkg/wireversion` 的指纹守卫、`tsgen_test.go` 的 `TestGeneratedTSFresh`、`agentre-ui` 的 `boundary.test.ts` / `public-api.test.ts` |

无法自动化的部分：浏览器控制台上"逐 token 流式仍然成立、补齐后转录不重不漏"需要一次真实
联调观察（环境拓扑见项目记录：控制台与 agentred 在 coding.local）。收尾时以运行时观察补上，
不以自动化测试替代。

## Open questions

（无）
