# 登录后桌面端与 server 的数据收敛

> Status: Draft（修订待批准）
> Owner: agentre 桌面端 · 同步
> Last updated: 2026-09-04（修订：决策 4 补首轮闸、决策 7 校正落点）

**Objective:** 只要登录了，账号级同步组在这台桌面端与 server 上就是同一份数据：本机存活的行 server 上有，server 上存活的行本机也有。本轮覆盖两条通往这个结果的路——换账号时的归属认领，以及看板删除要落的墓碑集合。

**Hard invariant:** 两条不得退让。其一，R6「删除不会被复活」：本轮新增的任何一条认领路径都不得把一条本机已软删的行当成新建推上账号。其二，`syncwire.GuardPayload` 的规则不放宽——除 `llm_provider` 之外的任何类型都不得携带 `api_key`，任何类型都不得携带本地自增 ID、provider 行正文、`agent_backend` 的 `cli_path` 或头像正文。决策 6 让 `llm_provider` 自己那一份`api_key` 跨账号，是在这条守卫**放行的范围内**做的选择，不是对它的松动。

## Problem

1. **换账号之后，桌面端显示两个账号数据的并集，而 server 只有当前账号那一份。** 认领只收未归属的行（`internal/repository/syncstate_repo/syncstate.go:223`，判据 `sync_account_id = 0 AND sync_deleted_at = 0`），属于另一个账号的行由 `syncmeta_entity.EligibleForSync`（`internal/model/entity/syncmeta_entity/syncmeta.go:83`）挡在上行之外；而四个域的读取一律不带账号条件——`project_repo.List`（`internal/repository/project_repo/project.go:97`）、`agent_repo.List`（`internal/repository/agent_repo/agent.go:132`）、`department_repo.List`（`internal/repository/department_repo/department.go:85`）、`agent_backend_repo.List`（`internal/repository/agent_backend_repo/agent_backend.go:168`）四处的 WHERE 都只有 `status = ACTIVE`。两件事叠起来的观察结果是：上一个账号的项目与 Agent 继续显示在界面上，却一个字节也不上行，用户看到的与 web 控制台看到的对不上，而界面上待同步是 0、没有任何错误可循。

   这一处不是缺陷而是既有设计（R13a）。本轮按下面决策 1 推翻它。

2. **在桌面端删一个标签或一个任务，server 上留下一批活的关联行。** 桌面端删标签硬删本机关联行（`internal/service/issue_svc/issue.go:456` 的 `DeleteByLabel`）却不为它们落墓碑，注释给出的理由是「它们的同步标识不经过本层……拿不到就编不出墓碑」，并依赖「对端收到标签墓碑时自己摘掉」——`labelAdapter.remove`（`internal/service/sync_svc/adapter_issue.go:105`）确实跟了一次 `DeleteByLabel`，所以另一台**桌面端**能自愈，但 server 不是桌面端，它收到一条标签墓碑不会去清关联行。删任务这一侧连自愈都没有：`issueSvc.Delete`（`issue.go:201`）只发一条 `issue` 墓碑，`issueAdapter` 没有 `children`，而对端的 `issueAdapter.remove`（`adapter_issue.go:245`）只把 `issues.status` 置 DELETE、不摘关联。对照之下 server 侧是齐的——它的 `DeleteIssue` / `DeleteLabel` 都调 `tombstoneLinks`（`agentre-server/internal/service/workspace_svc/issue_board_write.go:189`、`:258`）。

   后果与决策 1 那一处同形：新设备全量拉取时 `issueLabelAdapter.refs` 解不到已落墓碑的任务或标签，该行按 R2a 暂缓 30 天后以「引用丢失」进「没能同步的改动」。web 上看不出来，因为读侧两个方向都跳过孤儿（`issue_board.go:239` 的 `!ok` 与 `!live`）。

## Actors and user stories

1. 作为**在同一台桌面端上先后登录不同账号**的用户，我希望登录之后界面上就是这个账号的数据，这样我不必自己分辨哪个项目属于哪个账号。
2. 作为**同时在 web 与桌面端看同一个账号**的用户，我希望两边列出来的项目 / Agent / 后端 / 部门是同一份，这样我在任一侧做的编辑都能在另一侧看到。
3. 作为**在多台机器上用看板**的用户，我希望在一台机器上删掉一个标签或一个任务之后，它的关联行在 server 与别的机器上都不再留着，这样新设备登录时不会攒下一批「引用丢失」的改动记录要我处理。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 登录时把**属于其他账号**的存活行一并认领进当前账号并正常上行（放弃 R13a）。 | 用户决定。已知并接受的后果见「账号之间的收敛方向」一节。Rejected：读取按当前账号过滤——一致性靠「看不见」达成、本机独有列全部保住，但四个域的每一处读取都要加账号条件，漏一处就是又一处不一致；Rejected：换账号时清掉本机旧账号数据——读模型最简单，但 `projects.path`、CLI 路径与本端顺序覆盖会永久丢失，且不可逆。 |
| 2 | 认领跨账号的行时把 `sync_version` 清零。 | 已核实：版本号由 server 在受理上行时分配，是**那一个账号那套序列**里的坐标，在新账号里不可比。rebase 出于同一个理由清版本号（`internal/service/sync_svc/rebase.go` 的 `forgetServerVersions`，实现在 `syncstate_repo.ResetVersions`）。清零后按 R4a 当新建上行。Rejected：沿用旧版本号当基版本——server 的 `applyItem` 在该标识从未见过时照样 `accepted`（`agentre-server/internal/service/sync_svc/sync.go:266`），不报错，但那是一个撒谎的基版本，一旦该标识在新账号里已经存在，冲突判定就落在一个毫无意义的比较上。 |
| 3 | 认领沿用现有的「存活」判据：本机已软删的行与墓碑一律不收。 | 已核实：给新账号推一条它从没有过的墓碑会按 R6 永久占掉那个同步标识，那个对象在这个账号里再也建不回来。这也是现有 `ClaimUnowned` 注释给出的同一条理由。 |
| 4 | 系统 Agent 那一行改成「缺则补」：只有在一次全量拉取之后它**仍然属于别的账号**时才认领。 | 已核实：`agent_entity.DefaultAgentSyncID`（`internal/model/entity/agent_entity/agent.go:21`）是所有桌面端共用的固定同步标识，`EnsureSyncID` 强制系统 Agent 使用它。无条件认领会让上一个账号的 CEO 助手覆盖掉当前账号的那一份——server 判 `conflict`、本次上行按 R4 后到者胜照常生效（`agentre-server` 同上 file:line），每切一次账号发生一次。而当前账号如果是 web 上新建、从没有过桌面端的账号，它根本没有 CEO 助手，此时不补就又是一处「登录了却不一致」。「拉完仍归别人」正好等价于「当前账号没有自己那一份」。**但这个等价只在这一轮真的拉过之后成立**：`ensureServerIdentity` 的「没有记录身份」分支记下就返回、不拉取（`internal/service/sync_svc/identity.go:101`），首轮同步因此走不到那次全量拉取。所以判据是「**这一轮已经拉取过**且它仍属于别的账号」——未拉取的那一轮跳过固定同步标识那一行，留给下一轮（那时身份已记录、从 0 的拉取已发生）。这道闸此前对未归属的行同样不成立：一台新装机会在本机 seed 自己的 CEO 助手（`sync_account_id = 0`），改动之前的认领在首次登录时就会把它推上去、撞上账号里已有的那一份。本轮一并关掉。Rejected：无条件认领——见上；Rejected：永不认领——对空账号留下一处不一致；Rejected：让「没有记录身份」那一支也先拉一次全量——闸最彻底，但那一支当初刻意不拉正是为了避免「所有存量用户升级后各做一次全量重同步」。 |
| 5 | 读取层不改。 | 决策 1 之下，认领完成后每一条存活行都属于当前账号，四处不带账号条件的 `List` 因此已经正确。这是决策 1 相对「读取按账号过滤」省下的主要成本，也是它唯一的技术优势。 |
| 6 | `llm_provider` 与其余类型一样跨账号认领，`api_key` 随载荷进入新账号。 | 用户决定，后果已在决定时列明并接受：上一个账号的 API Key 会出现在新账号的 `sync_objects` 里、并被新账号的全部设备拉到，事后删除也无法从已经落地的各端收回。已核实 `api_key` 确实在载荷里（`internal/service/sync_svc/adapter_llm_provider.go:23`），且 `syncwire.GuardPayload` 只对 `kind=llm_provider` 放行这个键，因此这不是一次疏漏而是一个明确选择。Rejected：排除 `llm_provider`——凭据不过账号边界，代价是新账号里引用 `provider_key` 的后端指向一个缺失的 provider，按既有「目标已失效」语义引导用户重选；Rejected：排除并把旧账号的 provider 行从本机删掉——界面上不再列出不属于当前账号的凭据，代价是登回旧账号要从 server 重新拉一次。 |
| 7 | 看板删除的级联分两处：任务侧做在 `issueAdapter.children`，标签侧做在 `issue_svc.DeleteLabel` 的**删前快照**。 | `children` 是桌面端既有的删除级联缝（`enqueue` 在 `OpDelete` 时调它，项目 / Agent / 后端三类都走它），任务侧适用：`Issue().Delete` 是软删，关联行还在，取得到。**标签侧不可能走这个缝**——`children` 只在删除落库之后才被 `enqueue` 调到，而 `DeleteLabel` 先 `DeleteByLabel` 硬删关联行，等它去取时那些行已经不在库里，而墓碑只能凭关联行自己的同步标识上行（关联表是联合主键，本地 ID 在另一台机器上指向完全不同的两行）。删前快照是同一仓既有的做法（`SetLabels` 的差集、`agent_svc` 处理被挤掉的执行目标）。需要新增一个与 `ListRowsByIssue` 对称的 `ListRowsByLabel`。Rejected：两侧都做在 `children`——标签侧不可能成立（这是本规格初版的错误，由收尾的规格复核轴判出）；Rejected：把 `NotifyDelete` 提到硬删之前——那会在本地删除尚未落库时就推出一条墓碑；Rejected：让 server 在收到任务/标签墓碑时自己清关联行——把一条本该由发起端表达的删除搬到接收端推断。 |
| 8 | hooks（`hook_entity`）不进账号同步组。 | 用户决定。已核实它今天整类不同步，且 `docs/architecture.md` 与 `docs/design.md` 都没有写明这是刻意的边界——本轮把它记成明确决定，后来的人不必再当成疏漏重新发现一遍。实体形状也支持这个决定的另一半理由：`interpreter_path` 与运行态（`next_run_at` / `last_status` / `last_error` / `total_count`）本来就是本机的，只有定义部分是账号级的。Rejected：把定义部分拆出来同步——本轮不做，要做就是一个独立的域与独立一轮。 |

## 认领的判据

**前提**：本机存在四个域的行，其 `sync_account_id` 非零且不等于刚登录的账号；同步已就绪（有账号、有 transport）。
**动作**：每一轮同步开工时的认领。
**可观察结果**：这些行归属改成当前账号、版本号归零、以一次新建进入出站队列并上行；随后 web 控制台与本机列出同一份数据。

认领收两类行，判据合起来是「未归属**或**属于其他账号」：

- `sync_account_id = 0`：登录前创建的行，行为不变（R12a）。
- `sync_account_id` 非零且不等于当前账号：本轮新增，落实决策 1。

两类共用的排除条件不变：本机已软删的行（`sync_deleted_at` 非零，或有 `status` 列的表上 `status` 不是存活）不收（决策 3）。没有 `status` 列的三类（成员关系、执行目标、任务↔标签关联）按硬删处理，与现有的按表分判据一致。

跨账号那一类另有一条：版本号清零（决策 2）。

**固定同步标识那一行（系统 Agent）在「这一轮还没拉取过」时一律不认领**，两类都是（决策 4）。
前提：本机那一行属于别的账号或尚未归属，且这一轮同步尚未发生任何拉取。
动作：这一轮的认领。
可观察结果：那一行不入队、不上行，当前账号在 server 上已有的那一份**不被覆盖**；下一轮（身份已记录、从 0 的拉取已发生）按常规判据处理——账号已有自己那一份时它已被下行覆盖并改了归属，认领自然跳过；账号没有时认领收走并上行，因此「缺则补」最迟在下一轮兑现（一个轮询周期，≤ 30 秒）。

**排在旧账号名下的待发墓碑不受影响。** 这不需要额外规则：认领不收软删行，而待发墓碑对应的正是软删行，两者的行集不相交。登出期间或登录别的账号时删掉一条属于旧账号的行，那条墓碑照旧排在**那个**账号的队列里等它登回来（R6：删除必须到达各端），本轮不动这条路径。同理，登出期间记录的匿名队列行仍然只并给刚认证的那个账号。

## 看板删除要落的墓碑集合

**前提**：本机有一个标签挂在若干任务上，或有一个任务挂着若干标签；已登录。
**动作**：在桌面端删掉那个标签，或删掉那个任务。
**可观察结果**：被摘掉的每一条 `issue_label` 关联行各自落一条墓碑并上行，因此 server 上不再留活的关联行，别的设备也不再留。删标签与删任务两条路径的墓碑集合与 server 侧 `tombstoneLinks` 的取行判据一致（按 `label_sync_id` 或 `issue_sync_id` 命中）。

两侧的落点不同（决策 7）：任务侧在同步层的删除级联缝，标签侧在域服务里、硬删关联行**之前**取快照。

标签目录行与任务行自身的墓碑不变，仍由各自的删除路径发出。关联行的墓碑凭**关联行自己的**同步标识上行——关联表是联合主键，本地 ID 在另一台机器上指向完全不同的两行。

**失败处理**：取关联行失败时整次删除退回，不落一个「主行已删、关联行还在」的半批。这与既有删除路径一致，不新增失败形态。

## 账号之间的收敛方向

决策 1 是双向的，规格把它记成**已定后果**而不是缺陷：

切回上一个账号时，同一条判据会把当前账号的行认领进旧账号。因此每切一次账号，两个账号都朝「这台机器见过的全部数据」的并集收敛；这台机器上先后登录过的账号越多，并集越大。已核实这条路走得通而不是卡住：下行落地按同步标识取行、不带账号条件（`internal/repository/syncstate_repo/syncstate.go:176`），落地后 `SaveMeta` 把归属改写成当前账号（`internal/service/sync_svc/downlink.go:300`），所以行的归属总是收敛到最后一次成功同步的那个账号，不会停在一个半吊子状态。

一条认领上去的行如果撞上当前账号已有的同步标识，走既有机制：server 判 `conflict`，本次上行按 R4 生效，被覆盖的那一版随应答带回并落一条「被覆盖」记录，用户可在「没能同步的改动」里追回。本轮不新增冲突形态。

### 凭据与隐私后果

决策 1 与决策 6 合起来意味着两件必须说在明处的事，两者都是已定后果而不是缺陷：

- **配置数据跨账号**：先后登录过的账号越多，每个账号里的项目 / Agent / 后端 / 部门就越接近这台机器见过的全部数据。在一台机器上先登录公司账号、再登录个人账号，公司账号的这四个域会出现在个人账号里，反之亦然。
- **LLM 凭据跨账号**：`api_key` 在 `llm_provider` 的同步载荷里，因此它跟着一起过账号边界，并被新账号的全部设备拉到。这一步不可逆——事后在新账号里删掉那个 provider 只会产生一条墓碑，已经落到各端的那份正文不会因此消失。

本轮不为这两条提供开关、提示或例外：它们是决策 1 与决策 6 选定的行为。

## 失败与恢复

- 认领之后上行失败（server 不可达）：行已经归属当前账号并已入队，留在队列里等下一轮，本地编辑不被阻塞（R7 / R8 不变）。
- 离线登录：行暂时仍带旧账号；读取层本来就不过滤，界面照常显示，联网后认领并上行，最终一致。
- 认领本身失败（写库出错）：这一轮的认领整体退回，下一轮重做——判据是行上的归属，不另记「认领过没有」的状态，因此重做是幂等的。

## Out of scope

- **web 侧删除级联与 Agent 归属校验**（web 删 Agent 不级联执行目标与项目成员、删后端不级联执行目标、删部门不 reparent 子部门与成员 Agent、建改 Agent 不校验归属二选一）。同属「登录后两边不一致」，但改动全在 `agentre-server`，与本轮相互独立，另一轮处理。
- **按设计逐机不同的本机独有内容**，不参与「一致」的判定：`projects.path` 与 `local_path_missing`（跨机那份走 `project_location`，按「项目 × 机器」逐条同步）、`agent_backend_cli` 的 CLI 路径、`avatar_data_url` 正文（按内容哈希单独传）、执行目标的本端顺序覆盖（R14）。
- **OpenClaw 的 token 不进同步载荷**（存在 OS keychain）：后端配置同步到另一台机器之后仍需在那台机器上重填 token 才跑得起来。本轮不改这条边界。
- **hooks 不进账号同步组**（决策 8）：定义部分是账号级的，但本轮明确不同步它。
- **其余整类不在账号同步组的数据**：对话与会话（走 mirror / relay，不走 `sync_objects`）、app settings（只存 `sync.cursor` / `sync.server_identity` / `sync.local_paths.report` / `sync.board_join_notice` 这几件同步机件自身的状态，按机器一份）、配对的 agentred（server 另有 `devices` 表，两侧各存一份）、本机服务端状态——它们的实体不带 `syncmeta_entity.SyncMeta`。`llm_provider_model` **不在此列**：它随 provider 载荷的 `models` 数组同步。
- **R2a 暂缓与「没能同步的改动」列表仍需用户动作才收敛**：引用目标未到达的行暂缓 30 天后进该列表，冲突或重同步被拦下的那一版同样进该列表等用户追回。本轮不改这两条路径，也不承诺它们自动收敛。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `syncstate_repo` 的认领方法（sqlmock） | 判据与赋值：收未归属的行、收属于其他账号的行、排除软删与墓碑、跨账号那一类把版本号写 0、没有 `status` 列的三类不带存活条件 | `internal/repository/syncstate_repo/syncstate_test.go` 现有的认领用例；按仓规约用 sqlmock，不碰真库 |
| `sync_svc` 认领 + 上行（fake transport） | 本机一条属于账号 A 的项目，以账号 B 登录后：归属改成 B、以一次新建入队、上行到 B | `internal/service/sync_svc/sync_test.go` 里 R12a / R13a 一族用例（其中断言「属于另一个账号的行不上行」的那一条随决策 1 改写） |
| `sync_svc` 系统 Agent 守卫（fake transport） | 当前账号已有 CEO 助手时那一行不被认领；当前账号没有时，全量拉取之后被认领并上行 | 无既有用例，本轮新增 |
| `sync_svc` 首轮闸守卫（fake transport） | 这一轮未发生拉取时，固定同步标识那一行不入队、不上行；下一轮按常规判据处理 | 无既有用例，本轮新增 |
| `sync_svc` 凭据跨账号守卫（fake transport） | `llm_provider` 在跨账号认领里**被**收走、`api_key` 随载荷上行（锁住决策 6，防止后来的人当成疏漏「顺手修掉」） | 无既有用例，本轮新增 |
| `sync_svc` 看板删除级联（fake transport） | 删一个挂在两个任务上的标签：两条关联行各自以一条墓碑上行；删一个挂着两个标签的任务：同理。取行判据与 server 侧 `tombstoneLinks` 命中同一批 | `internal/service/sync_svc/adapter_issue_test.go` 与 `adapter_test.go` 里 `children` 一族用例（项目 / Agent / 后端） |
| `issue_repo` 的 `ListRowsByLabel`（sqlmock） | 新增取数的 WHERE 与返回列：按 label 取全部关联行且带同步元数据 | `internal/repository/issue_repo/label_test.go` 里 `ListRowsByIssue` 的对称用例 |
| `sync_svc` 墓碑守卫（fake transport） | 本机已软删、且属于其他账号的行不被认领、不被当成新建推上当前账号（Hard invariant 的 R6 那一半） | `internal/service/sync_svc/sync_test.go` 现有的登出删除一族用例 |

自动化覆盖不到的一处：**多账号切换的累积效应**（切换若干次之后两个账号各自的内容）。它要两套 server 状态与多轮切换，超出单元测试的边界；由收尾时的源码复核确认认领判据没有第二个入口，加上一次手动验证（登录 A 建数据 → 登录 B 确认出现在 B → 登录回 A 确认 B 的数据出现在 A）覆盖。

## Open questions

无。
