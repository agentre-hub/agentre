# 同步数据完整性一轮：后端 config 整体同步与同类丢值修复

> Status: Approved
> Owner: agentre 同步契约（pkg/syncwire）与 agentre-server web 控制台
> Last updated: 2026-09-17

**Objective:** 任何一端（桌面端、agentred、web 控制台）保存的后端、供应商、Agent 设置，在其它端与服务端存储里都完整、不被旧值或并发写入覆盖。

**Hard invariant:** 同步载荷仍须通过 `syncwire.GuardPayload`；`cli_path` 与凭据永不进 agent_backend 载荷；设备上行的冲突判定（`BaseVersion` / `OverwrittenPayload`）语义不变。

## Problem

证据标注：【核实】= 读完整路径或线上数据确认；【推断】= 机制确认、后果未实测。

1. **后端独占设置上行时整体丢失。**【核实】桌面端把权限模式、sandbox 等存在 `agent_backends.config_json` 一列，Go 字段为 `gorm:"-"`，只有 `agent_backend_repo.hydrateConfig` 会解码。同步上行 `agentBackendAdapter.load` 经 `syncstate_repo.FindRow` 裸读，载荷里这些值恒为空。线上 `agentre_server.sync_objects` kind=agent_backend 7 行 `default_permission_mode` 全为 `""`，而同步源 `agentre-beta/agentre.db` 对应行为 `bypassPermissions` / `danger-full-access`。后果：线上会话以 `acceptEdits` 运行，Codex 非 `danger-full-access`。
2. **后端下行抹掉接收端配置。**【核实】`apply` 对已存在行同样裸读，再由仓库写口按空字段重编 `config_json`，接收端的 hermes 设置被清空。
3. **同一组后端键三处各写一份。**【核实】桌面端私有 `backendConfig`（camelCase JSON 列）、同步载荷（九个 snake_case 平铺字段，无 hermes 键）、web API 平铺字段各自枚举。
4. **供应商下行把本地行的时间与同步元数据清零。**【核实】`llmProviderAdapter.apply` 不读已有行，构造新结构体后 `UpsertFromSync` 以 `Save` 整行覆盖：`createtime/updatetime` 与 `sync_*` 列被写成 0，模型行时间同样清零。正常下行随后 `saveInboundMeta` 补回 `sync_*`，时间永久为 0，下次上行 `UpdatedAt` 为 0；「同步失败的改动 → 恢复」路径不补元数据，随后以基线版本 0 上行。【推断】服务端对基线 0 的处理未实测。
5. **后端编辑器换设备会把旧设备的可执行文件路径写到新设备上。**【核实】共享 UI `agent-backends.tsx` 打开编辑时按后端取一次 `cliPath`，`handleDeviceChange` 只改设备与默认名，保存时该路径落到（后端, 新设备）的覆盖行，覆盖新设备原有路径。openclaw 等无 CLI 的类型还会落一条空路径覆盖行。
6. **web 写后端/供应商会丢掉服务端不认识的载荷键。**【核实机制，当前未丢】`engine_svc` 把存着的载荷解进 syncwire 结构体再整体编码；桌面端比服务端 pin 先加键时，一次 web 编辑即删掉该键。组织面与看板已按 map 合并，无此问题。
7. **web 写入与设备上行同刻发生时，设备刚推的值被覆盖。**【核实机制】`UpdateOrgObject`、`UpdateBackend`/`UpdateProvider`、看板更新与删除都在事务外读行，合并后 `WriteOrgRow` 取更大版本写入，`version<?` 恒成立，不报冲突。
8. **web 组织页切换工具开关只保留三个写死的工具键。**【核实机制，当前未丢】`OrgAgentDetail.tsx` 的 `ORG_TOOL_KEYS` 固定为 org/subagent/hook，`toggleTool` 只写回这三个；桌面端新增工具即被抹掉。
9. **web 以陈旧页面状态整体保存。**【推断，高置信】供应商模型开关从浏览器缓存取整份模型列表发回、服务端整体替换；设置页不随账号通道刷新。后端编辑器同样发送打开时的整份草稿。页面停留期间其它设备的改动会被撤回。

## Actors and user stories

1. 作为在桌面端配置后端的用户，我希望设置在线上 agentred 发起的会话里同样生效。
2. 作为在 web 控制台编辑的用户，我希望只改我动过的东西，其它设备同期的改动不被我撤回。
3. 作为多设备用户，我希望供应商、后端、Agent 的设置在任一端落地后不丢字段、不丢时间与版本信息。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 后端独占设置在载荷里是嵌套对象 `config`，键与桌面端 `config_json` 一致（用户决定） | 三处同形，上行直接搬列值。Rejected: `config_json` 字符串——库里转义串不可读不可查 |
| 2 | `config` 的键表唯一定义在 `pkg/syncwire`，桌面端实体改用它 | 单一真相源（Problem 3）。Rejected: 两边各留结构体 |
| 3 | 整份 config 过机，含 hermes 三键（用户决定） | 统一最简。代价：其它端显示别处 hermes 登录态但无 token。Rejected: 登录态留本机 |
| 4 | 存量修复：桌面端升级后把已绑定账号的后端各重新上行一次（用户决定） | 正确值只在桌面端本地。Rejected: 服务端迁移（只能搬空值）；手动逐个保存 |
| 5 | web API 后端视图与写入改用 `config`（用户决定） | 三处一致。Rejected: API 平铺、服务端互转 |
| 6 | 不做旧格式兼容（沿用首发无包袱原则） | 客户端与服务端须一起升级，见 Rollout。Rejected: 服务端兼容旧平铺键 |
| 7 | 服务端 web 写入在同一加锁事务内读行、按键合并、写入（用户决定） | 同刻写入不丢对方改动且无新交互。Rejected: 版本不符即报冲突——不同字段并发也打扰用户 |
| 8 | web 只发用户改动的部分：模型开关按单个模型写，编辑器只发改动字段；设置页随账号通道刷新（用户决定） | 陈旧页面不撤回其它设备改动；同一字段双方都改时后写生效。Rejected: 带版本号保存、不符则拒绝重做——需新文案与冲突态 |
| 9 | web 写入对载荷按 JSON 键合并，不经 syncwire 结构体重编码 | 未知键原样保留（Problem 6），与组织面现有做法一致。Rejected: 靠及时 bump pin——分叉不会自己暴露 |

## 同步契约

- `AgentBackendPayload` 保留 `type`、`name`、`provider_key`、`model_key`、`env_json`、`reasoning_effort`；删除 `model_routes`、`sandbox`、`approval`、`default_permission_mode`、`default_model` 与四个 `openclaw_*`；新增 `config`。
- `config` 是 JSON 对象，键为 `modelRoutes`（对象）、`sandbox`、`approval`、`defaultPermissionMode`、`defaultModel`、`openclawGatewayUrl`、`openclawAgentId`、`openclawDefaultModel`、`openclawSessionMode`、`hermesUrl`、`hermesAuthProvider`、`hermesUserId`。空值键省略，全空为 `{}`。
- 含 `config` 的载荷通过 `GuardPayload`。

## 桌面端（含 agentred）

- **后端上行：** 已绑定账号的后端被同步时，载荷 `config` 与本地存储的设置逐键相等，包括只存在于 `config_json` 列、未经解码的值。
- **后端下行：** 本地该后端的全部独占设置被 `config` 整体替换（缺失的键变空，`config` 缺失视为 `{}`），之后本机发起会话读到新值。`config` 非对象或不能通过既有后端校验时该条应用失败，本地原值不变。
- **存量修复：** 升级后首次启动，每个已绑定账号、未删除的后端各重新排入上行一次，上行成功后服务端载荷带上本地真实 `config`；之后启动不重复入队。
- **供应商下行：** 已存在的供应商与模型行被更新时，`createtime` 与同步元数据保持原值，`updatetime` 记为本次落地时刻；新建行 `createtime`/`updatetime` 为落地时刻。经「同步失败的改动 → 恢复」应用后，随后的上行以该行恢复前的版本为基线。

## 共享后端编辑器（桌面端与 web 同一组件）

- 编辑已有后端时切换运行设备，可执行文件路径字段随即显示该后端在新设备上已存的路径（没有则为空），保存只写新设备这一条覆盖；原设备的覆盖不变。
- 不使用可执行文件的后端类型保存时不产生路径覆盖行。

## 服务端

- **同步落库：** 设备上行的载荷原样存储，不改写 `config`。
- **后端读写：** web API 后端视图以 `config` 对象给出独占设置，不再有九个平铺字段；旧平铺格式的行读出 `config` 为 `{}` 不报错。写入带 `config` 时整体替换存着的 config，不带时保留；`config` 非对象时拒绝请求、不落库。
- **载荷合并：** 所有 web 写入（后端、供应商、组织对象、看板）只改请求涉及的载荷键，存着的其它键（含服务端不认识的键）原样保留。
- **并发：** web 写入的读、合并、写在同一事务内并锁住该行。设备上行与 web 写入同刻修改同一对象的不同字段时，两边的改动都保留；修改同一字段时后提交者生效。
- **模型开关：** 启用/停用或编辑单个模型只影响该模型，同一供应商的其它模型（含页面打开后其它设备新增或改动的）保持服务端当前值。
- **供应商 API Key：** 保持现有语义（空或掩码值不改）。

## web 前端

- 后端编辑器保存只发送用户在本次编辑中改动过的字段；未改动字段保持服务端当前值。
- 供应商/后端设置页在账号通道提示同步变化时重新拉取列表；正在打开的编辑弹窗内容不被打断。
- 组织页切换一个工具开关只改该工具，其它工具项（含 web 不认识的）原样保留。
- 读取默认权限模式等处改从 `config` 取值；用户可见行为与现在一致。

## Rollout

- 协议变更：桌面端、agentred 与 agentre-server 必须一起升级。未升级客户端收到新载荷会清空本地该后端的独占设置，编辑后上行会写回旧格式。
- 交付顺序：agentre（syncwire、共享 UI、桌面端）先提交推送，agentre-server pin 该提交后再提交；两仓推 dev（自动部署 docker.local）。生产 k3s 部署与 coding 机 agentred 升级由用户执行，不在本轮。

## Out of scope

- 导入/导出 bundle（`data_svc`）格式。
- agentred ↔ 桌面端 protobuf 线协议中的 `AgentBackend.default_permission_mode`。
- hermes token 跨机同步。
- 设备以旧 `BaseVersion` 上行覆盖 web 改动（现有冲突回报机制，按设计）。
- 本地已删除但墓碑未上行的行被更新的远端版本复活（审计中疑似，未核实）。
- 下行新建的部门/Agent/后端/CLI 覆盖行时间为 0（审计中发现，低严重度，另开）。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `pkg/syncwire` 编解码 + `GuardPayload` | `config` 往返无损、守卫放行 | `pkg/syncwire/payload_test.go` |
| 桌面 `agentBackendAdapter.load`（mock 只填 `ConfigJSON` 列） | 上行带出真实设置（Problem 1 回归，先红） | `sync_svc/adapter_test.go` |
| 桌面 `agentBackendAdapter.apply` | 整体替换、字段与列一致、非法 config 不落库 | 同上 |
| 桌面存量入队 | 已绑定未删除后端各入队一次、不重复 | `sync_svc` 队列测试 |
| 桌面 `llm_provider` 下行与恢复（repo 用 sqlmock 断言写入列） | 时间与同步元数据保留、恢复后基线版本正确（Problem 4，先红） | `llm_provider_repo` / `sync_svc/lostchange` 测试 |
| 共享 UI 后端编辑器（Vitest） | 换设备后路径取新设备值；无 CLI 类型不写覆盖；web 只发改动字段 | `agentre-ui` engine 测试 |
| server `engine_svc` / `workspace_svc`（repo mock + sqlmock 断言 `FOR UPDATE`） | config 替换/保留/非对象拒绝、未知键保留、事务内加锁读、单模型写入 | `engine_svc`、`workspace_svc` 既有测试 |
| server 前端（Vitest） | `enginePorts` config 映射、只发改动、通道信号触发重拉、工具开关保留未知项 | `__tests__/engine-ports.test.ts`、`settings.test.tsx` |

不能自动化的部分：事务加锁在真实 MySQL 上的串行效果（sqlmock 只验 SQL 形态）——运行时在 docker.local 真库上并发一次 web 保存与设备上行确认。运行时另确认：桌面端升级后 `sync_objects` 后端载荷含 `config.defaultPermissionMode`；web 控制台显示与编辑正确。

## Open questions
