# agrctl resource management

> Status: Draft
> Owner: desktop app + agentred + agentre-server maintainers
> Last updated: 2026-09-23

**Objective:** 人和 AI 在装有桌面端或 agentred 的机器上，用 opsctl 风格的 `agrctl list/get/create/update/delete/help` 管理 LLM 提供方与模型、Agent 后端、项目、部门和 Agent。写操作由拥有该会话的一端审批，生效后所有在线的桌面端和 Web 控制台都实时刷新。

**Hard invariant:**
- 不回显密钥：API key 和 OpenClaw token 的明文不出现在任何 `list`/`get` 输出、审批卡、审批弹窗、日志或 ctl 响应里。
- AI 不能批准自己：会话级 token 不能批准或拒绝任何工具权限请求、工具审批，也不能停止会话。
- 现有行为不变：桌面端 Wails 界面、orgtool、Web 控制台的写入路径和校验规则都不退化。`agrctl acp` 和 `agrctl claudecode` 两个子命令的行为也不变。

## Problem

1. **agrctl 只能列出 agent、列出项目、派发任务。** 子命令只有 `agents`、`projects`、`send`（`internal/cli/ctlcmd/main.go:48-58`）；控制接口也没有任何配置写入路由（`internal/service/ctl_svc/handler.go`）。（已验证）
2. **本机 ctl token 是全权的，调用方不分身份。** 握手文件 `ctl-endpoint.json` 里只有一个进程级 token（`internal/service/ctl_svc/ctl.go:22`）。agrctl skill 同时装进 Claude Code 插件目录和 `~/.agents/skills/agrctl`（`internal/pkg/ctlskill/ctlskill.go:1-11`）：Agentre 会话里的 agent、外部 codex/pi/cursor、人手动运行，拿到的都是同一个 token。orgtool 的写操作必须经用户审批（`internal/service/orgtool_svc/mcp.go:28-37`），ctl 却没有这一步。（已验证）
3. **桌面端界面收不到后台写入。** Wails 绑定和服务层写入后都不发 UI 事件，前端只在自己发起的修改完成后重新拉取。`sync:applied` 只刷新侧边栏（`frontend/src/components/agentre/sync-applied-host.tsx:16-26`），组织页、提供方设置、后端设置都不订阅它。（已验证）
4. **agentred 主机上没有 agrctl。**
   - 发布包只含 `agentred`（`Makefile:106-117`），ctlskill 只由桌面端安装（`internal/bootstrap/cago.go:246-264`）。
   - agentred 没有通往桌面端的通用通道，只有每个会话的 MCP 反向隧道（`runtime.mcpProxy`，`internal/daemon/handlers/mcpproxy.go`）。
   - Web 控制台直接派发到 agentred 的会话全程没有桌面端参与（`agentre-server/frontend/src/lib/dispatch.ts:1-17`）。（已验证）
5. **Web 控制台答不了工具审批。** `answerToolApproval` 端口是 `notWiredYet`（`agentre-server/frontend/src/lib/transcriptPorts.ts:70`），relay 方法表里也没有对应方法（`internal/pkg/agentruntime/runtimes/remote/wire/wire.go:32-139`）。（已验证）
6. **server 的提供方和后端写接口只认浏览器会话。** `/v1/engine/providers`、`/v1/engine/backends` 要求 cookie 加 CSRF；组织和项目的写接口则已经接受设备 Bearer token（`agentre-server/internal/api/router.go:193-206, 256-345`）。（已验证）

## Actors and user stories

1. As a `在终端里操作的用户`, I want 不开界面也能增删改提供方、后端、部门、Agent 和项目, so that 能用脚本批量搭建和维护团队配置。
2. As a `Agentre 会话里的 agent`, I want 用 agrctl 查询配置、提出修改, so that 能帮用户搭组织架构。修改要等用户在该会话里批准后才生效。
3. As a `外部 AI 工具`（在用户自己的终端里跑的 Claude Code、codex、pi 等）, I want 用同一个 agrctl 修改配置, so that 不必经过 Agentre 会话。每次写入都要由桌面端的用户批准。
4. As a `控制台用户`, I want 在控制台派发的会话里（无论跑在 agentred 还是桌面端上）也能看到并批准 agent 提出的配置修改, so that 没有桌面端在场时也能用。
5. As a `同一账号下另一台设备或控制台的使用者`, I want 在任何一端做的修改都在我这里即时出现, so that 不必手动刷新。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 命令采用 opsctl 风格：`list/get/create/update/delete/help`；`update` 只改传入的 flag；复杂字段用 `--config '<JSON>'` 或 `--config-file` | 用户决定，与同作者的 opsctl（`opskat/cmd/opsctl/command`）一致。Rejected: k8s 风格的 `apply -f`/`edit`/YAML 信封——对这个规模太重 |
| 2 | 命令放在顶层，删掉 `ctl` 这一层；`send` 挪到顶层，行为不变；`ctl agents`/`ctl projects` 由 `list agents`/`list projects` 取代，不保留别名 | 用户决定；按首发处理，不留兼容包袱。Rejected: 挂在 `ctl` 下面 |
| 3 | 五类资源一轮全做 | 用户决定。Rejected: 分两轮，先做组织和项目，再做提供方和后端 |
| 4 | **谁拥有会话，请求就交给谁**：桌面端拥有的会话交给桌面端执行；控制台拥有、跑在 agentred 上的会话交给 server 执行；没有会话的调用只在桌面主机上支持 | 用户决定，本 spec 一并交付。审批留在拥有会话的一端，与现有的工具权限、提问、审批归属一致。Rejected: 只支持桌面主机；让 agrctl 以 server 为唯一后端——那样本机路径、本机钥匙串这类设备本地数据写不进去，离线的本地用户也不能用 |
| 5 | 审批强度是护栏级：Agentre 会话注入会话级 token，写操作在该会话里审批；外部调用凭「stdin 不是 TTY」识别，走桌面端弹窗审批；人在 TTY 里直接执行，只有 `delete` 再问一次 y/N | 用户决定。同一系统用户下的 agent 能读到握手文件、也能伪造一个 pty，所以这只拦得住守规矩的 AI，文档要写明。Rejected: 握手 token 降为只读、所有写入都要桌面弹窗——这样 CLI 就不能在无人值守时写入 |
| 6 | 审批卡用方案 B「变更清单卡」：完整命令，加上每项变更的操作、类型、名字和字段前后值；它仍然是 `tool_approval` 块，`toolKey` 为 `ctl` | 用户决定，见 mockup。仍用 `tool_approval` 块，控制台升级 pin 之前也能按旧的 JSON 卡降级显示。Rejected: 复用现有卡直接渲染 JSON |
| 7 | 名字解析、flag 解析、输出格式、`help` 契约只在 agrctl 客户端实现；执行者只接受按 id 的操作和字段集合，字段前后值由执行者对照当前数据自己算 | 桌面端和 server 是两个 Go 模块，不能互相 import。把语义放在客户端，只需要实现一份。前后值不信任客户端，审批卡上显示的就是真实变更。Rejected: 两个执行者各实现一套名字解析 |
| 8 | ctl 的请求和响应契约（资源文档、操作、变更清单）放进 `pkg/wire`，以 Protobuf 定义 | `pkg/wire` 是工作区里已经批准的跨仓 Go 共享物（`AGENTS.md`）。Rejected: 新增一个共享模块——违反跨仓不变量 |
| 9 | 模型是提供方下的子资源，用 `提供方/ModelID` 定位（按第一个 `/` 切出提供方，其余是 ModelID，如 `openrouter/openai/gpt-5.1`）；同一提供方下 ModelID 重复时按歧义处理。创建模型不接受 key，ModelKey 仍由服务生成，只在 `get` 里展示 | 用户决定（2026-09-23 修订）。模型没有自己的同步标识，随提供方一起同步（`llm_provider_model.go:27`）；ModelKey 是创建时生成的 UUID、永久不可改（`llm_provider.go:164`、`llm_provider_model.go:4`），用户看得到的是 ModelID。Rejected: 用 ModelKey 定位——用户得先查 UUID；按显示名定位——显示名可以为空 |
| 10 | 密钥只从 flag 写入：裸写 `--api-key`/`--token` 在 TTY 里无回显输入；写 `--api-key=<值>` 可用，但 help 里警告它会留在 shell 历史里；没有 TTY 时裸 flag 以退出码 3 结束并提示 `NEEDS TTY` | 沿用 opsctl 的 `--password` 约定。Rejected: 从环境变量或文件读取密钥——多出一条泄露面 |
| 11 | 桌面端实时刷新：服务层每写一次这五类资源就发 `config:changed`；组织页、提供方设置、后端设置、侧边栏订阅它，同时也订阅 `sync:applied` | 在生产者一侧统一发事件，Wails、orgtool、ctl 三条写入路径一起受益。Rejected: 只在 ctl 路径上发事件 |
| 12 | 外部调用的审批弹窗、会话审批的超时都是 4 分钟；关闭弹窗等同拒绝 | 与 orgtool/hooktool 的 `approvalTimeout` 一致（`orgtool_svc/orgtool.go:25`）。Rejected: 关闭后保留待审批——请求会一直悬着 |
| 13 | 控制台答工具审批用一个通用的 relay 方法，所有 `toolKey`（`org`、`ctl`、…）都走它 | 问题 5 对所有 `tool_approval` 块都成立，按 toolKey 各开一个方法只会重复。Rejected: 只接通 `ctl` |

## Command surface

**资源：** `agent`、`department`（短名 `dept`）、`project`（短名 `proj`）、`provider`、`model`、`backend`。`list` 的资源名用复数，`agents`、`providers` 等都可以写。

**定位：**
- 用数字 id、名字，或者 `父/子` 路径定位资源。项目、部门只在同级内名字唯一，Agent、提供方、后端全局唯一；模型用 `提供方/ModelID` 定位，见决策 9。
- 名字有多个匹配时报错，列出每个候选的 id 和路径，并给出一条可以直接改用的示例命令。找不到时报 `<资源> "<名字>" not found`。
- flag 里引用其他资源时，同样按名字或 id 解析，例如 `--department`、`--parent`、`--backend`、`--provider`、`--lead`。

**动词：**
- `list <资源> [过滤 flag] [-o json]`：以表格输出，首列是 ID。过滤 flag 有 `agents --department`、`projects --parent`、`models --provider`、`backends --type`。
- `get <资源> <定位>`：输出 JSON 详情。
  - 字段名与 `create`/`update` 的 flag 对应。
  - API key 只显示掩码（前 4 位、圆点、后 4 位），token 只显示是否已设置。
  - 附带关联信息：提供方被多少后端引用、项目的成员和各设备上的路径、Agent 的执行目标。
- `create <资源> [flag]`：创建成功后输出 `<资源> <名字> created (id N)`。
- `update <资源> <定位> [flag]`：只修改传入的字段。
  - 集合字段有专门的 flag：`project --add-member/--remove-member`；`agent --backend` 可重复，写了就整体替换执行目标；模型用 `--default`、`--enable`、`--disable`。
  - 成功后输出 `<资源> <名字> updated`。
- `delete <资源> <定位> [flag]`：
  - 部门默认把子部门和 Agent 上移到父部门，`--cascade` 则一起删除。
  - 仍被后端引用的提供方或模型，需要 `--force` 才能删。
  - 项目有子项目或活跃会话时、系统 Agent，沿用服务层现有的拒绝，错误信息原样透出。
- `help [资源 [类型]]`：输出该资源的 flag、`--config` 可接受的字段、密钥字段和示例。`help backend <type>` 按后端类型列出专属配置。它是只读的，不需要连接桌面端。
- `send`：行为与现在的 `ctl send` 相同。

**校验：** 按服务层现有规则执行，第 1 节列出的名称、类型、颜色、环路、引用等规则都不放宽。未知的 flag，或者不属于该后端类型的 `--config` 字段，都判为用法错误。

**退出码：**

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 执行失败、被拒绝、审批超时、连接不上执行者 |
| 2 | 用法错误：未知子命令或 flag、缺必填项、定位有歧义 |
| 3 | 需要人来操作：没有 TTY 却要输入密钥（`NEEDS TTY`） |

错误输出到 stderr，以 `Error:` 开头；需要人来操作时以 `NEEDS TTY:` 开头，并说明应该让用户去自己的终端里执行。

输出样例见 mockup 的「CLI 输出」页：`.dev-kit/artifacts/2026-09-22-agrctl-resource-management/mockups/`（本地证据，不入库）。

## Routing and approval

agrctl 按以下顺序确定调用方和执行者：
1. 环境变量里有会话级 token：这是会话调用。
2. 没有会话级 token：使用本机桌面端的握手 token。stdin 不是 TTY 时视为外部调用，是 TTY 时视为人工调用。

`help` 是纯客户端的，任何机器上都能用。`list`、`get` 对能连上执行者的调用方直接执行，不需要审批。写操作（`create`、`update`、`delete`）按下表处理；最后一行的限制同样适用于读操作。

| 调用方 | 执行者 | 审批 |
|---|---|---|
| 桌面主机上的 Agentre 会话（包括控制台派发到该桌面的会话） | 本机桌面端 | 在该会话的对话流里出一张 `ctl` 审批卡；桌面端和控制台都能答 |
| 桌面端派发到 agentred 的会话 | agentred 本地的 ctl 代理经 `runtime.mcpProxy` 同一条连接转回拥有会话的桌面端 | 同上，卡片出现在桌面端的这个会话里 |
| 控制台派发到 agentred 的会话 | agentred 本地的 ctl 代理转给 server 执行 | agentred 在自己的会话对话流里出 `ctl` 审批卡，控制台经 relay 作答；批准后 agentred 才把操作提交给 server |
| 桌面主机上的外部调用（非 TTY、无会话 token） | 本机桌面端 | 桌面端全局审批弹窗 |
| 桌面主机上的人工调用（TTY） | 本机桌面端 | 直接执行；`delete` 在终端里再问一次 y/N |
| agentred 主机上没有会话 token 的调用 | 无 | 退出码 1，说明在 agentred 主机上只有 Agentre 派发的会话能用 agrctl |

**会话级 token：**
- 桌面端和 agentred 启动 CLI 类 agent 子进程时，都要注入 `AGENTRE_CTL_ENDPOINT` 和会话级 `AGENTRE_CTL_TOKEN`。token 用 HMAC 签发，绑定 agent 和会话，签名方式与 `internal/pkg/agenttool/token.go` 相同。
- 持会话级 token 可以做：全部读操作、`send`、需要审批的写操作。
- 持会话级 token 不能做：`answer-permission`、答工具审批、`stop`，一律返回 403。

**审批卡（会话内）：**
- 一次写命令对应一张卡。卡上显示：
  - 完整命令行，其中密钥 flag 的值替换成 `…`；
  - 这次变更的操作徽标（新建/修改/删除）、资源类型和名字；
  - 修改字段的前后值；密钥字段只写「已更新（值不显示）」；带 `--cascade` 的删除写明连带删除的子部门和 Agent 数量。
- 状态有：待审批、已批准（结果写「已更新 …」或「执行失败：<原因>」）、已拒绝、已过期。应答失败时在卡内显示错误，可以重试。
- 等待期间 agrctl 在 stderr 打印 `waiting for approval in session #<id> …`。被拒绝或超时时退出码为 1，输出 `Error: rejected in session #<id>` 或 `Error: approval timed out`。
- 卡片组件属于 `@agentre-hub/agentre-ui`，由现有的 `tool_approval` 路由按 `toolKey === "ctl"` 分派，桌面端和控制台共用。

**外部调用的审批弹窗（桌面端全局）：**
- 不论当前在哪个页面都会弹出。内容与会话审批卡相同，额外显示一行调用方信息：父进程名、pid、工作目录，均由 agrctl 上报，只作提示，不作为信任依据。
- 多个请求排队时一次只显示一条，副标题写「第 N 个，共 M 个待审批」。
- 删除操作使用危险确认形态，主按钮为「批准删除」。
- 提交中两个按钮都不可点，Esc 也不关窗。提交失败时错误显示在底栏左侧。
- 4 分钟无人处理即自动拒绝；关闭弹窗等同拒绝。
- 窗口不在前台时同时发一条系统通知，点击通知切回窗口并显示弹窗。
- 等待期间 agrctl 在 stderr 打印 `waiting for approval in the Agentre desktop …`。

**控制台作答：**
- 新增一个 relay 方法，用来答工具审批。桌面端（作为 relay 目标）和 agentred 都要处理它。
- 控制台的 `answerToolApproval` 端口接到这个方法上。`org` 和 `ctl` 两类审批卡在控制台都能作答。

## Executors

**桌面端执行者：** 经现有服务层完成读写，不绕过服务层的校验，也不绕过同步通知（`sync_svc.Notify*`）。提供方的 `--api-key` 按现有规则处理：省略表示保留原值，写空字符串表示清空。OpenClaw 的 `--token` 由服务层路由到后端绑定设备的钥匙串，设备离线时报错，与编辑器的现有行为一致。

**server 执行者（agentre-server）：**
- 提供一组只接受设备 Bearer token 的 ctl 接口，对五类资源实现与桌面端相同的读取、按 id 写入和前后值计算，写入复用 `workspace_svc`/`engine_svc` 的现有路径。
- 写入后照常推进同步版本并广播账号信号，所以桌面端和控制台都会实时刷新。
- 提供方和后端的写入要能以设备 Bearer 身份执行。浏览器路径仍然要求 CSRF，不受影响。
- 本 spec 在 server 路径上不支持以下操作，都返回退出码 1 并说明原因：
  - `send`；
  - OpenClaw 后端的 `--token`，除非该后端就绑定在发起请求的这台 agentred 上，这时由 agentred 在本地写入；
  - 为桌面设备设置项目的本机路径。

**agentred：**
- 发布包附带 agrctl。agentred 启动时，把 agrctl 和 ctlskill 装到该主机用户的目录下，与桌面端的安装位置相同。
- agentred 在自己的 gateway 上挂一个 ctl 代理，按会话归属转发：拥有会话的是桌面端连接就转回桌面端，是控制台就转给 server。

## Real-time refresh

- **本机桌面端：** 这五类资源的任何写入成功后，服务层都会发 `config:changed`，载荷是变更涉及的资源类型。组织页、提供方设置、后端设置、侧边栏的项目树订阅它，就地重新拉取，不显示任何提示条或 loading 横幅。三条写入路径（Wails、orgtool、ctl）都会触发。
- **其它桌面端：** 这些页面同时订阅 `sync:applied`，同步下行真正落地数据后刷新。
- **Web 控制台：** 现有的账号信号已经能在约 1 秒内刷新（`useAccountChannel`），不需要改。
- **前提与例外：** 跨端刷新要求桌面端已登录；桌面设备的项目本机路径按设计不同步，不会出现在其它端。

## Out of scope

- `--dry-run`、`--watch`、批量文件导入，以及 k8s 风格的 `apply`/`edit`。
- 头像上传、Hermes 交互式登录、CLI 路径覆盖（`agent_backend_cli` overlay）。这些继续在桌面端或控制台界面里操作。
- agentred 主机上没有会话的调用（见路由表）；非 Agentre 派发的外部 AI 在 agentred 主机上也不支持。
- 控制台里的「OpenClaw exec 审批」和「计划操作」两种作答：它们同样是 `notWiredYet`，但不属于本 spec。
- `agrctl login`、个人访问 token，以及让 agrctl 以 server 为唯一后端。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `internal/cli/ctlcmd`（fake 控制服务 + 注入 env/TTY） | 每个动词和资源的 flag 解析；名字、路径、歧义、找不到时的解析；表格和 JSON 输出；密钥 flag 在 TTY 与非 TTY 下的行为；退出码 0/1/2/3；等待审批时的 stderr 行 | `internal/cli/ctlcmd/main_test.go`（`fakeControl`、`runCLI`） |
| `internal/service/ctl_svc` handler（gateway fake） | 按 id 的读写；前后值计算；输出和响应里没有明文密钥；三类 token 各自的权限（全权、会话级、非 TTY 外部）；会话 token 调 answer-permission/stop 返回 403；审批的批准、拒绝、超时 | `internal/service/ctl_svc/handler_test.go` |
| 五个服务的写入方法（mockgen repo mock） | 写入成功后发出 `config:changed` 并带上正确的资源类型；失败时不发 | `sync_svc` emitter 注入 |
| agentred ctl 代理 | 按会话归属转发（桌面端连接或 server）；没有会话 token 的调用被拒；控制台路径上先审批后提交 | `internal/daemon/handlers/mcpproxy_internal_test.go` |
| agentre-server ctl 接口（sqlmock + service mock） | 设备 Bearer 鉴权；按 id 写入复用现有服务；前后值计算；写入后广播；不支持的操作返回明确错误 | `agentre-server` 的 workspace/engine controller 测试 |
| 工具审批 relay 方法（桌面入站 + agentred） | 作答能唤醒挂起的审批；会话不存在或 requestId 过期时的错误 | `internal/peer/protobuf_inbound_test.go`（submitToolPermission 入站） |
| `agentre-ui` 变更清单卡（Vitest） | 各状态的渲染；密钥字段不显示值；应答失败后可重试；`toolKey` 非 `ctl` 时仍渲染原来的卡 | `transcript/tool-approval/card.test.tsx` |
| 桌面端审批弹窗与页面订阅（Vitest） | 排队、超时、关闭等同拒绝、危险形态；页面收到 `config:changed`/`sync:applied` 后重新拉取 | 无现成测试（`quit-confirm-dialog.tsx`、`sync-applied-host.tsx` 为参照形态） |

以下内容无法自动化，收尾时做一次实机验证，按 `docs/verification.md` 留证据：
- 在桌面主机上分别以人工 TTY、外部 AI、Agentre 会话三种身份执行写操作，确认本机页面、第二台桌面和 Web 控制台都实时刷新。
- 在 dev 环境里，对桌面端派发和控制台派发到 agentred 的两类会话各执行一次写操作，确认审批卡出现在正确的一端并能作答。
- 生产 MySQL 是 8.4.2；如果 server 侧没有 DDL 改动，这一项不涉及。

## Open questions
