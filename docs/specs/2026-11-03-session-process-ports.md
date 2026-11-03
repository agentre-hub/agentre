# 会话进程与端口

> Status: Approved
> Owner: agentre 桌面端 + agentred
> Last updated: 2026-11-03（修订 1：CLI 排除口径、协议版本与 agentre-server 配对）

**Objective:** 会话头部的后台任务弹层列出该会话的 CLI 启动后仍存活、且在监听端口或已脱离 CLI 进程树的进程（命令行、端口、PID、运行时长、工作目录），可以在浏览器打开端口、可以结束进程；本机会话与远程（agentred）会话、macOS / Linux / Windows 都支持。

**Hard invariant:** 现有后台任务行（子代理 / 命令 / 已结束）的推导、停止与定位行为不变；进程扫描失败不得影响后台任务行与会话本身；结束进程只能作用于被核实属于本会话的进程。

## Problem

1. **会话留下的常驻进程不可见。** sess-4684（2026-11-03）为画 mockup 在前台 Bash 里用 `(npx vite --port 5391 --strictPort --force > … 2>&1 &)` 起了 dev server。CLI 只把 `run_in_background` 的命令报成后台任务，弹层只从转录里的 task 帧推导（`frontend/src/components/agentre/background-tasks/derive.ts` 的 `deriveBackgroundTasks`），所以这个 vite 在界面上任何地方都看不到；用户只能自己 `lsof` 去找谁占了端口。（已核实：会话转录 + 弹层推导代码。）
2. **沿进程树找不到这类进程。** 本机实测（2026-11-03）：在 Agentre 会话里用 `( … &)` 起的 `python3 -m http.server`，父进程变成 1（launchd），与 CLI 的进程树断开；但它继承了 CLI 的环境变量，`ps -E` 能读到，`lsof -iTCP -sTCP:LISTEN` 能看到端口。（已核实：实测。）
3. **远程会话同样会留进程，而且端口在远程机器上。** 远程会话在 agentred 里执行 CLI（同一套 runtime 包），桌面端访问不到远程 `localhost:<port>`；现有的设备端口转发（`port_forward_svc`）只能在设备设置页里手动建。（已核实：代码。）

## Actors and user stories

1. 作为在本机会话里让 agent 起过 dev server 的用户，我想在会话头部看到它还在跑、占着哪个端口，并且能一键打开或结束它，这样不用去终端里 `lsof` / `kill`。
2. 作为用远程设备（agentred）执行会话的用户，我想同样看到远程机器上该会话留下的进程，点打开就能在本机浏览器访问那个端口，这样不用自己去设备设置里配转发。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 入口放在现有后台任务弹层里，新增一节「进程与端口」（mockup 方案 A） | 用户选定（2026-11-03，对比 mockup 后）。同一个弹层回答「这个会话还有什么在后台跑」。Rejected: 头部单独一颗端口按钮（方案 B）——后台信息分散到两处、头部多一颗图标 |
| 2 | 用注入 CLI 环境的会话标记 `AGENTRE_SESSION=<conversation_id>` 认领进程 | 已核实：孤儿进程继承环境、同用户可读。值是会话 UUID，非机密。Rejected: 从 CLI pid 沿进程树往下找——`( … &)` 孤儿化后断链；匹配已有的 `AGENTRE_CTL_TOKEN`——那是凭据，不该被拿来做扫描匹配 |
| 3 | 列出规则：带本会话标记的进程中，**在监听 TCP 端口**或**父进程不带同一标记（已脱离）**的才列；宿主当前作为 CLI 启动、仍在运行的那些进程不列 | 用户选定（2026-11-03）。修订 1：原写「宿主直接启动的子进程即 CLI」，但容器里 agentred 是 PID 1（`deploy/Dockerfile:63` 直接 `ENTRYPOINT` agentred、无 init），孤儿会被过继给它，按原口径会被误当成 CLI 漏掉，所以改为只排除宿主自己记录的 CLI 进程。轮内前台命令在转录里已可见，列出来是噪音。Rejected: 只列有端口的（漏掉 `tail -f`、watcher）；全部带标记进程（轮内噪音大） |
| 4 | 每行带「结束进程」 | 用户认可（2026-11-03）。只列本会话进程，误伤面受控。Rejected: 只读列表——只解决一半问题 |
| 5 | 远程会话也支持 | 用户选定（2026-11-03）。Rejected: 本轮只做本机 |
| 6 | macOS、Linux、Windows 都做 | 用户选定（2026-11-03），桌面端与 agentred 都发 Windows 版（已核实：release / nightly workflow）。Rejected: Windows 本轮不出现 |
| 7 | 远程行的「打开」：复用或新建该设备的端口转发，再用它的本机地址打开浏览器 | 用户选定（2026-11-03）。已核实：现有 `PortForwardCreate` → `PortForwardOpen` → `BrowserOpenURL` 链路。Rejected: 远程行不给打开按钮 |
| 8 | 不引入 gopsutil，按平台自写扫描 | pi 调研 + 抽查核实：gopsutil 在 darwin 未实现读环境、端口也是调 `lsof`，引入它省不掉核心工作；仓库已直接依赖 `golang.org/x/sys`。Rejected: gopsutil |
| 9 | 只在会话面板可见时扫描：每 5 秒一次，弹层打开时立刻补一次 | 弹层按钮要在「只剩进程」时也出现，所以面板可见期间要持续知道有没有存活进程；面板隐藏时不需要。Rejected: 只在弹层打开时扫——按钮不知道该不该出现 |
| 10 | agentre-server 只做协议版本配对：抬它钉住的 agentre revision 与 `MinSupported`，不加功能代码；web 控制台不展示会话进程 | 已核实：中继只转发不透明帧（`relay_svc.ForwardDaemon/ForwardClient`），控制台没有后台任务弹层；但服务端出示的协议版本来自它钉住的 agentre revision，桌面端 / agentred 对版本做逐字相等校验，不抬钉控制台就连不上升级后的 agentred（修订 1）。Rejected: 同步给 web 控制台做——控制台本就没有这个弹层 |
| 11 | 新增两种请求随项目惯例抬协议版本（0.7.0 → 0.8.0），不设能力位 | 已核实：握手判据是版本逐字相等（`internal/pkg/wireversion/wireversion.go` 的 `Match`），方法集变化必须抬版本（`methodset_test.go` 守卫）。同版本两端必然都支持这两种请求，能力位没有可区分的情形（修订 1）。Rejected: 能力位 + 旧 agentred 降级——旧 agentred 在握手时就被拒，到不了降级分支 |

## 认领与列出

**前提**：会话的 CLI（claude / codex / pi / ACP 任一后端）由桌面端或 agentred 启动。

- 启动 CLI 时，其环境里带 `AGENTRE_SESSION`，值为该会话的 `conversation_id`；本机与 agentred 两条执行路径都带，且不能被用户在后端配置里的环境变量覆盖。这个变量要能传到 CLI 的 shell 工具起的命令里（包括 Codex 的沙箱命令）。
- 扫描时取同一操作系统用户的全部进程，读出环境里 `AGENTRE_SESSION` 等于本会话 `conversation_id` 的那些，再按决策 3 过滤：
  - 宿主（桌面进程 / agentred）当前作为 CLI 启动、仍在运行的进程不列；宿主的其他子进程（例如容器里 agentred 作为 PID 1 时被过继来的孤儿）照常按下面两条判断；
  - 在监听 TCP 端口的列；
  - 父进程不存在、或父进程不带同一标记的列（已脱离的根）；
  - 其余（轮内前台命令、已脱离进程的子进程、包裹命令的 shell）不列，除非它自己在监听端口。
- 每个被列出的进程给出：PID、命令行（参数拼接，超长截断）、监听的 TCP 端口（可能为空）、启动时间、工作目录（读不到时为空）。
- 标记是会话的稳定 ID，所以 Agentre 重启、或空闲清扫回收 CLI 之后，此前留下的进程照样被认出。
- 环境读不到的进程（系统保护进程、权限不足）跳过，不报错。

## 结束进程

**前提**：用户在某一行点「结束进程」。

- 结束前重新核对：该 PID 仍存在、环境标记仍是本会话、启动时间与列出时一致；任一不符则不发信号，返回「进程已不属于本会话」类失败。
- macOS / Linux：对该单个 PID 发 SIGTERM，几秒内未退出再发 SIGKILL；不对进程组发信号。Windows：直接终止该进程。
- 完成后列表立即刷新一次；失败（已核实不属于本会话 / 无权限 / 远程设备离线）以 toast 提示，列表照常刷新。

## 打开端口

**前提**：该行至少有一个监听端口；打开的是第一个端口。

- 本机会话：在系统浏览器打开 `http://localhost:<port>`。
- 远程会话：在该会话的执行设备上找目标为这个端口的已有端口转发，没有就新建一条（新建的转发出现在该设备的端口转发列表里，和手动建的一样，直到用户在那里删除）；随后打开它在本机的地址。转发建立或打开失败以 toast 提示，沿用端口转发现有的失败文案。

## 弹层与按钮

**前提**：会话面板可见。参照 mockup：`.dev-kit/artifacts/2026-11-03-session-process-ports/mockups/`（方案 A、方案 A·只剩进程）。

- 后台任务按钮在「有运行中的后台任务」**或**「有存活的会话进程」时出现；两者都没有时隐藏。按钮的无障碍标签同时报出运行中任务数与存活进程数。
- 弹层主页依次是「子代理」「命令」「进程与端口」三节，只渲染非空的节；「已结束」入口与现在一样钉在底部。只剩进程时弹层只有「进程与端口」一节。
- 「进程与端口」节标题右侧是存活进程数。每行：行首运行态圆点；单行等宽字体命令行（截断）；meta 行依次为端口（`:5391`，多个端口空格分隔；没有时写「无监听端口」）、`PID <n>`、运行时长（与后台任务同一格式，每秒走）、工作目录（截断，读不到时省略）。行尾：有端口时一个「在浏览器打开」按钮，然后是「结束进程」按钮。
- 进程行不可点击定位（进程与转录里的具体步骤没有对应关系）。
- 新文案全部走 i18n（zh-CN 与 en）；命令行、路径、端口、PID 是动态值，不翻译。
- 扫描结果为空或扫描失败时，该节不出现、按钮按「只看后台任务」的规则显示，不出现错误态。

## 远程会话

**前提**：会话的执行目标是 agentred。

- 桌面端向 agentred 发「列出会话进程」与「结束会话进程」两种请求，以 `conversation_id` 定位会话；agentred 在它所在机器上按同一认领规则扫描与结束，结果原样回到桌面端同一节里。
- 这两种请求随协议版本 0.8.0 引入。桌面端与 agentred 版本不一致时，与以往每次协议变更相同，握手即被拒、该远程设备整体不可用，直到两端升级到同一版本；本特性不为旧版 agentred 提供降级。
- 设备离线或请求失败时该节不出现，与扫描失败同一口径。
- 经 agentre-server 中继时，服务端不需要识别这两种请求；但 agentre-server 要在桌面端这一轮推送之后钉到新的 agentre revision、把 `MinSupported` 抬到 0.8.0，否则它的 web 控制台连不上升级后的 agentred。这一步只改钉与版本号，不加功能代码，在 agentre-server 仓库单独提交。

## 平台

- macOS、Linux、Windows 的桌面端与 agentred 都提供认领、列端口、结束进程。
- Linux 上的 agentred 不依赖 `lsof`（容器镜像里没有）。
- 平台能力缺失（例如读不到其他进程环境）按「扫描失败」口径处理：该节不出现。

## 隐私与安全

- 只读取同一操作系统用户的进程；只匹配 `AGENTRE_SESSION` 这一个变量，不保存、不记录、不传输其他环境变量。
- 结束进程只作用于结束前重新核实属于本会话的单个 PID。
- CLI 启动的、在监听端口的 MCP server 也会被列出，结束它会让该会话的对应工具失效——作为已知限制接受，不做区分。

## Out of scope

- agentre-server web 控制台与 Peer Tab 中展示会话进程（它们没有后台任务弹层）。
- 跨会话的全局「端口占用」视图。
- 进程与转录中发起它的那一步之间的对应 / 定位。
- 识别 Docker 容器、`launchctl` 托管、或用清空环境（`env -i`）启动的进程——它们不带标记，认不出来。
- UDP 端口、非监听的 TCP 连接。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 认领规则（纯函数，输入固定进程快照） | 监听端口 / 已脱离根 / 排除宿主记录的 CLI 进程 / 宿主的其他子进程（被过继的孤儿）照常判断 / 排除已脱离进程的子进程 / 标记不符 | `internal/pkg/cliprocess` 的单测 |
| 平台解析（固定字节样本） | darwin `kern.procargs2` 缓冲区解析出环境；Linux `/proc/net/tcp{,6}` 解析出监听端口 | 无 |
| 结束进程的安全核对（注入 OS 端口） | PID 不存在 / 标记不符 / 启动时间不符时不发信号；正常路径先 TERM 后 KILL | `internal/pkg/pty/local` 的宽限收尾测试 |
| 真实进程集成测试（macOS / Linux） | 起一个带标记、`( … &)` 脱离的测试服务器 → 扫描得到它和端口 → 结束 → 消失 | 无 |
| CLI 环境构建 | 四种后端的环境都带 `AGENTRE_SESSION`，且后端配置的同名变量覆盖不了 | `internal/pkg/agentruntime` clienv / ctlenv 测试 |
| 远程：daemon handler + wire 契约 | 两种请求的注册、按 `conversation_id` 定位；协议版本与方法集守卫、契约守卫与 TS 生成 | `internal/daemon/handlers` 测试、`internal/pkg/wireinbound/contract.go` 守卫、`protorpctest` |
| chat_svc / 绑定层（mock） | 本机走扫描、远程走 RPC、远程失败时为空、远程打开先建/复用转发 | `port_forward_svc/svc_test.go`、`chat_svc` stop 测试 |
| 前端 Vitest | 只剩进程时按钮出现、无障碍标签、节渲染与空/失败不出现、打开与结束回调、远程打开走转发 | `background-tasks-chip.test.tsx` |

不能自动化、靠收尾时人工运行观察的：

- macOS 本机会话：真实 Claude Code 会话里用 `( … &)` 起 dev server，弹层列出、打开、结束。
- 远程：对 dev agentred（docker.local 上的 `agentred-dev`）跑一次同样的流程，含自动建转发。
- Windows：CI 只在 Linux 上跑测试，Windows 的认领 / 端口 / 结束由用户在 Windows 机器上人工验证一次。

## Open questions
