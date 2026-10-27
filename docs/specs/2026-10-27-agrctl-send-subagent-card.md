# agrctl send 子代理卡

> Status: Approved
> Owner: desktop app + agentred maintainers（agentre-server 只抬 wire 钉）
> Last updated: 2026-10-27

**Objective:** Agentre 会话里的 agent 用 shell 跑 `agrctl send --agent <X> --wait "<任务>"` 时，父转录里那条 shell 调用原地变成子代理卡。卡片实时显示子会话的工具调用、模型、用量和状态，结束后显示子任务的最终文本，刷新、重开、远端会话里都一样。

**Hard invariant:**
- 不挂错卡：不能确定子会话对应哪条 shell 调用时，就保持普通 shell 行。宁可不升级，也不能把子会话挂到别的调用上。
- 卡片状态不说谎：子任务还在跑时不显示已结束或已取消；子任务结束后不再显示运行中。
- 现有行为不变：`agrctl send` 的请求和响应、退出码、stdout，「客户端断开即停止子会话」，Claude / Codex / Pi 原生子代理卡，后台任务面板和停止按钮，以及不升级时的 shell 行，都不退化。

## Problem

1. **派出去的子任务在父转录里看不见。** `serveSend` 新建子会话、起轮；带 `--wait` 时阻塞到结束，把最终文本写进响应（`internal/service/ctl_svc/handler.go:146-252`）。父转录里只有一条普通 shell 行，子 agent 读了什么、跑了什么都看不到，也没有入口跳到子会话。（已验证）
2. **服务端知道调用方，却没用上。** 会话里的 agrctl 带的是会话级 token，服务端能解出发起方的 (agent, session)（`internal/service/ctl_svc/caller.go:44-74`），但 `serveSend` 拿不到这个身份（`handler.go:56-57`）。（已验证）
3. **卡片类型只看 canonical。** 前端只有在 `canonical.kind === "agent.spawn"` 时才画子代理卡，嵌套块挂在其他调用下面会被直接过滤（`frontend/packages/agentre-ui/src/transcript/transcript-rows.ts:20-24, 234`）。重载时 canonical 由工具名和参数重新推导，shell 调用推不出 spawn（`internal/service/chat_svc/message_projection.go:84-93`）。（已验证）
4. **嵌套事件不去重。** 同一条嵌套 ToolCall / ToolResult 应用两次，会产生两个块（`internal/pkg/transcript/handlers/raw_tool.go:28-36, 79-90`；`internal/pkg/transcript/turn/accumulator.go:57-75`）。（已验证）
5. **CLI 自己会往同一个 ID 上写子代理状态。** Agentre 的代码注释说 Claude CLI 对每条 Bash 都发 `task_type:"local_bash"` 帧；前台 Bash 今天不出问题，只是因为 Started 被跳过、后续的 Progress / Done 找不到 overlay（`internal/pkg/transcript/handlers/subagent.go:114-142`）。一旦给这条 Bash 建了 overlay，CLI 的 Progress / Done 就会覆盖它的状态、摘要和计数（`subagent.go:255-270, 335-370`）。后台 Bash 的完成通知里，summary 是「Background command completed (exit code 0)」这类退出码文本，不是 stdout。（已验证：代码与 fixture。另：用 Claude Code 2.1.296 `-p` 模式实测一条 3 秒的前台 Bash，没收到 `local_bash` 帧，与注释不符。）
6. **远端会话的嵌套块在桌面端会丢。** agentred 发来的持久帧投影到桌面端时，`nested_tool_use` / `nested_tool_result` 不在 `EventForStoredBlock` 的分支里，会落成 `UnrecognizedBlock`（`internal/pkg/transcript/projection.go:161-174, 240-330`）；桌面端 dispatcher 对没注册 handler 的事件直接 `return nil`（`internal/pkg/transcript/turn/dispatcher.go:52-55`）。今天远端 Claude 原生子代理卡里的嵌套调用同样受影响。（已验证：代码路径。未实测：远端会话的实际界面。）
7. **agentred 上没有「轮结束后更新卡片」的路径。** 桌面端靠 `driveSubagentActivity` 和 `SubagentFlipper` 在轮结束后更新后台卡（`internal/service/chat_svc/subagent_activity.go:59-150`；`internal/service/chat_svc/dispatcher_adapters.go:224-248`）。agentred 的轮上下文里没有 flipper（`internal/daemon/handlers/runtime_transcript.go:123`），自主续轮的起始帧也不带 `CompletedTask`。所以远端的 Claude 原生后台子代理卡很可能收不了尾。（未证实：只读了代码；本机两个库里都没有远端会话，无法用数据证明。）
8. **Codex 默认沙箱挡住了 ctl 网关。** 用 `codex exec` 实测（codex-cli 0.162.1）：`workspace-write` 下连 `127.0.0.1` 返回 000，`danger-full-access` 下返回 200，`AGENTRE_CTL_TOKEN` 在两种模式下都能进 shell。Codex 后端的沙箱取自后端配置，留空就用 Codex 自己的默认值（`internal/pkg/agentruntime/runtimes/codex/session.go:395-397`）。（已验证）
9. **shell 超时和后台模式各不相同。** Claude Code 2.1.296：前台 Bash 默认 2 分钟、最多 10 分钟，到点直接杀进程，实测 `Exit code 143`；`run_in_background` 默认 30 分钟、最多 2 小时（二进制源码中的 `120000` / `600000` / `1800000` / `7200000`）。Pi 的 `bash` 没有默认超时，也没有后台参数。Codex 跑 40 秒的命令时，自始至终是同一条执行项，跑完才结束（`codex exec` 实测）。客户端断开时 `serveSend` 会停掉子会话（`handler.go:247-250`）。（已验证）

## Actors and user stories

1. As a `用户`，I want 在父会话里直接看到派出去的子任务在做什么、做到哪、结果是什么，so that 不用去侧栏里一个个找子会话。
2. As a `用户`，I want 刷新、重开会话，或在远端会话里，看到的都是同一张卡，so that 回看历史时信息不丢。
3. As a `Agentre 会话里的 agent`（Claude Code / Codex / Pi），I want `agrctl send` 的用法和返回不变，so that 已有的提示词和脚本照常可用。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 由服务端在收到 send 后绑定父轮里还开着的那条 shell 调用，原地升级成子代理卡 | 用户决定。只有服务端真的收到 send 才升级，和命令怎么写无关，三个后端共用一套。Rejected: 照搬 Pi 在 translator 里解析 shell 命令——要改三个 translator，复杂写法认不出，send 失败也会先冒出一张卡；Rejected: 只做跳转链接卡——看不到实时活动 |
| 2 | 只升级 `--wait`。不带 `--wait` 的保持普通行 | 用户决定。Rejected: 异步 send 也升级——父 agent 没在等结果，卡片的生命周期没有锚点 |
| 3 | 范围包括桌面端拥有的本机和远端会话，前台和 Claude 后台 Bash，一份 spec 交付 | 用户决定。Rejected: 先本机后远端分两轮；Rejected: 远端后台另开一轮 |
| 4 | 控制台拥有的会话不做 | 用户决定。它们的 send 走 agentre-server 的 `/v1/ctl/send`，要改 server 的业务逻辑。Rejected: 一并做 |
| 5 | Codex 按配置走：不开沙箱时升级；开沙箱时 send 连不上，保持普通行，agrctl 的报错里说明是沙箱挡住了本机网关 | 用户决定，有实测依据（Problem 8）。Rejected: Agentre 给 Codex 开沙箱网络——会打开沙箱内的全部网络，不只是本机回环；Rejected: 顺带覆盖 MCP `call_agent` |
| 6 | 卡片上提供「打开会话」，放在展开区 meta 行的右端；`--isolated` 的子会话不在侧栏里，不显示这个入口 | 用户决定。Rejected: 放进卡片头部做图标按钮——头部更挤，窄栏时任务描述被压得更厉害 |
| 7 | 前台卡没有停止按钮；后台卡沿用现有的停止按钮（走 `stop_task`） | 现有卡只在拿得到后台 taskId 时显示停止按钮。后台 Bash 有 taskId，停掉它会让 agrctl 退出、子会话被停，链路是现成的 |
| 8 | 匹配只看父轮的外层 shell 调用：Claude `Bash`、Codex `command_execution`、Pi `bash` | Pi subagent 扩展拉起的子进程继承了父会话的 token，它跑的 send 对应的是嵌套块；如果嵌套块也参与匹配，会和 Pi 的 tracker 抢同一张卡 |
| 9 | 前台还是后台，只按那条 shell 调用入参里的 `run_in_background` 判断，不看 send 请求和 shell 结果谁先到 | 后台 Bash 会立刻拿到结果，与 send 请求到达是竞争关系（Problem 9） |
| 10 | 前台卡用新的子代理 kind；后台卡保留 `local_bash` | `local_bash` 加前台会在 Started 时被丢弃；`local_agent` 会被当成后台任务；后台卡离开 `local_bash` 就会从后台面板、停止按钮、轮结束保护里消失（`handlers/subagent.go:62-73, 122-142`） |
| 11 | 写入分工：子会话带来的内容（嵌套步骤、模型、用量、工具数、子任务最终文本）归注入方；后台卡的状态归 CLI。子任务最终文本另存一个字段，不写 `summary`。agrctl 绑定过的 overlay 不再接受 CLI 为这条 shell 调用发来的前台 `local_bash` 帧 | Problem 5。agrctl 的退出码跟着子任务的结果走，CLI 判出的后台状态因此和子任务一致。Rejected: 两边都写 `summary`，后到的赢 |
| 12 | 由父轮的宿主负责绑定和注入：本机由桌面端做，远端由 agentred 做，通过新的 wire RPC 接收桌面端转发的子会话事件 | 远端父转录归 agentred 所有。Rejected: 照审批卡的先例在桌面端本地插块——agentred 和控制台看不到，桌面重启追帧会多出一行 assistant，块的顺序也不对 |
| 13 | Claude 前台 10 分钟的上限不改；agrctl 帮助文本和随附的 agrctl skill 提示长任务改用后台 shell | 用户决定不改超时。Rejected: 调大 `BASH_*_TIMEOUT_MS`——影响这个后端的所有 Bash |

## Binding

前提：调用方带会话级 token，请求是 `send --wait`，目标会话由桌面端拥有。

1. 父轮的宿主（本机是桌面端，远端是 agentred）在调用方会话**当前这一轮**里，找所有外层、还没有结果的 shell 调用，挑出命令文本里同时含 `agrctl`、`send` 和目标 agent 名（或 `--agent-id` 的值）的那些。
2. 正好一条就绑定它。有多条时，再按任务文本筛；还剩不止一条，就不绑定。
3. 不绑定的情况：调用方不是会话 token、没带 `--wait`、没有候选、有歧义、目标会话由控制台拥有。这些情况下 send 照今天的流程执行，父转录不变，桌面端记一条 info 日志说明原因。
4. 绑定以 send 成功建出子会话、起轮为准。agent 名错误等导致的 send 失败不升级，报错和退出码与今天相同。
5. 一条 shell 调用最多绑定一个子会话。同一条命令里先后跑了两个 `send --wait`，第二个就不再绑定，保持原样。

## Card lifecycle

**升级。** 绑定成功后，父转录里那条 shell 行原地变成子代理卡（同一个块，不会多出一项）。卡片头部的写法和现有子代理卡一致：名字是目标 agent 名，描述是任务文本，模型徽标和进度（工具数、用量）取自子会话，状态胶囊显示运行中。卡片默认折叠。

**运行中。** 子会话每发一个工具调用或结果，卡片的 STEPS 里就多一行，显示方式与现有子代理卡的嵌套步骤相同。进度、模型、用量跟着子会话更新。同一条嵌套调用或结果只出现一次。

**结束。**
- 成功：状态与现有子代理卡的成功态相同，不显示胶囊，改显示「N 步 · 用量 · 耗时」。SUMMARY 是子轮的最终文本，也就是父 agent 从 stdout 拿到的那一段。
- 失败：卡片是错误底色，胶囊显示失败，SUMMARY 放错误原因，已经镜像过来的步骤保留。
- 取消：父轮被停、shell 被超时杀掉、后台任务被停止时，agrctl 断开，子会话被停，卡片显示已停止。

**前台与后台。**
- 前台：shell 调用一直开到子任务结束。卡片的终态由子轮结果决定，并且在这条 shell 调用拿到结果之前写入。
- 后台（Claude `run_in_background`）：shell 调用立刻拿到结果，父轮可以正常结束。卡片继续显示运行中，后续步骤写进**原来那条消息**。会话头部的后台胶囊和后台任务面板照常列出这一条（归在「命令」下），停止按钮可用。子任务结束、agrctl 退出后，卡片在原位置收尾。CLI 照常起一轮自主续轮，这部分行为不变。
- 父轮先结束、子任务还在跑的其他情况（例如 Codex 的命令在父轮结束后仍在运行）也走后台这条路：卡片不能被父轮结束翻成已取消，必须由子任务的真实结果收尾。

**写入冲突。** 以下情况都不能把卡片写成错误的状态：
- resume 前奏里 CLI 补发的 `stopped` 通知，不能把已完成的卡翻回已取消；
- 用户中止父轮、卡片已经标成取消之后，注入方迟到的事件不能把卡片写回运行中；
- 父轮还没落库时从外部写同一条消息，结果不能丢块。

**跳转。** 子会话在侧栏里可见时，展开区 meta 行右端显示「打开会话」，点击后切到子会话。`--isolated` 的子会话不显示这个入口。宿主没有提供导航能力时（agentre-server 网页端），也不显示。文案走 i18n，中英文同时更新。

**重载。** 刷新、重开会话、重启桌面端之后，已绑定的 shell 调用仍然显示成同一张子代理卡，内容和收尾时一致。

## Remote sessions

前提：父会话由桌面端拥有，在 agentred 上执行。

- 绑定由 agentred 在转发 send 时完成：它在自己的父转录里匹配，把匹配到的 shell 调用 ID 附在转发给桌面端的请求里。
- 桌面端照常建子会话、起轮，并把子会话的事件通过新的 wire RPC 交给 agentred，由 agentred 写进它的父转录，再以预览帧和持久帧回传。桌面端、agentred、重连追帧看到的是同一份。
- 嵌套调用和结果经持久帧投影到桌面端时不能再丢失（Problem 6）。这一修复同样作用于远端的原生子代理卡。
- agentred 上要能在父轮结束后继续更新后台卡（Problem 7）。实现这一项之前，先复现「远端原生后台子代理卡收不了尾」：能复现，就连同原生后台卡一起修；复现不出来，停下来和用户确认，不凭推断改。
- 断线期间：子任务本身不受影响。重连后卡片的最终状态和最终文本必须正确。断线期间产生的嵌套步骤可能缺几条，桌面端记一条 warn 日志。
- 新增的 wire 消息放在 `pkg/wire`。agentre-server 抬钉，验证构建和测试不受影响，两边成对落地。

## Backends

| 后端 | 支持情况 |
|---|---|
| Claude Code | 前台 Bash 与后台 Bash 都升级 |
| Pi | `bash` 升级；Pi subagent 扩展孙进程里的 send 不升级（Design 8） |
| Codex | 后端沙箱为 `danger-full-access` 时升级。开着沙箱时 send 连不上网关，agrctl 的报错要指出 Codex 沙箱挡住了本机网关、需要把该后端的沙箱设为 `danger-full-access`，退出码与今天的连接失败相同 |

## Out of scope

- 不带 `--wait` 的 send；shell 层面的 `&` / `nohup` 后台化。服务端收到请求时调用已经关闭，自然不升级。
- 控制台拥有的会话。
- Agentre 的 MCP 工具 `call_agent`。
- 调整 Claude 的 Bash 超时；给 Codex 开沙箱网络。
- `agrctl send` 的环检测。
- 调研中发现、与本需求无关的问题，另行跟踪，不在本轮修：Codex 原生 collab agent 没被识别（`transcript-rows.ts:18` 的注释已过时）；Pi `bash` 与 Codex 命令执行的实时输出被丢弃。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 匹配规则（纯函数） | 唯一候选、按任务文本消歧、仍有歧义、无候选、调用已关闭、只看外层块、按 `run_in_background` 分前台和后台、一条调用只绑一次 | `runtimes/piagent/subagent.go` 的 `classifySubagentInvocation` 测试 |
| `serveSend` 的绑定（mock chat 网关） | 会话 token + `--wait` 时绑定；握手 token、异步 send、send 失败时不绑定；请求、响应、退出码不变 | `internal/service/ctl_svc/handler_test.go` |
| chat_svc 注入（mock repo） | 父轮进行中经 `t.mu` 注入；父轮结束后写回原消息；嵌套事件去重；写入冲突各情形 | `tool_approval` 测试、`subagent_activity` 测试 |
| transcript handler | agrctl overlay 不接受 CLI 的前台 `local_bash` 帧；后台卡保留 `local_bash` 的面板语义；子任务最终文本不被 CLI summary 覆盖 | `internal/pkg/transcript/handlers/subagent_test.go` |
| 持久帧投影 | 嵌套调用和结果能投影并在桌面端还原 | `internal/pkg/transcript/projection_test.go` |
| 消息投影（重载） | 已绑定的 shell 调用重载后是 `agent.spawn` | `message_projection` 中 Pi subagent 回放的测试 |
| agentred | 代理侧匹配并附带调用 ID；注入 RPC 写进父转录并发出帧；轮结束后的更新路径；先写复现原生后台卡问题的回归测试 | `internal/daemon/handlers` 现有测试 |
| wire | 新消息的编解码往返 | `pkg/wire` 现有测试 |
| 前端（Vitest） | 实时原地升级、不出重复块；重载渲染；「打开会话」按条件显示；后台卡的运行与收尾；中英文案 | `agent-spawn/card.test.tsx`、`chat-block-reducers` 测试、`i18n.test.ts` |
| E2E（fake runtime） | 本机前台与后台 `agrctl send --wait` 端到端：升级、实时步骤、收尾、重载 | `e2e-bg-task:` 自主续轮接缝 |

无法自动化、改为收尾时人工验证的：
- 用真实 Claude Code 在隔离验证台跑一次前台和一次后台 send；
- 用真实 Codex（`danger-full-access`，走 app-server）跑一次，并确认父轮先结束时卡片的表现；
- 在 dev 环境的 agentred 上跑一次远端前台和后台 send；
- 按 `docs/verification.md` 留下证据。

## Open questions

（无）
