# agrctl 账号对端：不开桌面发起、查询、继续对话

> Status: Draft
> Owner: desktop app + agentred maintainers、agentre-server maintainers（跨仓，wire 与 server 契约成对交付）
> Last updated: 2026-11-25

**Objective:** 其它工具（脚本、编辑器、别的 agent）可以只靠 `agrctl` 在任意设备上发起、继续、查询、等待、停止 Agentre 对话，并回答对话中途的提问和审批，像使用 subagent 一样同步或异步地拿结果；登录账号后不需要打开桌面端，没登录时同一套命令只作用于本机桌面。同一轮补齐 agrctl 的资源管理：密钥不回显、供应商测试与模型导入、设备管理。

**Hard invariant:**
- 不泄密：任何 agrctl 响应、`cli` 设备凭据、个人 token 都拿不到明文 API key，连掩码也不给。
- 不越权：个人 token 只能做它的范围允许的事；`chat` / `approve` 只作用于这枚 token 自己发起的会话；会话里的 agent 不能停止会话、不能批准自己（沿用 `2026-09-22` 的不变式）。
- 不误登出：agrctl 的凭据与桌面端的凭据是两条独立的链，agrctl 刷新凭据永远不会让桌面端掉线。
- 不重复发送：同一条插话只进入一轮；运行中的会话不会被再塞进一个新的轮次。
- 现有行为不变：会话内 agrctl（会话级 token）的资源读写与审批、`agrctl acp` 在桌面模式下的行为、Web 控制台发起对话的流程、桌面端远端执行，都不退化。

## Problem

1. **agrctl 离不开桌面端。** agrctl 只认三种执行者入口：命令行 flag、会话注入的环境变量、桌面写的握手文件（`internal/pkg/ctlclient/client.go:64-124`）。会话外的调用全部打到桌面 ctl 网关；桌面没开就连不上。（已验证）
2. **对话能力只有一次性派发。** `agrctl send` 只能对 agent 新建会话发一条任务，可选 `--wait` 拿最终文本（`internal/cli/ctlcmd/send.go:14-70`）。不能接着已有会话聊、不能指定设备或模型、不能附图、不能查会话列表或历史、不能实时看输出、不能停止。控制 API 已有 `sessionId` 续聊、`stream`、`stop`、`answer-permission`，但只有 `agrctl acp` 在用（`internal/service/ctl_svc/handler.go:49-67`）。（已验证）
3. **控制台会话里的 agent 派不了子任务。** 控制台拥有的会话里，agrctl `send` 被转给 server，server 恒返回 422（`agentre-server/internal/service/ctl_svc/ctl.go:94-96`）。（已验证）
4. **没有账号对端的身份与授权模型。** server 没有个人 token 或任何范围概念；凭据自省只返回账号、设备、类型、对端指纹（`agentre-server/internal/api/credentials/credentials.go:23-35`）；agentred 只保留其中的账号与指纹（`pkg/wire/protorpc/conn.go:70-75`），任何账号对端都能以任意发起方的名义操作并列出全账号会话（`internal/daemon/handlers/session_catchup.go:72-103,356-367`）。server ctl 执行者对任何设备 token 直接写入、不看调用方（`agentre-server/internal/service/ctl_svc/ctl.go:25-29`，`write.go:25`）。（已验证）
5. **设备 token 能拿到明文 API key。** `/v1/engine/snapshot` 与 `/v1/sync/pull` 都把 provider 明文 key 给任何设备 token（`agentre-server/internal/controller/engine_ctr/engine.go:186-204`，`internal/service/sync_svc/sync.go:596`，路由 `router.go:415-428`）。新增一种客户端设备会直接继承这个出口。（已验证）
6. **ctl 响应回显密钥掩码。** `get/list provider` 返回「前 4 位·圆点·后 4 位」（`internal/service/ctl_svc/resources.go:193,250-260`），已有 `apiKeySet` 足以表达「是否设置过」。（已验证）
7. **运行中的会话会被再塞一轮。** agentred 的 `runtime.run` 不检查会话是否在跑（`internal/daemon/handlers/runtime.go:361-608`）；claudecode 复用活着的 CLI 进程，把新消息写进还没结束的那一轮（`internal/pkg/agentruntime/runtimes/claudecode/runtime.go:643-657`）。一轮结束时没送进去的插话由控制台在浏览器里补发（`agentre-server/frontend/src/components/session/useSteerAutoContinue.ts`），浏览器关掉或调用方是一次性 CLI 时就没人补发。（已验证：代码）
8. **审批卡列不出来。** agentred 的待处理查询只含工具权限与提问（`pkg/wire/proto/agentre/wire/wire.proto:696-699`），ctl / 组织操作的审批卡只存在于进程内存（`internal/daemon/handlers/ctlsessions.go:207`）；回答审批卡不检查会话归属（`internal/daemon/handlers/tool_approval.go:18-41`）。（已验证）
9. **资源管理缺口。** ctl 不能测试供应商连接、不能从上游拉取并导入模型、不能管理设备；桌面设置页有这些能力（`internal/app/llm_provider.go`，`internal/app/remote_device.go`）。agentred 已有针对已保存供应商的 `ENGINE_TEST` / `ENGINE_DISCOVER` RPC（`wire.proto:92,395-399`）。设备升级只接受浏览器会话（`agentre-server/internal/api/router.go:242-253`）。（已验证）

## Actors and user stories

1. As a `外部工具`（脚本、CI、编辑器插件、别的 agent），I want 用 `agrctl send` 发起对话并选择 agent、设备、模型，同步等结果或异步拿到会话 id 稍后再查，so that 我能像调用 subagent 一样把任务交给 Agentre，而不依赖桌面端是否开着。
2. As a `外部工具`，I want 继续一个已有会话、在它运行时插话、读取它的消息、实时跟随输出、停止它，so that 我能进行多轮协作。
3. As a `外部工具`，I want 在对话停下来等输入时立即得知，并能回答提问；被授权时还能批准工具权限和审批卡，so that 任务不会无声地卡住。
4. As a `用户`，I want 一次登录（或桌面登录后自动）就让本机 agrctl 可用，并能给不同工具签发范围受限、可吊销的 token，so that 我清楚每个工具能做什么，泄露时能立刻收回。
5. As a `用户`，I want 在 Web 控制台和桌面端看到外部工具发起的会话和它们的待处理项，so that 必要时我亲自接手。
6. As a `控制台会话里的 agent`，I want 调 `agrctl send` 把子任务派给别的 agent，so that 控制台会话与桌面会话一样能编排子任务。
7. As a `Agentre 会话里的 agent`，I want 异步派发任务后再用 `wait` / `messages` 取结果，并能读取、跟随、继续其它会话，so that 不必用 `send --wait` 堵住自己的 shell。
8. As a `用户`，I want 用 agrctl 完整管理供应商（含测试连接、导入模型）和设备，并且永远看不到密钥字符，so that 不打开图形界面也能完成配置。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | agrctl 成为与 Web 控制台平级的账号对端：登录账号后经 server relay 直接驱动 agentred | 用户决定。真正不依赖桌面。Rejected: 桌面无窗口常驻——桌面进程仍在、只限本机；Rejected: 本机 agentred 直接当执行者——agentred 没有组织数据 |
| 2 | 执行者选择：会话内用会话 token；`--desktop` 强制桌面；已登录走账号；否则走本机桌面 | 用户决定。规则可预期，同一条命令不会悄悄换数据源。Rejected: 桌面开着优先桌面——桌面开关会改变看到的数据；Rejected: 只留账号——没登录的桌面用户失去现有能力 |
| 3 | 没登录时，新的对话命令同样可用，只作用于本机桌面的会话 | 用户决定：「没登录就相当于只有本机，操作本机」。Rejected: 未登录时只保留旧 `send` |
| 4 | 会话一律用 `conversation_id`（UUID）指称，桌面模式与账号模式相同；`agrctl send` 的输出改为 UUID，不再输出数字 id | 每个桌面会话建档时就有 UUIDv7，存量已回填（`internal/model/entity/chat_entity/session.go:38-45`）；三端与线格式都用它。项目尚未发布，不保留数字 id 兼容。Rejected: 两种模式各用各的编号 |
| 5 | agrctl 以新的设备类型 `cli` 登录，使用自己的设备指纹 | refresh token 每次刷新轮换，旧 token 再用会被判重放并吊销整条链（`agentre-server/internal/service/device_svc/device.go:416-470`），共用桌面凭据会把桌面登出；设备按（账号，指纹）去重且覆盖类型（`device_repo/device.go:69-73`），复用桌面指纹会把桌面行改成 `cli`。Rejected: 复用桌面凭据；Rejected: 冒用 `mobile` 类型——语义错误 |
| 6 | 桌面已登录时自动为本机 agrctl 代签一台子 `cli` 设备；桌面退出登录时一并吊销 | 用户决定。Rejected: 代签但生命周期独立——换人登录后 agrctl 仍挂在旧账号；Rejected: 不联动，一律 `agrctl login` |
| 7 | 外部工具用带范围、可吊销、可过期的个人 token；范围内直接执行，不再审批 | 用户决定。账号模式没有桌面弹窗。Rejected: 审批搬到 Web 控制台——工具在无人值守时一直卡住；Rejected: 两者都要——工作量最大 |
| 8 | `approve` 是单独的范围，默认不给；只能批准这枚 token 自己发起的会话 | 用户决定。Rejected: `chat` 即可批准——一枚 chat token 泄露就能无审批在设备上跑命令；Rejected: 只有人能批——无法全自动 |
| 9 | 发起对话必须指定 agent；`--device` / `--backend` 只在该 agent 的执行后端中选；模型、推理强度、权限模式按会话覆盖 | 用户决定，与控制台 `dispatch-target` 同一套语义（`agentre-server/internal/service/workspace_svc/web_dispatch.go:200-260`）。Rejected: 允许无 agent 的裸后端会话 |
| 10 | 运行中的会话：不带 `--steer` 的 send 被拒绝；`--steer` 插入当前轮；一轮结束时未送达的插话由 agentred 自己补发下一轮，控制台不再在浏览器里补发 | 用户决定（Problem 7）。Rejected: 一律拒绝、不提供插话；Rejected: 排队到下一轮 |
| 11 | `agrctl acp` 在账号模式下与其它外部工具一样使用个人 token（chat + approve）；桌面模式保持现状 | 用户决定。acp 经标准输入输出运行、没有终端，会被归为外部程序。Rejected: acp 特例视为人——任何程序都能冒充 acp；Rejected: 桌面模式也引入 token |
| 12 | 控制台拥有的会话里的 `agrctl send` 这一轮做通：由会话所在的 agentred 以账号对端身份发起子会话，并在父转录写子代理卡 | 用户决定，取代 `2026-10-27-agrctl-send-subagent-card.md` 决策 4。Rejected: 继续返回 422 |
| 13 | 供应商测试连接、模型发现与导入在一台在线设备上执行；桌面模式在本机执行 | 用户决定。base URL 可能只有设备访问得到（内网、localhost）。扩展已有 `ENGINE_TEST` / `ENGINE_DISCOVER`，不另起 RPC。Rejected: 在 server 执行 |
| 14 | ctl 响应不再包含 API key 的任何字符，只给 `apiKeySet`；两个执行者都改 | 用户决定，也与 `2026-09-22` 的「不回显密钥」不变式一致。桌面设置页的显示不在本轮 |
| 15 | `cli` 设备与个人 token 不能调用 `/v1/engine/snapshot` 和 `/v1/sync/*` | Problem 5。agrctl 只通过 ctl 资源接口读组织数据，那里不含密钥 |
| 16 | 范围在 server 的 HTTP 接口上由 server 检查，在 relay RPC 上由 agentred 检查；agentred 通过凭据自省拿到范围与设备类型 | relay 上 server 看不到 RPC 内容；agentred 唯一的身份来源是自省（`internal/daemon/auth/auth.go:189-209`）。Rejected: 只在 server 检查——管不到 relay |
| 17 | 「外部程序用登录态只能读」是 agrctl 客户端的防误用护栏，不是安全边界；安全边界是个人 token 的范围 | 同一系统用户下的程序可以伪造终端判定、也能读 0600 的凭据文件；server 无法区分。spec 如实写明，不把护栏当鉴权 |
| 18 | 会话级 token 获得对话的读取、等待、跟随与继续，范围是执行者可见的全部会话，不限于自己派出的子会话；仍不能停止、不能回答待处理项、不能批准 | 用户决定，支持「异步派发、稍后取结果」与跨会话协作。不能停止与批准沿用 `2026-09-22` 的不变式。Rejected: 只限自己派出的子会话；Rejected: 保持现状只有 send |
| 19 | 个人 token 允许永不过期；提供 `rotate` 换密钥、保留名字、范围与对端指纹 | 用户决定（长期工具不想定期换 token）。保留对端指纹使轮换后仍能管理此前发起的会话。Rejected: 最长 365 天、不提供永不过期 |

## 执行者与会话引用

- 前提：用户在终端或外部工具里运行 agrctl。
- 执行者按以下顺序确定，`agrctl whoami` 显示本次选中的执行者：
  1. 存在会话注入的 `AGENTRE_CTL_ENDPOINT` / `AGENTRE_CTL_TOKEN`：会话执行者，行为与今天相同。
  2. 显式 `--desktop`：本机桌面（握手文件）。桌面没在运行时报「桌面端未运行」，退出码 1。
  3. 存在 `AGENTRE_TOKEN`（或 `--token`）个人 token，或本机已登录：账号执行者。
  4. 否则：本机桌面，与今天相同。
- 会话一律用 `conversation_id` 指称。所有输出给出完整 UUID；输入不是合法 UUID 时报用法错误（退出码 2）；UUID 合法但当前执行者看不到时报「会话不存在或无权访问」（退出码 1），不区分两者以免泄露存在性。
- 桌面模式只看得到本机桌面的会话（包括桌面派到远端设备执行的会话）。账号模式看得到账号下的会话（见「查询」），看不到只在桌面本机执行的会话。

## 身份与登录

### `agrctl login` / `logout` / `whoami`

- `agrctl login [--server <url>]`：走 Device Flow，打印验证网址与用户码，等待用户在浏览器批准。成功后本机 agrctl 成为账号下一台 `cli` 设备；超时、拒绝、网络错误分别给出明确提示，退出码 1。已登录时再 `login` 报「已登录为 <账号>，先 `agrctl logout`」，退出码 1。
- 凭据保存在 agrctl 解析出的数据目录下，文件权限 0600，不同构建渠道（stable / beta / nightly / dev）互不共享。access token 过期时自动用 refresh token 刷新；refresh token 失效或设备被吊销时，命令失败并提示重新 `agrctl login`，退出码 1。
- `agrctl logout`：先在 server 吊销本 `cli` 设备，再删除本地凭据；server 不可达时仍删除本地凭据，并提示「server 端吊销未完成，可在设备列表中删除」。
- `agrctl whoami`：显示 server、账号、本设备名、当前执行者；使用个人 token 时显示 token 名、范围与到期时间。不显示任何凭据字符。
- `cli` 设备用自己的设备指纹；它不是 relay 的可达目标，不获得局域网直连凭据。

### 桌面代签

- 前提：本机桌面已登录账号。
- 触发：桌面登录成功时，以及桌面启动时已登录且本机 agrctl 没有凭据时。
- 行为：桌面用自己的设备身份向 server 申请一台归属于它的子 `cli` 设备，把凭据写到本机 agrctl 的凭据位置；无需浏览器批准。用户随后在终端直接使用 agrctl，不需要桌面在线。
- 不覆盖：agrctl 已有凭据、且不是本桌面代签的（用户手动 `agrctl login` 的），桌面不覆盖、不吊销它。
- 吊销：桌面主动退出登录，或因凭据过期被迫退出时，吊销本桌面代签的子设备并删除对应本地凭据。server 吊销一台桌面设备时，连带吊销它代签的全部子 `cli` 设备，因此桌面离线时在控制台删除它也能收回子设备。
- 代签失败（server 不可达、版本不支持）只记日志，不影响桌面登录；下次启动重试。

### 设备显示

- `cli` 设备在 Web 控制台设备页显示为 agrctl 类型，不显示在线 / 离线，改为显示最近使用时间；可改名、删除（吊销）。
- 桌面端的远端设备面板不显示 `cli` 设备。
- `cli` 设备不出现在任何「选择执行设备」的列表里。

## 个人 token 与范围

- 前提：已登录，且由人在终端里操作（个人 token 不能创建 token）。
- `agrctl create token --name <name> --scope <s>[,<s>...] [--expires <duration>|never]`：在 server 创建一枚个人 token，**只在这一次**打印 token 明文。名字在账号内唯一。默认 30 天过期；`--expires never` 创建永不过期的 token，创建时在 stderr 提示「这枚 token 不会过期，不用时请删除」。
- `agrctl rotate token <name> [--expires <duration>|never]`：为同名 token 换一个新密钥，名字、范围、对端指纹不变（因此它发起过的会话仍归它管），只打印一次新密钥；旧密钥立即作废（已建立的 relay 连接 60 秒内失效）。不给 `--expires` 时沿用原来的期限规则（原来是 30 天就从现在起再算 30 天，原来是永不过期就仍永不过期）。
- `agrctl list tokens`：显示名字、范围、创建时间、到期时间（或「永不过期」）、最近使用时间、最近轮换时间、创建它的设备；不显示 token 字符。`agrctl delete token <name>`：吊销。
- 使用：外部工具设置环境变量 `AGENTRE_TOKEN`，或传 `--token`。
- 范围：

| 范围 | 允许 |
|---|---|
| `read` | 读资源、设备、会话、消息、待处理项；跟随会话输出 |
| `chat` | 发起对话；对这枚 token 自己发起的会话继续、插话、停止、回答提问（含 `read` 中对会话的读取） |
| `approve` | 对这枚 token 自己发起的会话批准或拒绝工具权限与审批卡 |
| `resources:write` | 创建、修改、删除资源；测试供应商连接；导入模型 |
| `devices:write` | 改名、删除（吊销）、升级设备 |

- 每枚 token 有自己的对端指纹；「这枚 token 自己发起的会话」指 agentred 上记录的发起方是这个指纹的会话。
- 越权：返回失败并指明缺少的范围（例如 `token lacks scope chat`），退出码 1，不挂起、不进入任何审批。
- 吊销与到期：HTTP 接口立即生效；已经建立的 relay 连接在 60 秒内失效（agentred 自省缓存的时长）。
- 个人 token 与 `cli` 设备都拿不到明文 API key，也不能调用 `/v1/engine/snapshot` 与 `/v1/sync/*`。
- agentred 只在声明支持范围检查的协议版本上接受个人 token；目标 agentred 版本过旧时，个人 token 的请求失败并提示「设备 <名> 需要升级」，退出码 1。

## 授权与审批

| 调用方 | 读 | 发起 / 继续 / 停止 / 回答提问 | 批准工具权限与审批卡 | 写资源 / 设备 |
|---|---|---|---|---|
| 会话级 token（会话内） | 执行者可见的全部会话（含 `wait` / `tail` / `messages` / `get session` / `pending`） | 发起；继续执行者可见的任意会话（自己所在的会话除外）；不能停止、不能回答待处理项 | 不能 | 与今天相同（会话内审批卡） |
| 账号登录态 + 人在终端 | 全部 | 账号可见的全部会话 | 账号可见的全部会话 | 直接执行 |
| 账号登录态 + 外部程序 | 全部 | 拒绝，提示签发个人 token | 拒绝 | 拒绝，提示签发个人 token |
| 个人 token | 需 `read` 或 `chat` | 需 `chat`，限自己发起的会话 | 需 `approve`，限自己发起的会话 | 需 `resources:write` / `devices:write` |
| 桌面握手 token + 人在终端 | 全部 | 本机全部会话 | 本机全部会话 | 直接执行 |
| 桌面握手 token + 外部程序 | 全部 | 本机全部会话 | 本机全部会话（现状，`agrctl acp` 依赖） | 桌面弹窗审批（现状） |

- 会话级 token 的「执行者可见」：桌面拥有的会话里是本机桌面的会话；控制台拥有的会话里是账号下的会话（由会话所在的 agentred 代为查询与跟随）。对自己所在的会话 `send --session` 报用法错误「不能向自己所在的会话发送」，退出码 2。
- 「人在终端」与「外部程序」的判定沿用 agrctl 现有规则（有终端、且不在 agent CLI 的终端里）。账号模式下这条判定是客户端护栏（Design 17）。
- `agrctl acp` 在账号模式下需要个人 token（`chat` + `approve`）；帮助文本给出编辑器里配置 `AGENTRE_TOKEN` 的写法。没配 token 时 acp 报错并给出这条提示。

## 对话

### 发起与继续：`agrctl send`

```
agrctl send --agent <agent> [--device <device> | --backend <backend>] [--project <project>]
            [--model <provider>/<model> | --cli-model <model>] [--effort <level>]
            [--permission-mode <mode>] [--image <file>]... [--title <title>]
            [--isolated] [--wait [--timeout <duration>]] [-o json] <text...>
agrctl send --session <id> [--steer] [--image <file>]... [--wait [--timeout <duration>]] [-o json] <text...>
```

- 新会话必须给 `--agent`（名字或 id）。`--device` 在该 agent 的执行后端里挑运行在这台设备上的那个，按 agent 配置的顺序取第一个；没有时失败并列出该 agent 可用的设备。`--backend` 精确指定其中一个后端，不在该 agent 的执行后端里时失败。都不给时按 agent 配置顺序挑第一个可用的，与控制台相同。目标设备离线、项目在该设备上没有路径时，按控制台同样的分类给出原因，退出码 1。
- `--project` 接受名字、`父/子` 路径或 id；不给就是自由会话。
- `--model` / `--cli-model` / `--effort` / `--permission-mode` 覆盖本会话的设置；值不被目标后端支持时失败并列出可选值，退出码 2。
- `--image` 可重复，读取本地图片文件作为附件；文件不存在或不是支持的图片格式时报用法错误。
- `--isolated`：桌面模式下为不进侧栏的一次性会话（现状）；账号模式下为不保存到账号的会话——不进控制台索引、设备离线后查不到。默认会话保存到账号，控制台与桌面都能看到。
- 继续（`--session`）时不能再给 `--agent` / `--device` / `--backend` / `--project`（用法错误）；模型类覆盖仍可用并对之后的轮次生效。
- 会话正在运行时：
  - 不带 `--steer`：失败，提示「会话运行中，使用 --steer 插话或先 agrctl wait」，退出码 1。
  - 带 `--steer`：插入当前这一轮。当前轮结束时还没送进去的插话，由 agentred（桌面模式下由桌面）在下一轮按原顺序送出；调用方退出不影响送达。`--steer` 不能带 `--image`（用法错误）。
  - 会话不在运行时带 `--steer`：按普通继续处理。
- agentred 拒绝在运行中的会话上开新的一轮（任何对端都一样），返回「会话运行中」；控制台与桌面不再自己补发插话，避免同一条插话送两次。
- 账号模式下继续别的对端（桌面、控制台、其它 token）发起的会话：账号登录态的人可以；个人 token 只能继续自己发起的会话。
- 输出：
  - 不带 `--wait`：stdout 打印会话 id，stderr 打印「已派发给 <agent> — <设备>」；这一轮在设备上继续运行，agrctl 立即退出。
  - 带 `--wait`：行为同 `agrctl wait`。
  - `-o json`：stdout 输出一个 JSON 对象，至少包含 `session`、`status`，有结果时包含 `text`，等待输入时包含 `pending`。

### 等待：`agrctl wait <session> [--timeout <duration>] [-o json]`

- 等到以下任一情况出现即返回：
  - 会话空闲（没有运行中的轮次，也没有待送达的插话）：stdout 打印最后一轮助手的最终文本，退出码 0；这一轮以失败结束时打印错误摘要，退出码 1。
  - 会话进入等待输入：stdout 以 JSON 列出待处理项（同 `agrctl pending`），退出码 **4**。
  - 超时：提示「仍在运行」，退出码 **5**；会话继续在设备上运行，可以再次 `wait`。默认不超时。
- relay 断线或设备暂时离线时自动重连，从上次位置继续等，不重复、不遗漏；设备离线超过超时时间按超时处理。
- 帮助文本说明：在 agent 的 shell 工具里长时间 `--wait` 会被 shell 超时杀掉，长任务应异步发起后再 `wait`。

### 跟随输出：`agrctl tail <session> [--from <cursor>]`

- 以 NDJSON 逐行输出事件：轮次开始、正文增量、工具调用、工具结果、待处理项出现与消失、轮次结束、会话状态变化。每条事件带一个不透明的 `cursor`。
- `--from <cursor>` 从该位置之后续接，先补齐错过的事件再接实时事件；不给时从当前开始。cursor 失效时报错并提示用 `agrctl messages` 读取历史。
- 默认一直跟随直到 Ctrl-C；`--until-idle` 在会话空闲时退出（退出码 0）或进入等待输入时退出（退出码 4）。
- 断线自动重连，从最后一个 cursor 续接。

### 停止：`agrctl stop <session>`

- 停止正在运行的轮次，并丢弃尚未送达的插话；输出被丢弃的插话条数。会话不在运行时提示「会话未在运行」，退出码 0。

### 查询

- `agrctl list sessions [--agent <a>] [--project <p>] [--device <d>] [--status running|idle|waiting|failed] [--mine] [--limit <n>] [-o json]`：
  - 账号模式：保存到账号的会话（控制台与 CLI 发起的），加上在线设备上正在执行的其它会话（例如桌面派到远端设备的会话）。离线设备上未保存的会话不出现；离线设备上已保存会话的状态显示为最后已知状态并注明设备离线。
  - 桌面模式：本机桌面的会话。
  - `--mine` 只列当前调用方（这枚 token、或这台 `cli` 设备）发起的会话。
  - 表格首列为会话 id，另含标题、agent、设备、状态、最近活动时间。
- `agrctl get session <id>`：JSON，包含 id、标题、agent、项目、设备、后端、模型、状态、发起方（桌面 / 控制台 / agrctl 设备 / token 名）、创建与最近活动时间、token 用量、待处理项数量、最后一轮最终文本摘要。拿不到的字段省略，不编造。
- `agrctl messages <session> [--last <n>] [--before <cursor>] [--full] [-o json]`：按时间顺序输出消息；默认最近 20 轮，只含用户消息与助手最终文本；`--full` 包含思考之外的工具调用与结果；`--before` 向前翻页。设备离线时，已保存会话从 server 镜像读取，未保存会话报设备离线。

## 挂起交互

- `agrctl pending [<session>] [-o json]`：列出待处理项。不给会话时列出当前调用方可见的全部会话里的待处理项（个人 token 只看自己发起的会话）。每项包含：id、会话、类型（`question` / `tool_permission` / `approval`）、提问与选项，或工具名与要执行的命令 / 变更摘要、出现时间。
  - `tool_permission` 覆盖 CLI 的工具权限请求、计划审批（`ExitPlanMode`）与 Codex 的执行审批。
  - `approval` 是会话内 agent 调 agrctl 写资源或组织操作时出的审批卡。
- `agrctl answer <session> <id> ...`：
  - `question`：`--option <选项文本或序号>`（多选可重复）或 `--text <自由文本>`。
  - `tool_permission` / `approval`：`--allow` 或 `--deny [--message <理由>]`。
  - 待处理项已被别处（控制台、桌面、另一个调用方）处理或已超时：提示「该项已不存在」，退出码 1。
- 回答提问需要 `chat`；批准或拒绝需要 `approve`；都只限调用方有权管的会话（见「授权与审批」）。会话里的 agent 永远不能回答它自己会话的批准类待处理项。
- 所有待处理项同时显示在 Web 控制台与桌面端，人可以在那里处理；agrctl 的 `wait` 随之继续。

## 控制台会话里的 `agrctl send`

- 前提：Web 控制台拥有的会话在 agentred A 上运行，会话里的 agent 运行 `agrctl send --agent <X> [--wait] <任务>`。
- 行为：A 按与控制台相同的规则为 X 选择执行设备 B（可以就是 A），以 A 的设备身份作为账号对端在 B 上发起子会话，并保存到账号，控制台能看到这个子会话。带 `--wait` 时，父转录里对应的 shell 调用按 `2026-10-27-agrctl-send-subagent-card.md` 的规则升级为子代理卡，卡片在控制台与转录里实时更新；不带 `--wait` 返回会话 id。
- 承载 send 的连接断开时停止子会话，与桌面拥有的会话相同。
- 目标设备离线、agent 不存在等失败，与桌面拥有会话的 send 给出同样的报错与退出码。

## 资源管理补齐

### 密钥不回显

- 两个执行者的 `get` / `list provider` 响应里都不再包含 API key 的任何字符，只有 `apiKeySet`；写入 API key 的方式不变。

### 供应商测试与模型导入

- `agrctl test provider <provider> [--model <model id>] [--device <device>]`：用已保存的配置发一次测试请求，输出成功或上游错误摘要（不含密钥）。账号模式在指定设备上执行，不给 `--device` 时用第一台在线、且已同步到这个供应商的设备；没有可用设备时失败并说明原因。桌面模式在本机执行。
- `agrctl discover models <provider> [--device <device>] [-o json]`：列出上游可用的模型，标出哪些已导入。
- `agrctl import models <provider> (--all | <model id>...) [--device <device>]`：把上游模型导入为该供应商下的模型；已存在的跳过并计数。
- 刚创建或修改的供应商尚未同步到设备时，提示「设备 <名> 还没有收到这个供应商，稍后重试」，退出码 1。
- 需要 `resources:write`（个人 token）；登录态外部程序被拒绝。

### 设备管理（仅账号模式）

- `agrctl list devices [-o json]`：名字、类型、在线状态（`cli` 显示最近使用时间）、版本、是否为当前设备。
- `agrctl get device <device>`：额外列出绑定在这台设备上的后端。
- `agrctl update device <device> --name <name>`、`agrctl delete device <device>`（吊销；删除当前设备等同 `logout`）、`agrctl upgrade device <device>`（仅 agentred）：需要 `devices:write`。server 的设备升级接口对持有该范围的调用方开放。
- 配对新的 agentred 仍在那台机器上运行 `agentred login`，不在 agrctl 里做。
- 桌面模式下设备命令提示「设备管理需要 `agrctl login`」，退出码 1。

## 输出与退出码

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 执行失败、被拒绝、越权、会话运行中、设备离线、连不上执行者 |
| 2 | 用法错误（未知命令或 flag、缺必填项、引用有歧义、值不被支持） |
| 3 | 需要人在终端输入（`NEEDS TTY`，现状） |
| 4 | 会话在等待输入（stdout 为待处理项 JSON） |
| 5 | 等待超时，会话仍在运行 |

- 所有列表与详情支持 `-o json`，字段名稳定；错误一律写 stderr。

## 失败与恢复

- 设备离线：发起、继续、插话、停止、回答失败并指明设备；查询退回 server 镜像（仅已保存会话）。
- relay 断线：`wait` / `tail` 自动重连并续接；单次命令（`send`、`answer` 等）失败返回 1，调用方可以重试。
- 凭据：access token 自动刷新；refresh token 失效、设备被吊销、个人 token 过期或被吊销时，失败并给出下一步（重新 `login` 或换 token）。
- 版本：账号模式需要 server 与目标 agentred 都支持本 spec 的协议版本；不满足时提示升级哪一端，退出码 1。

## Compatibility

- 本 spec 取代：
  - `2026-09-22-agrctl-resource-management.md` 的「不做 `agrctl login`、个人 token、server 作为后端」与决策 4「会话外调用只在桌面宿主上支持」。决策 5 的终端判定在桌面模式下保留，账号模式下只作护栏（Design 17）。
  - `2026-10-27-agrctl-send-subagent-card.md` 决策 4「控制台拥有的会话不做」。
- `agrctl send` 的 stdout 从数字 id 改为 UUID；控制 API 的会话标识改为 `conversation_id`。
- 涉及 `pkg/wire`（自省范围、待处理审批卡、运行中拒绝、插话补发、ENGINE 扩展、协议版本）与 server 契约的改动，agentre 与 agentre-server 成对上线。
- 控制台的浏览器补发插话逻辑与 agentred 的补发同时切换，不能出现两边都补发或都不补发的版本组合。

## Out of scope

- Web 控制台或桌面端里管理个人 token 的界面（本轮只有 agrctl 命令）。
- 桌面设置页的 API key 显示方式。
- 在 agrctl 里配对新设备。
- 账号模式下的 `memory` 资源（仍只对会话内开放）、按设备设置项目路径。
- 技能、Hooks、Issue、端口转发、应用设置、MCP 配置等其它资源。
- 账号模式访问只在桌面本机执行的会话。
- 一个 agrctl 同时登录多个账号。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| agrctl 命令层：`ctlcmd.run` 对假执行者 | 执行者选择顺序、各命令的 flag 与用法错误、`-o json` 形状、退出码 0–5、会话 UUID 校验、登录态外部程序被拒 | `internal/cli/ctlcmd/main_test.go`、`resources_test.go`、`fake_executor_test.go` |
| agrctl 账号执行者对假 server + 假 relay/agentred | login / logout / 刷新 / 吊销提示；send 选设备与后端、续聊、`--steer`、wait 断线续接、tail cursor 续接、pending / answer | `internal/pkg/ctlclient/client_test.go`、`internal/cli/acpcmd` 测试 |
| agrctl 依赖图守护 | agrctl 不引入 service / bootstrap / wails | `internal/guard/production_dependencies_test.go` |
| server：设备与 token 服务（mock repo） | `cli` 类型、代签与连带吊销、token 范围与到期、越权拒绝、snapshot 与 sync 拒绝 `cli` 和个人 token、升级接口按范围开放 | `agentre-server/internal/service/device_svc` 测试、`api/engine/guard_test.go` |
| server：ctl 执行者 | 读写按范围检查、`send` 不再恒 422 的分支（控制台会话子任务所需接口） | `agentre-server/internal/service/ctl_svc` 测试 |
| agentred：鉴权与运行时 handler | 自省范围落到连接、按方法拦截、「自己发起的会话」检查、运行中拒绝新轮、插话补发、待处理含审批卡、回答审批卡检查归属、控制台会话 send 与子代理卡 | `internal/daemon/handlers/*_test.go`（`ctlproxy`、`runtime`、`session_catchup`、`tool_approval`） |
| 桌面：ctl_svc 与代签 | 新对话端点（列表、详情、消息分页、待处理、回答、插话、覆盖）、cursor 续接、UUID 定位、桌面登录 / 退出触发代签与吊销 | `internal/service/ctl_svc/handler_test.go`、`internal/service/server_svc` 测试 |
| 前端（控制台与桌面） | 控制台不再浏览器补发插话；`cli` 设备显示；桌面远端设备面板排除 `cli` | `agentre-server/frontend` 与 `agentre/frontend` Vitest |
| E2E 远端对端 smoke | 无头 agrctl：login → send（异步）→ wait → pending → answer → messages 全链路 | `e2e/README.md` 的 remote-peer 边界 |

无法自动化、需在收尾时实地观察：真实 dev 环境（`local-docker` 上的 agentred 与 dev server）里桌面代签、控制台显示 CLI 发起的会话与子代理卡、个人 token 吊销在 60 秒内生效。

## Open questions
