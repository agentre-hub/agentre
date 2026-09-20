# Device-local backend credentials

> Status: Approved
> Owner: desktop app + agentred maintainers
> Last updated: 2026-09-17

**Objective:** 绑定到任意设备（本机或 agentred）的 Hermes / OpenClaw 后端，桌面端与控制台都能为它登录、保存或清除凭据并测试连接；凭据只存在于该后端绑定的设备上。

**Hard invariant:** 凭据明文（OpenClaw Gateway token、Hermes 密码与 refresh/access token、OpenClaw 设备身份种子）不出现在：同步对象、`RunParams`、任何 RPC 响应、账号服务器的数据库与日志、agentred 与桌面端的日志。绑定到本机的后端，其登录、存 token、测试、删除的现有行为不退化。

## Problem

1. **远端设备无法持有后端凭据。** agentred 不注册 OpenClaw runtime，理由是「没有 daemon 本地的 secret enrollment/reference」（`internal/daemon/handlers/runtime.go:361`、`docs/agent-backend.md:583`、`:611`）；桌面端测试远端 OpenClaw 直接返回 `OPENCLAW_REMOTE_SECRET_UNAVAILABLE`（`internal/service/agent_backend_svc/agent_backend.go:515`）。（已验证）
2. **Hermes 登录只能落在桌面端 keychain。** `LoginHermes` 在桌面进程内跑 PKCE 并写本机 keychain（`hermes_auth.go:279-306`）；共享包的登录契约 `HermesLoginInput` 只有 `url/provider/username/password`，没有目标设备（`frontend/packages/agentre-ui/src/engine/ports.ts:127`）。绑定到 agentred 的 Hermes 后端无从登录。（已验证）
3. **OpenClaw token 按本地自增 id 存。** 槽位是 `agentre.openclaw.backend.<backendID>.token`（`agent_backend.go:1352`），该 id 只在一台机器上有意义，无法在另一台设备上引用同一个后端。（已验证）
4. **控制台完全没有凭据入口。** `agentre-server/frontend/src/lib/enginePorts.ts` 未实现 OpenClaw token 与 Hermes 登录相关端口，控制台建出的 OpenClaw 后端没有 token。（已验证）

## Actors and user stories

1. As a `桌面端或控制台用户`, I want 为绑定到 agentred 的 Hermes 后端输入账号密码登录, so that 该 agentred 能连上需要登录的 `hermes serve`（控制台入口在 spec B 把 `hermes` 加入白名单后出现，本 spec 交付其端口实现）。
2. As a `控制台或桌面端用户`, I want 为绑定到 agentred 的 OpenClaw 后端保存 Gateway token 并测试连接, so that 确认那台设备能连上 Gateway。
3. As a `桌面端用户`, I want 绑定到本机与绑定到 agentred 的后端用同一个编辑器管理凭据, so that 不必关心凭据最终存在哪台机器。
4. As a `账号所有者`, I want 账号服务器从不保存这些凭据, so that 服务器被读库时 Gateway 与 Hermes 身份不外泄。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | Hermes 与 OpenClaw 共用一套「设备本地后端凭据」机制：凭据经 RPC 送到后端绑定的设备，只存该设备 | 用户决定。Rejected: 各自独立实现——两套下发路径与离线/删除语义 |
| 2 | OpenClaw Gateway token 也走设备本地登记，不按 LLM API key 的先例存服务器、经 engine snapshot 下发 | 用户决定。代价：换绑设备需重填 token，设备离线时不能改 token。Rejected: 存服务器——服务器持有 Gateway token，且与 Hermes 形成两条凭据路径 |
| 3 | 控制台登录 Hermes：浏览器输入密码，经中继送到 agentred，由 agentred 完成登录并保存 token | 用户决定。密码只以内存形态穿过中继。Rejected: 只能在设备本机用命令登录——多一步；两者都给——工作量最大 |
| 4 | agentred 上凭据存 `state.json`；桌面端仍用系统 keychain | agentred 没有 keychain，设备 token、直连凭据、LLM API key 都已在 `state.json`（`internal/daemon/state/types.go:8-20`）。Rejected: 为 agentred 新开 file keychain 目录——同一进程出现第二处 secret 存储 |
| 5 | 槽位键：OpenClaw token 按后端 `sync_id`；Hermes 按规范化 URL 派生（保持现状）；OpenClaw 设备身份种子每台设备一份，本机生成、永不出设备 | `sync_id` 是账号级标识，两端指代同一后端；Hermes 凭据代表的是「这台设备对这个 serve 的身份」，与后端行无关（`hermes_auth.go:34-38`）。Rejected: 继续按本地 backendID——另一台设备上不存在该 id |
| 6 | 不迁移桌面端按 backendID 存的旧 OpenClaw token，也不处理旧版本 agentred | 用户决定：尚未发布，不考虑兼容 |
| 7 | 编辑器打开时向绑定设备查询凭据状态；设备离线则显示「设备离线，登录状态未知」，登录、保存 token、测试不可用 | 状态不能靠同步字段猜，离线时任何写都到不了设备。Rejected: 离线时先存服务器、上线后下发——违背决定 2 |
| 8 | 删除 Hermes 后端时，只有该设备上已无其他后端指向同一 URL 才清除凭据 | 凭据按 URL 共享（决定 5），无条件清除会让同设备其他后端被登出。Rejected: 沿用现状无条件清除（`hermes_auth.go:352-364`） |

## Credentials on the bound device

每个 Hermes / OpenClaw 后端恰好绑定一台设备（`DeviceFingerprint`）。下文「绑定设备」指这台设备；它是本机时凭据写系统 keychain，是 agentred 时写该 agentred 的 `state.json`。

- **OpenClaw Gateway token**：用户在编辑器保存后端时一并提交 token（或选择清除），写入绑定设备，键为后端 `sync_id`。响应与后续查询只给出「是否已保存」，从不回传 token。
- **OpenClaw 设备身份**：绑定设备第一次需要连 Gateway 时自行生成 Ed25519 种子并保存；它不随任何请求或同步离开设备。
- **Hermes 登录态**：登录成功后绑定设备保存 refresh token（不保存密码），并缓存 access token。登录结果中的 `provider` 与 `userId` 是非敏感展示字段，写回后端行并随同步到两端。

## Device operations

绑定设备对已经能在它上面发起对话的对端（同账号认证的桌面端与控制台浏览器、与它配对的桌面端）提供以下操作，其他调用者一律拒绝。桌面端对绑定到本机的后端走本地同名路径，行为相同。

| 操作 | 输入 | 可观察结果 |
|---|---|---|
| 查询凭据状态 | 后端类型与 `sync_id`（OpenClaw）或 URL（Hermes） | OpenClaw：是否已保存 token；Hermes：已登录（provider、userId）/ 未登录。登录是否过期只在测试连接或发起对话时得知 |
| 保存 / 清除 OpenClaw token | `sync_id`、token 或清除标志 | 写入或删除该槽位；失败时返回可读原因 |
| 列出 Hermes 认证提供方 | URL | 该设备访问 `GET /api/auth/providers` 的结果（名称、显示名、是否支持密码） |
| 登录 Hermes | URL、provider、用户名、密码 | 成功返回 provider 与 userId；失败返回结构化原因（连不上、凭据错误、提供方不支持密码、提供方不可用） |
| 登出 Hermes | URL | 删除该 URL 的凭据 |
| 测试连接 | 后端的连接字段，外加可选的一次性 token（OpenClaw 未保存的草稿） | 由绑定设备按本地凭据实际连一次，返回与桌面端现有测试相同的结构化结果（成功与延迟、需要登录、登录过期、鉴权失败、连不上） |

一次性 token 与密码只用于本次请求，不写入存储。

## Editor behaviour on both hosts

- 共享包的 Hermes 登录 / 登出 / 列提供方，以及 OpenClaw 保存 token / 测试连接，都带上后端的绑定设备；宿主据此路由。控制台始终走设备操作；桌面端对本机后端走本地、对其他设备走设备操作。
- 编辑器打开或切换绑定设备时查询一次凭据状态，并据此显示「已登录为 X / 未登录 / 已保存 token / 未保存 token」。
- 绑定设备未选择、离线或不在账号内：复用现有三条提示，不发请求，登录、保存 token、测试不可用。
- 保存后端时写 token 失败：后端配置回滚，显示可读错误（沿用桌面端现有语义，`agent_backend.go:461-481`）。
- 换绑设备：旧设备的凭据不迁移；新设备上显示未登录 / 未保存 token，需要重新输入。
- 删除后端：尽力在绑定设备上清除凭据（OpenClaw 按 `sync_id`；Hermes 按决定 8）。设备离线时删除仍然成功，残留凭据不处理。
- 所有凭据相关错误按界面文案规范解析成中英文可读句子，不直接显示协议原文。

## Out of scope

- agentred 注册 Hermes / OpenClaw runtime 并执行对话、移除桌面端远端 Hermes / OpenClaw 拦截 → spec B。
- 后端行上 Hermes 字段进入 wire / 同步 / 账号服务器 API，控制台后端类型白名单加入 `hermes`（`provider/userId` 写回在服务器侧生效依赖此项）→ spec B。
- Hermes v7 审批 / 反问及其余能力 → spec C1 / C2。
- 端到端加密中继。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| agentred 设备操作 handler（临时数据目录，Hermes auth 与 Gateway 用替身） | 各操作的成功/失败结果；凭据落 `state.json` 且按决定 5 取键；响应与日志不含 token / 密码；一次性凭据不落盘 | `internal/daemon/handlers` 既有 handler 测试 |
| 桌面端 `agent_backend_svc`（keychain 用内存实现，设备操作用 mock） | 本机与远端后端的路由；OpenClaw 按 `sync_id` 存取；保存失败回滚；删除时 Hermes 同 URL 判断 | `hermes_auth_test.go`、`agent_backend` 既有测试 |
| 共享包编辑器（Vitest） | 登录 / 保存 token / 测试携带绑定设备；离线与未选设备时控件不可用；状态文案 | `agent-backends` 既有测试 |
| 控制台 `enginePorts`（Vitest） | 各凭据端口发出对应设备操作并折叠结果 | `enginePorts` 既有测试 |

不能自动化、由运行时验证覆盖：在 docker.local 的 agentred 上，从桌面端登录一个需要登录的 `hermes serve`，从桌面端与控制台分别为 OpenClaw 保存 token 并测试连接；随后核对该 agentred 的 `state.json` 有对应凭据，账号服务器数据库与日志中搜不到 token 与密码明文。

## Open questions
