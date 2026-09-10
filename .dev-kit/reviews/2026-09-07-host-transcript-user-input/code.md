# 代码评审回执 —— 2026-09-07 host-transcript-user-input

轴:代码评审(正确性 / 边界 / 错误处理 / 安全 / 死代码 / 测试价值 / 跨任务漂移)。
需求符合性归另一条轴,本回执不涉及。

评审对象(逐个 `git show`,不用提交区间 —— `dev` 上混着另一个会话的提交):

```
8fd0d613  ✨ 附件落进两个宿主的用户消息,并随这一轮送去执行
338151d4  ✨ 运行应答回传用户消息的帧号区间,游标闸门改比最低号
8012c2b3  ✨ agentred 做宿主时把插话落进自己的转录并取号
c5137d17  🎨 补上 8fd0d613 / 338151d4 漏掉的 import 排序
9eafeed9  ✨ 消费方不再从预览帧落插话:落库交给宿主发来的持久帧
e58b589f  ✨ 协议窗口从 0.2.0 移到 0.3.0
60267146  🐛 附件那一格别把用户的话记两遍:userBlocks 里已经有它
```

另一个会话的提交(`8f0da0a0` / `69311bad` / `4d20023a` / `c1c1f2b8` / `38bea2c1`)与它的
未提交文件(`docs/specs/2026-09-06-device-port-forward.md`)一个字没碰。

---

## A. 接手的三处半成品

上一轮这条轴留在工作树里的改动,方向核实无误,全部保留并补完。

### A1. `internal/service/chat_svc/turn_run.go` —— 认领从 `applyLive` 搬到 `applyDurable`

`turnRun.consumeEvents`(turn_run.go:149)在两级帧那一路把持久帧送 `applyDurable`、预览帧送
`applyPreview`(→ `applyLive(preview=true)`),只有本机后端(`previews == nil`)才走
`applyLive(preview=false)`。9eafeed9 把 `case agentruntime.UserMessageEvent:` 加在 `applyLive`
的 switch 里、再用 `if preview { return }` 挡住预览 —— 于是**远端那条唯一会收到该事件的路径上
一次都跑不到**:生产上是死代码,插话在消费方转录里一个字不剩。

`autonomousTurnRun` 在同一次提交里认领的位置是对的(`applyDurable`,autonomous_turn_run.go:399),
两条路本就应当同形。保留搬移。

核实过认领在生产上真的到得了:
- 产生方 `internal/pkg/transcript/projection.go:170` `EventForStoredBlock` —— user 角色的
  text / display_text 块折成 `UserMessageEvent` 并带上 `sourceDevice` / `sourceDeviceName`;
- 过线 `remote/protowire/event.go:167 / :285` 编解码都带这两格;
- 判据(来源标识)在桌面端→agentred 这条上不会误伤自己那条提问:`remote.Runtime` 的
  `RunParams` 不填 `SourceDevice`(全仓只有 `peer_svc.RunFresh` 填,那条走的是对端桌面的
  `RunPeerSession`,不经 `RuntimeHandlers.Run`),所以 agentred 的开轮用户行不带来源,
  `um.SourceDevice == ""` 早退成立。

### A2. `internal/service/chat_svc/steer_segmentation_test.go` —— 删掉两条走 builtin 的用例

`TestSend_DurableUserMessageWithSourceSegmentsTheTurn` /
`TestSend_DurableUserMessageWithoutSourceDoesNotSegment` 用 `SwapRuntimeForTest(TypeBuiltin, …)`
直接吐 `UserMessageEvent`,验的是 `previews == nil` 那条本机路径 —— 而缺陷在远端路径,且本机
后端从不产生这类事件。它们绿着但不验任何生产行为。连同只服务于它们的 `expectPlainTurn` 一并删。

### A3. `internal/service/chat_svc/turn_run_durable_test.go` —— 补完并跑绿

改为直接调 `turnRun.applyDurable`(两级帧的真实生产入口)。任务书提到它需要
`nowForSegment` / `turnRun.ctxForTest` 两个不存在的辅助物 —— 工作树里那一版已经不再需要它们
(用 `testutils.Database(t)` 拿 ctx、`svc.newTurnContext` 装配 turnCtx),`go vet` 与
`go test` 均通过,未再引入新辅助物。

三条用例:带来源 → 分段;不带来源 → 一行不落;工具在途 → 攒着不分段。

---

## B. 本轮新发现并修复的缺陷

### B1(高)agentred 做宿主时,插话会在两种情况下被整段丢掉

`internal/daemon/handlers/runtime_transcript.go`,由 8012c2b3 引入。

`turnTranscript.flushPendingSteers` 把「工具在途就什么都不做」写在函数**内部**,而唯二的调用点
都在 `observe` 里。于是:

1. **没有「推迟至多一个事件」的兜底。** 桌面端做宿主时这条规则早就有
   (`chat_svc.turnRun.applyLive`:下一帧不是 `tool_result` 就不再等,锚
   `TestSend_SteerConsumedSplitsAnywayWhenPendingToolNeverResolves`),因为存在
   「在途 tool_use 的结果根本不走流」的工具(AskUserQuestion 这类)。agentred 只在
   `HasOpenToolUse()` 转假的那一刻才 flush —— 这类工具下它永远不转假。
2. **`finish` 不 flush。** `pendingSteers` 于是随 `turnTranscript` 一起消失:那段插话已经被后端
   消费进这一轮的上下文(它进了模型),却在转录里一个字都不剩,持久帧也没取号,消费方补齐
   同样拿不到。这正是 8012c2b3 自己在说的那条「2026-05 不变量 1 的漏」,只是触发条件更窄;
   而桌面端做宿主时是落的(`turnRun.finalize` 开头无条件 `flushPendingSteers`)。同一件事
   两台宿主两种结果。

**动作**:把「工具在途先攒着」的判据移到调用点,并按桌面端补齐两处无条件落地(「不是
tool_result」与收口)。三处调用点现在与 `chat_svc.turnRun.applyLive` / `finalize` 一一对应。

**红→绿**(先写测试,先看它红):
- `TestIntegration_SteerWhileToolNeverResolves_SegmentsAnyway`(internal/daemon/integration_test.go)
  先红:`should have 4 item(s), but has 2`,实际落成
  `user:[hi] | assistant:[tool_use + text "after-steer"]` —— 插话不见,前后两段并成一块。
- `TestIntegration_SteerAsTheLastEventOfTheTurn_StillLandsAtFinish` 先红:`Condition never satisfied`,
  实际落成空(只有两行)。

### B2(中)分段落库失败时插话被吃掉

同文件。`flushPendingSteers` 在调 `SegmentTurn` **之前**就 `t.pendingSteers = nil`,两个失败分支
只记一条 `Warn` 就 return:那段插话此后既不在库里也不在队列里。函数注释当时写的
「内容一块不丢」只对 assistant 正文成立,对插话不成立。

**动作**:两个失败分支把 steers 退回队列,由下一个时机(最后是 `finish`)重试;注释改写成事实。

**红→绿**:`TestRuntime_Run_GivenSegmentTurnFails_ThenTheSteerIsRetriedNotDropped`
(internal/daemon/handlers/runtime_test.go)先红:`expected ["插一句","插一句"], actual ["插一句"]`。

---

## C. 探针实验(「拿掉被测代码它还绿不绿」)

每一条都真的改了源码跑一遍,跑完立刻还原。

| 探针 | 拿掉的东西 | 观测 |
| --- | --- | --- |
| A | `applyDurable` 里整段 `UserMessageEvent` 认领 | 红:`…SegmentsOnTheDurableStream` 与 `…WhileToolInFlight` 各 `should have 1 item(s), but has 0` |
| B | `if um.SourceDevice == "" { return }` | 红:`…WithoutSource_DoesNotSegment` —— 「不该分段却落了 role=user / role=assistant」+ emit 了 `steer_consumed` |
| C | `applyDurable` 里的 `!t.acc.HasOpenToolUse()` 闸 | 红:`…WhileToolInFlight` —— 工具在途时分了段,`pendingSteers` 空 |
| D | `turnTranscript.finish` 里新加的 flush | 红:`…SteerAsTheLastEventOfTheTurn…` `Condition never satisfied` |
| E | `observe` 里新加的「不是 tool_result 就不再等」 | 红:`…SteerWhileToolNeverResolves…` `Condition never satisfied` |
| F | `observe` 里 SteerConsumed 那一处的 `HasOpenToolUse` 闸(证明判据搬家没丢) | 红:`…SteerConsumedWhileToolInFlight_SegmentsAfterTheToolResult` —— `tool_use` 与它的 `tool_result` 被切到两条消息 |
| G | 两处 `t.pendingSteers = steers` 回退 | 红:`…GivenSegmentTurnFails…` 只拿到一次 `插一句` |

**探针 D 第一次跑出的是绿的**,这是本轮唯一一次自己踩进「断言恒成立」:第一版
`…SteerAsTheLastEventOfTheTurn…` 的 fake runner 在插话之后照旧发了一条 `Done{}`,于是
探针 E 那条规则先把它 flush 掉了,`finish` 的兜底根本没被跑到。改成「`SteerConsumed` 就是流上
最后一件事、通道紧接着关掉」之后探针 D 才判红。

另一次自查也有收获:`…GivenSegmentTurnFails…` 第一版靠日志里的
`"handlers.RuntimeHandlers.fanout: session ended"` 等收工,而全局 logger 是共用的 ——
它会撞上前面用例泄漏出来的 fanout 协程,在插话还没进 `SegmentTurn` 时就提前收工(实测:
`make test` 那次绿、紧接着单跑那次红)。改成等自己这一轮的 `FinishTurn` 被调到(它排在
`flushPendingSteers` 之后),连跑 5 次稳定绿。

---

## D. 看过但**未**动的观察

1. **`internal/daemon/handlers` 在 `-race` 下有 3 条红,与本轮无关。**
   `captureRuntimeLogs` 换全局 logger,撞上前面用例泄漏出来的 fanout 协程,
   `logger.SetLogger` 与 `sessionEmitter.emit` / `logFanoutSummary` 数据竞争。
   在 worktree 里逐个 checkout 验过:`a3a0c343`(本轮全部提交之前)、`8fd0d613`、`338151d4`、
   `8012c2b3`、`9eafeed9` 都是 2 条;第 3 条(`TestRuntime_Run_GivenPeerSubmittedTurn…`)
   来自**另一个会话的** `c1c1f2b8`。`make test-backend` 跑的是 `go test`(不带 `-race`),
   所以门禁一直是绿的 —— 本轮不动它:它既不是这几个提交引入的,修它也要动别人的用例。
2. **`send` 的图片能力校验对 `peerBlocks` 偏保守**(chat.go:967 → :1037)。
   `imageBlocks = append(imageBlocks, req.peerBlocks...)` 之后按 `len(imageBlocks) > 0` 要求
   `CapImageInput`,而 `peerBlocks` 理论上可以含非图片块。60267146 已经把实际会发生的那一种
   (与 `userText` 同字的首个文本块)去掉了;今天生产上的用户消息只由
   `userBlocksForSend`(文本 + 图)与 `persistConsumedSteers`(纯文本)产生,所以剩下的是
   理论口。落库那一侧另有 `messageHasImage`(chat.go:2318)按真实块判,不受影响。未动。
3. **`transcript.TurnAttachments` 只认 `TextBlock`,而 `StampUserMessageSource` 认
   `text` 与 `display_text` 两种。** 今天 `buildRunRequest` 交出的首块必是 `TextBlock`
   (`userBlocksForSend`),`textOfMessage` 也只认这一种 —— 两者严格对偶,不成问题;
   将来若有 `display_text` 打头的用户消息,这一格会漏。记在这里,未动。
4. **`adoptDispatchedUserMessageSeq` 的闸门被放宽了**(338151d4):旧规则只收
   `seq == cursor+1`,新规则收任意 `minSeq <= cursor+1` 并直接跳到 `maxSeq`。一个报错号的
   宿主因此能让消费方永久跳过一大段帧(旧规则最多跳 1 帧)。判为可接受:区间由
   `AllocateFrameSeqs` 连续分配、只覆盖用户那一行;而且这些帧本就只可能来自同一个宿主 ——
   它不发和让人跳过是同一件事,不构成越权。未动。
5. **`e58b589f` 的跨仓后果**:`agentre-server` 自己那份 `internal/pkg/wireversion` 仍是
   `0.2.0`,桌面端/agentred 抬到 `0.3.0` 后握手会被拒。提交说明里已写明须锁步,属交付轴,
   本轮不动。
6. 死代码复扫:本轮新增的 `transcript.TurnAttachments` / `transcript.StampUserMessageSource` /
   `TranscriptPort.SegmentTurn` / `RunAck.UserMessageMinSeq` / `SendResponse.UserMessageMinSeq`
   / proto 的 `user_message_min_seq` 都有生产调用方与消费方,除 A1 那一处外没有同类。

---

## E. 门禁

```
$ make -C /Users/codfrm/Code/agentre/agentre test
Test Files  418 passed (418)
     Tests  4994 passed (4994)
EXIT=0        # go test 段 0 条 FAIL

$ make -C /Users/codfrm/Code/agentre/agentre lint
golangci-lint run --timeout 10m
0 issues.
cd frontend && pnpm lint      # eslint 无输出
EXIT=0
```

任务书里提到的 `frontend/packages/agentre-ui/src/index.ts:1092` 那条既有 prettier 红**已经不在了**
(本轮之前被别处修掉),所以 `make lint` 这次是 EXIT=0,不是预期中的非零。本轮没碰前端。

## F. 提交

- 单个提交,`git commit --only <显式路径>`,只含:
  `internal/service/chat_svc/turn_run.go`、`internal/service/chat_svc/steer_segmentation_test.go`、
  `internal/service/chat_svc/turn_run_durable_test.go`、
  `internal/daemon/handlers/runtime_transcript.go`、`internal/daemon/handlers/runtime_test.go`、
  `internal/daemon/integration_test.go`、本回执。
- 最终 HEAD:本回执所在的这一个提交本身 —— 它是 `38bea2c1` 之上、本轴留下的唯一一个提交
  (`git log -1 --format=%H` 取到的就是它;sha 写不进它自己承载的这份文件里)。
