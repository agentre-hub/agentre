# 附件在转录里画成一张图,发送侧立得住总量上限

> Status: Approved
> Owner: agentre(wire 契约 / agentred 投影 / 共享转录包)+ agentre-server(钉 pin 与消费侧守卫)
> Last updated: 2026-09-07

**Objective:** 让**控制台经中继发给远端 agentred** 的那张图,在转录里画成一张图并归在用户自己名下;并让「一次贴得太多」变成一句就地的拒绝,而不是把那台机器的全部会话一起打断线。

**主线是哪一条:** 浏览器控制台 → 中继 → 远端 agentred。这条路上三个问题同时成立——控制台的转录**只能**从帧现折(它没有本地消息表可读)、浏览器发的图必过中继那一跳、agentred 与控制台各自独立升级。桌面端做宿主时同样接得到经中继来的图(`chat_svc.RunPeerSession`),所以它作为**宿主**在主线内;桌面端**本机**拖图跑本机 runtime 不在主线内(理由见下面「哪几跳有上限」)。

**Hard invariant:**

1. **块级帧不回退。** 一块一帧、用户消息可占多帧(`2026-09-05-transcript-storage-alignment`),游标闸门按最低号推进(`2026-09-07-host-transcript-user-input` 决策 4)。本轮新增的帧种类照这条走,不把附件与正文合成一个帧位。
2. **不识别的块仍如实呈现。** R8 兜底(`UnrecognizedBlock`)保留,本轮只是让 image 不再需要它,不是拆掉它。
3. **超载不得拆掉物理连接。** 任何输入侧的量都不许再走到 1009 那条路上。
4. **image 帧不得走 `$proto` 逃生路。** `2026-09-07-journal-payload-json`(agentre-server)把「`wireview` 投影不出来的帧」兜成 `{"$proto": "<base64>"}`,并写明「逃生路是第二道闸,不是主路」。落进那条路的后果不是报错而是**每一张图都绕行**——库里存的是一坨 base64 protobuf,那一列存在的理由(可直接读)当场失效。

## Problem

1. **控制台里一张图画成一段 base64 文本,而且署名成助手说的。**
   `internal/pkg/transcript/projection.go:160` 的 `EventForStoredBlock` 在 `message.Role == "user"` 这一支只认 `text` 与 `display_text`;image 块因此落 R8 兜底,发成 `agentruntime.UnrecognizedBlock{BlockType:"image", Data:<原块 JSON>}`。共享包 `frontend/packages/agentre-ui/src/transcript/frames.ts:881` 收到它之后 `openAssistant(st, sessionId).blocks.push({type:"notice", text: \`${blockType} ${pretty(data)}\`})`。

   实测(把 agentred 真会发的那一帧喂进 `reduceFrames`)得到:

   ```json
   [{"role":"assistant","blocks":[{"type":"notice",
     "text":"image {\n  \"media_type\": \"image/png\",\n  \"source\": {\n    \"inline\": \"iVBORw0KGgoAAAANSUhEUg\"\n  }\n}"}]}]
   ```

   两处都错:画的是 notice 不是图(一张 3MB 的图会在转录里铺开成四百万字符),而且落在**助手**消息上,可那张图是用户贴的。包里画图的 `ImageBlockView`(`transcript-row-view.tsx:636`)读的是 `block.image.dataUrl`,与这个载荷形状对不上,永远走不到。

2. **贴两张 5MB 的图,会把那台机器上所有会话一起打断线。**
   输入侧只卡单张:共享包 `chat-composer.tsx:94-95` 的 `MAX_IMAGE_COUNT = 4` / `MAX_IMAGE_BYTES = 5MB`,桌面端 `internal/service/chat_svc/chat.go:62-63` 的 `maxSendImages` / `maxSendImageBytes` 是同一对数。**没有任何一处卡总量**,而 `runtime.run` 把一条消息的所有图装在同一个请求里。

   `pkg/wire/wirelimits/wirelimits.go` 的 `MaxPayloadBytes` 是 10 MiB,base64 膨胀 4/3,于是原始总量超过约 7.5 MB 即超限。后果写在那个文件自己的注释里:

   > 超限时 gorilla 回 1009 并让读循环出错,于是**整条物理连接**被拆掉,而 daemon 那条链路上跑着那台机器的全部虚拟通道,所有会话一起断线重连。

   也就是说这不是「这一次发送失败了」,是那台机器上**每一条**会话一起重连。4 × 5 MB 是输入侧明确允许的组合。

3. **老形态不是过渡期现象,是永久的。**
   `session.pull` 读的是 agentred 自己的**通知日志**(`internal/daemon/handlers/session_catchup.go:258` 的 `Journal.ListSince`),原样重放它**当时发出**的那一份——不是从转录重新投影。而那份日志只追加、永久保存(`2026-09-07-journal-payload-json` 决策 5 引的原话:「agentred 不再回收任何一行」)。

   于是:**一张在 agentred 升级前发的图,永远是 `UnrecognizedBlock` 形态**,就算两端后来都升级了,重拉历史回来的还是它。而 `migrations/202609070101_journal_payload_json.go` 刚刚 `TRUNCATE` 了服务端这张表并把 `latest_seq` 归零,由镜像全量重拉——升级期内所有历史图会以老形态重新落库一遍。

   只认新事件的话,这些图**永远**停在问题 1 的样子,不是"等大家升级就好了"。

## Actors and user stories

1. 作为在**控制台**里看一条跑在远端 agentred 上的对话的人,我要看见我贴的那张图本身,而不是一段 base64,这样我才能确认我给模型看的是哪一张。
2. 作为在**桌面端 Peer Tab** 里看另一台机器那条对话的人,我要的是同一件事——那一栏的转录同样从帧现折。
3. 作为一次贴了好几张大图的人,我要在**按下发送之前**就被告知贴超了,而不是让那台机器上每一条会话一起掉线。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | image 有**自己的事件 kind**,不塞进 `UserMessageEvent` 的字段里 | 块级帧是 `2026-09-05` 的既定契约,`2026-09-07` 决策 4 还专门为「用户消息可能占多帧」把游标闸门从「最高号」改成「最低号」。Rejected: 把附件挂进 `UserMessageEvent` —— 帧粒度回退成消息级,刚改完的闸门立刻失去意义 |
| 2 | reducer 按**相邻性**把 image 帧并进当前那条用户消息 | 两个宿主的块序一致:先文本后附件(`chat_svc.go:1135` 的 `userBlocksForSend`、`daemon.go:1628` 的 `AddText` 后追加)。而 `frames.ts:464` 的 `pushUserMessage` 每次都新建一条消息并把 `st.open` 置空,不合并就是一句话两个气泡。Rejected: 让事件带 messageId —— 帧上没有这一格,加它等于为一个靠块序就能定的事扩协议 |
| 3 | 只贴图不打字时 image 帧是**首帧**,此时新建那条用户消息 | 没有文本就没有 `user_message` 帧,而这一档是发得出去的一条(提交键在有图时启用)。同一条规则覆盖两种块序,不分叉 |
| 4 | 新 reducer **同时认** `blockType === "image"` 的 `UnrecognizedBlock`,落点与新事件相同 | 问题 3:`session.pull` 原样重放 agentred 日志里**当时**那一份,而那份日志永久保存 —— 老形态因此是永久的,不是过渡期的。这不是"少等一会儿"的优化,是**唯一**能让升级前那些图画出来的办法。Rejected: 只认新事件 —— 那些图永远停在 notice |
| 8 | image 事件的字节走 `messageMap` 的 `BytesKind → base64` 默认投射,**不进 `putRawJSON`** | `putRawJSON` 是给「本该是 JSON 的字节」用的(`input` / `canonical` / `meta` / `data`),它解不动时包成 `{"$b64": ...}` 消歧义。图片字节从来不是 JSON,一定解不动,于是一定被包 —— 白给消费方多一层壳,而 `source.inline` 这一格的语义从来不含糊。Rejected: 一并走 `putRawJSON` —— 为一个不存在的歧义付一层包装 |
| 9 | 判别值只写在 `.proto` 的 `(agentre.wire.event_kind)` 选项上,`wireview` 不加分支 | 已核实:`pkg/wire/eventkind/eventkind.go:40` 的 `Of` 纯反射读那个选项,`wireview`(在 **agentre-server**)的 switch 只服务需要特例的 kind。决策 8 既然要默认投射,这一档就什么都不必加 —— 硬不变量 4 因此由「proto 选项标对了」保证,而不是由记得改另一个仓保证。`TestNotificationViewCoversEveryRuntimeEventCase` 是那一侧的兜底守卫 |
| 5 | 附件总量上限**从 `wirelimits.MaxPayloadBytes` 推导**,取其 2/3 作为原始字节预算 | `wirelimits.go` 自己记着「三处曾经不同源」的教训,新写一个字面量就是再犯一次。2/3 原始 → base64 后占满 8/9,余下 1/9(约 1.16 MB)给正文、提及与其余字段。Rejected: 取一半 —— 更安全但把今天能用的组合(两张 3 MB)也拒掉,是一次没有理由的功能收缩 |
| 6 | 这个上限**生成**到 TS,不手抄 | 与 `event-kinds.gen.ts` 同一条理由:只有生成物才谈得上「漂移在机械上不可能」,而这个常量正是漂移过的那一个。Rejected: 前端手写 + 守卫测试 —— 本仓已有生成器,多一种同步方式没有收益 |
| 7 | 超量在**共享包 composer**就地拒绝,两个宿主各自再兜一道 | 用户要的是「按下之前就知道」;而宿主不能信任客户端(老版本控制台、别的实现)。宿主侧拒绝整轮,与「附件解不开就拒绝整轮」(`2026-09-07-host-transcript-user-input` 决策 3)同一条纪律 |

## 一张图怎么过帧

前置:一条用户消息已经落成 `[文本块, 附件块...]`(两个宿主同形,见决策 2 的依据)。动作:宿主按块投影这条消息。可观察结果:每个 image 块投影成一帧 image 事件,载荷是这个块**原本的载荷**——媒体类型与字节来源,不重新编码、不改键名。

字节来源沿用 `blocks.BlobSource` 已有的两格:内联字节,或一个 URL。今天所有产生方只填内联那一格,URL 恒空;规格不为 URL 那一档新增产生方,只要求消费方遇到它时按 URL 取图而不是当作缺失。

失败:两格都空的块画不出图。这时**不静默跳过**——按 R8 的同一条纪律如实说这里有一张取不到的图,而不是让转录里凭空少一块。

## reducer 怎么把它归到那条用户消息上

前置:控制台或 Peer Tab 正在归约一段帧流。动作:收到一帧 image 事件。可观察结果:

- 若这一轮**已经**开了一条用户消息(紧邻的前一帧是它的 `user_message`,中间没有任何助手帧),图块追加到**那一条**上,排在文本块之后;
- 否则新建一条用户消息,图块是它的第一块。

也就是说 `[文本, 图]` 画成一个气泡里一句话加一张图,`[图]` 画成一个只有图的气泡,两种都归在「我」名下。此前无论哪种都画成助手名下的一段 base64。

助手帧到达即关闭「当前这条用户消息」,与 `st.open` 对助手消息的既有语义对称——一轮的正文开始之后再来的图不属于上一条提问。

## 兼容:老帧与老宿主

前置:帧流里混着老形态(`UnrecognizedBlock`,`blockType === "image"`)与新形态。动作:同一个 reducer 归约。可观察结果:两种形态落到同一处、画出同一张图,读者看不出这条对话是哪一版宿主写的。

`UnrecognizedBlock` 的其余 `blockType` 行为一个字不改,仍落 notice——本轮只把 image 这一支接走。

方向相反的那一档(**新**宿主发 image 事件、**老**控制台不认)落到 reducer 既有的 `default` 分支,画成 notice,与今天一样;它不是新的坏,只是旧的坏没有变好。因此发布顺序是先出共享包(消费方先认得),再出宿主。

## 哪几跳有上限,哪几跳没有

`MaxPayloadBytes` 只挂在 wire 传输上:中继链路(`relaytransport/hub.go:129` 的 `defaultMaxFrameBytes`)与桌面端 ↔ agentred 的直连 WS(`daemon.go:715`)。桌面端跑**本机** runtime 是进程内调用,一个字节都不过这层。

这条区分决定了同一个上限在两处**说的不是同一件事**:

- **过中继/直连那几跳**(控制台发图、桌面端驱远端 agentred、控制台发给桌面端做宿主的会话):超限会拆掉整条物理连接,连累那台机器上全部虚拟通道。这是本轮要挡的事故。
- **桌面端本机那一跳**:不过 wire,超限不会断任何连接。上限在这里只是「一条消息别大得离谱」的一致性措施,不是事故防线。

因此上限的**取值**由中继那一跳决定(它是最紧的),但对本机那一跳同样生效——一条消息能带多少附件不该因为它这一轮恰好跑在哪儿而不同,那会让同一段草稿在换执行目标之后突然发不出去。

## 发送侧的总量预算

前置:用户在输入框里贴图。动作:每贴一张就结算这一条消息的**原始字节总量**(不是 base64 后的量,那是传输形态)。可观察结果:

- 总量在预算内:照常贴上,与今天一样;
- 超出预算:这一张不被收下,就地一句说明说的是**总量**超了(与既有的「张数超了」「格式不支持」并列,不复用它们的措辞——三件事的处置不同);
- 已经贴上的不被回收:超限的是**这一张**,把用户先前贴的图一起清掉是另一种数据丢失。

宿主侧各自再按同一个预算校验一次:agentred 的 `runtime.run` 参数解码(主线的接收端),以及桌面端做宿主时的对端会话入口(`chat_svc` 那条同样收经中继来的图)。超出时拒绝整轮并给出该轮参数无效的错误——不截断附件照跑,理由与解不开时相同:静默跑一轮「用户以为发了图、模型没看见」的对话,比一个明确的错误更糟。

单张上限(5 MB)与张数上限(4)不变。本轮只补总量这一维。

## Out of scope

- **blob 外置**(把字节挪出帧、改走独立取图端点)。已确认接受现状:字节继续留在 `agent_session_notification_journal.payload` —— 该列已于 `2026-09-07-journal-payload-json` 改为原生 `json`,存的是 `{method, params}` 视图,图的 base64 落在 `params` 里(老形态在 `params.data.source.inline`,新形态在该事件自己的字节格)。一条带大图的对话首屏因此会超出 `session_mirror_read.go:39` 的 `tailBytes = 256 KB` 预算。这是一个已知且被接受的代价,不在本轮。
- **附件的文件名**。`blocks.ImageBlock` 只有 `MediaType` 与 `Source`,没有名字这一格,文件名在两个宿主落库那一刻就丢了。因此转录里图的替代文本只能退到媒体类型。改它要动块结构,是另一件事。
- **非图片附件**(PDF、文本、压缩包)。今天没有任何一处产生它们:输入侧只收 png/jpeg/webp。
- **缩略图、懒加载、超大图降采样**。`2026-09-07-host-transcript-user-input` 已把呈现细节列为 out of scope,本轮不改这一条。
- **中继单帧上限本身**。本轮是让输入侧不去撞它,不是抬高它。

## 基线依赖

本规格建立在「附件已经落成两个宿主用户消息里的块」之上(`2026-09-07-host-transcript-user-input` 决策 3)。**该依赖已于 `60267146` 落地**(`internal/pkg/transcript/userblocks.go` 的 `TurnAttachments` 及其两个调用点),写这份规格时它还在飞,批准时已提交。

服务端那一侧的存储形态同期改过一轮(`agentre-server` 的 `2026-09-07-journal-payload-json`,迁移 `202609070101`):`agent_session_notification_journal.payload` 从 protobuf `longblob` 改成原生 `json`,存 `wireview` 的 `{method, params}` 视图。本规格的硬不变量 4 与决策 8、9 都是从那一轮的形状推出来的。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `transcript.ProjectMessages` / `EventForStoredBlock` | 一条 `[文本, 图]` 的用户消息投影出文本帧 + image 帧,载荷保住媒体类型与字节来源;两格来源都空时不静默跳过 | `internal/pkg/transcript` 既有投影测试 |
| 共享包 `reduceFrames` | 三种输入各自的落点:`[user_message, image]` 并成一条用户消息、单独 image 起一条新的、`blockType==="image"` 的 `UnrecognizedBlock` 与新事件同落点;其余 `blockType` 仍落 notice | `frames-vocabulary.test.ts`(词表穷举)、`frames.ts` 既有归约测试 |
| 共享包 `ChatComposer` | 贴到超过总量预算时:这一张被拒、说明说的是总量、先前贴的仍在 | `chat-composer.test.tsx` 既有的张数/格式拒绝用例 |
| `chat_svc` 发送入口 | 宿主侧总量校验:超量拒绝整轮,不截断附件 | `chat_test.go` 既有的图片能力校验用例 |
| agentred `runtime.run` 参数解码 | 同上,在另一个宿主上 | `handlers/runtime_test.go` 的 `decodeUserBlocks` 用例 |
| `wireview.Notification()`(**agentre-server**,钉新 pin 之后) | 新 kind 投影得出 `{kind, ...}` 视图而**不落 `$proto`**;字节格投成裸 base64 而不是 `{"$b64": ...}` | `TestNotificationViewCoversEveryRuntimeEventCase`(穷举守卫;本轮预期它无需改动即绿,红了说明 proto 选项没标对) |
| 生成器 `TestGeneratedTSFresh` | 新增的事件 kind 与预算常量两处产物逐字节一致 | 既有守卫,无需新写 |

**自动化覆盖不到的一处:** 「一张真图在浏览器里确实画出来了」——jsdom 不做布局也不解码图片,单测只证得到 DOM 上有一个 `src` 正确的 `<img>`。真正看一眼要在 `agentre-server` 的 `e2e/` 里驱一次(`pnpm serve` + `pnpm drive`,证据是截图),按该仓 `docs/verification.md` 的 scratch 流程走。

## Open questions

无。
