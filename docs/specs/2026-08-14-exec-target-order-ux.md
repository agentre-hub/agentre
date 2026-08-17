# 执行目标顺序：每端自己排

<!-- File: docs/specs/2026-08-14-exec-target-order-ux.md -->

> Status: Approved（2026-08-14，用户裁决「可以」）；**2026-08-16 修订并重新批准**（用户裁决：「server 可以当作为默认顺序，浏览器 server 这边就不用区分浏览器了」）
> Owner: 组织架构 / 派发（桌面前端 + web 控制台 + server workspace）
> Last updated: 2026-08-16
> 实施：跨两个仓库，**分别提交、不混 commit**（工作区根 AGENTS.md）。首轮 `agentre` 走 `ui/2026-08-14-exec-target-order-ux`（纯前端，已合入 `3c5bcec4`），`agentre-server` 走 `feat/2026-08-14-exec-target-order-ux`（迁移 + 端点 + web 界面，已合入）。**2026-08-16 修订只动 `agentre-server`**（删表迁移 + 端点改写 + web 侧去 client id），桌面端零改动。

## 2026-08-16 修订：浏览器排的就是账号默认顺序

**首轮已实现并合入，本次修订推翻其中的浏览器侧存储模型。** 变的只有一句：浏览器不再持有「自己那一份」顺序，它排的就是账号级的 `agent_exec_targets.sort_order`。桌面端的每端覆盖不变。

层数从三层降到两层：

| 层 | 首轮 | 修订后 |
|---|---|---|
| 账号默认 | `agent_exec_targets.sort_order`，只有桌面端能写 | 同上，**浏览器直接编辑** |
| 桌面端每台 | `agent_exec_target_overrides`（本地表） | 不变 |
| 浏览器每个 | `browser_exec_target_orders`（server 表） | **删除** |

**这一条直接推翻决策 7 的拒绝方案**（「拒绝把顺序写回同步对象的 `sort_order`（那是账号级的，会污染所有端）」）。当时判定的「污染」在本次裁决下被重新定义为**账号默认应有的语义**：账号默认就是「还没有自己意见的端跟随的那一份」，浏览器改它，从未调整过的桌面端跟着变，正是这个概念的定义而不是它的副作用。已经拖过一次的桌面端有本端覆盖，不受影响（决策 4 取消了回到默认的路径）。

改动清单（全在 `agentre-server`）：

- 新迁移 `DROP TABLE browser_exec_target_orders`（追加到 `migrationList()` 末尾，不改既有迁移）
- 删 `internal/model/entity/exec_order_entity` 包及其 repo
- `SetExecTargetOrder` 去掉 `ClientID` 入参，改为读-改-写 `sync_objects` 里该 Agent 的各行 `agent_exec_target`，只改 `sort_order`
- `WebDispatchPlan` 去掉 `deviceFingerprint` 重排参数（顺序已经在集合里，无需二次重排）
- web 侧 `frontend/src/lib/execOrder.ts` 去掉 client id 的生成、持久化与上送

**写入必须整行读-改-写。** 决策 4（前置规格）把字段级冲突合并列为非目标，`sync_objects` 是整行 last-write-wins。`agent_exec_targets` 的字段是 `(agent_id, agent_backend_id, sort_order, skills_json)` + SyncMeta（`internal/model/entity/agent_entity/exec_target.go:18-22`），浏览器只想改 `sort_order`，**不得把 `skills_json`（R15e 的按档技能授权）冲掉**。浏览器本来就持有完整载荷（渲染执行目标链用的就是它），改完整行写回即可。这张表里没有路径 / `CLIPath` / `EnvJSON`，浏览器持有整行不触 R19。

**Objective:** 让「这个 Agent 会派到哪台机器」在每一端都是一个可以就地重排的列表，各端互不干扰；界面上不再出现「账号默认顺序」这个概念，也不再要求用户先选作用域才能做事。

**Hard invariant:**

1. 不改变派发的解析语义：桌面端仍是「本端有覆盖用覆盖，无覆盖用账号默认且本机档提前」（`internal/model/entity/agent_entity/exec_target_override.go:49-108` 的 `ResolveExecTargetOrder`），web 端仍是「跳过本机相对引用，按序取第一个可用」（`agentre-server/internal/service/workspace_svc/workspace.go:498-546`）。本轮只改**顺序从哪里来**，不改**怎么按顺序挑**。
2. 不违反 web 控制台的 R19 红线：项目路径只随选中档（`Chosen.Cwd`）出现，不得为了让浏览器自己挑档而把 `cwd` 铺到每一档上。
3. 不改动桌面端 `agent_exec_target_overrides` 表结构，不新增桌面端迁移。
4. server 不因本轮持有任何会话内容。~~新增的表只存「哪台设备、哪个 Agent、backend 的排列」。~~（2026-08-16 修订：那张表被删除，顺序回到同步组的 `agent_exec_targets.sort_order`；server 持有的仍然只是账号级对象，不含会话内容。）

## Problem

桌面端证据取自源码（未跑真机取证；mockup 为按源码 1:1 重建，见「相关链接」）。

1. **作用域切换器无归属且盖在标题之上。** 两个并排的 `Button`（`frontend/src/components/agentre/org/org-detail-agent.tsx:681-702`）悬在 `ExecTargetList` 自带的「执行目标」标题上方，既不是 Tabs 也不是段控件，层级倒置成「切换器 → 标题 → 列表」。
2. **同一份列表两个视图、能力不对称。** 本设备作用域隐藏了添加 / 移除 / 更换（`org/exec-target-list.tsx:189-191, 201-235, 254`）。用户要加一台机器，第一步是判断「这件事属于账号默认」再切过去 —— 而增删本来就不是顺序问题。
3. **「恢复为账号默认顺序」恒显。** `HasOverride` 由服务端返回（`internal/service/chat_svc/exec_pick.go:58-60`）、hook 也透出了（`org/use-exec-target-availability.ts:27,51`），但 `org-detail-agent.tsx` 从未消费它（只在 `:209` 的注释里提及）。没有覆盖时按钮照样在，点下去只是写一次空数组。
4. **第 1 档为什么在第 1 看不出来。** 无覆盖时是本机档被自动提前，有覆盖时是用户手排的，两种成因渲染完全一致（`internal/service/chat_svc/exec_target_order.go:88-95`）。
5. **切到账号视图后的顺序对不上，且无解释。** 账号默认视图里的顺序不是这台电脑实际派发的顺序，两份列表外观一致，切换时只表现为「行跳了一下」。
6. **单端用户被迫理解双层模型。** 账号下只有本机时两个视图恒等，界面仍把「账号 / 本设备」摊开，与前置规格 R22「单端界面零变化」冲突。
7. **加载窗口里的文案自相矛盾。** `deviceTargets` 初值为空且仅在 `orderedTargets.length > 0` 时写入（`org-detail-agent.tsx:223-228`），加载完成前渲染的是空态卡片：大字「还没有执行目标」+ 小字「本设备顺序尚未加载」（`org/exec-target-list.tsx:244-253`）。
8. **「当前生效」徽标在账号视图里贴错行。** `firstAvailableIndex` 按传入的那份顺序算（`org/exec-target-list.tsx:121-123`，用于 `:183` 的 `isFirstAvailable`）。账号视图传入的是账号顺序，于是绿标贴在账号首档上 —— 而本端有覆盖时根本不派给它。界面在断言一件假事。
9. **web 端完全无法调整派发顺序。** web 控制台路由只有 `/overview` `/devices` `/devices/:id/sessions` `/chat` `/audit` `/login` 与 device flow 几屏（`agentre-server/frontend/src/App.tsx:21-82`），没有任何 Agent 编辑入口；Overview 的 Agent 卡片只读渲染执行目标链，顺序纯粹来自同步下来的 `agent_exec_target` 载荷里的 `sort_order`（`workspace_svc/workspace.go:280`）。浏览器只能接受账号默认顺序。
10. **一档执行目标在 web 侧没有稳定标识。** `DispatchTierItem` 只有 `rank`（位置性，重排即变）、`device_id`（**不唯一** —— 一台设备可以挂多个 backend，例如同一台机器上的 Claude Code 与 Codex）与 `backend_type`（同型可重复）。`buildAgentChains` 解析时拿到了 `BackendSyncID` 却在生成 `resolvedTarget` 时丢弃（`workspace.go:294-306`）。因此今天没有任何键可以用来表达「这一档排第几」。

## Actors and user stories

1. 作为**只有一台电脑的用户**，我希望执行目标那块就是一个列表，不要出现「账号」「本设备」这类我用不上的概念。
2. 作为**有多台机器的用户**，我希望在哪台电脑上排的顺序就只影响哪台，不会被别处的改动覆盖掉。
3. 作为**要给 Agent 加一台机器的用户**，我希望直接在看到的这个列表上加，而不必先想明白这属于哪个作用域。
4. 作为**用浏览器发起对话的用户**，我希望在浏览器上也能把常用的那台机器排到前面，并且这个偏好在我下次打开时还在 —— 换一个浏览器、清过站点数据之后也还在。
5. ~~作为**在手机和电脑上都开过控制台的用户**，我希望两个浏览器各自记住各自的顺序，互不覆盖。~~ **（2026-08-16 撤销）** 浏览器之间没有任何物理差异能证成不同的顺序：桌面端每台排序不同是因为「这台机器上有本机档、那台没有」，而浏览器**永远跳过本机相对引用**（R15d，`workspace.go:501-503`），任何浏览器面对的候选集都是账号下的全部 agentred，同一批机器、同一个先后。改为：手机和电脑上看到并编辑的是同一份顺序。
6. 作为**删掉了某个 Agent 的用户**，我希望各个浏览器为它排的顺序一并消失，不残留在账号里。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 桌面端取消作用域切换，只保留一个列表；它恒等于「这台电脑当前实际的派发顺序」。 | 用户裁决（2026-08-14）。问题 1/2/5/6 同源于「作用域被做成了视图切换」，用户进来要干的事都被「先选视图」挡住。拒绝方案 B（把两个 `Button` 换成真段控件、补齐覆盖态徽标）—— 它修掉了问题 1/3/4/6/8，但保留了问题 2 与 5，且两个 tab 控件布局不同、切换时「添加」按钮位置会跳（见 mockup `05-plan-b.png`）。 |
| 2 | 界面上不再出现「账号默认顺序」，它退化成不可见的初始值 / 兜底。 | 用户裁决（2026-08-14）：「各个设备觉得不对劲了，自己去调整不就行了」。新端第一次拿到它、就地拖一下即可，不需要一个专门编辑它的入口。拒绝初版设计（保留次级入口 + 弹层 + 覆盖态灰/蓝两态）—— 它仍要求用户理解双层模型。 |
| 3 | 本轮推翻前置规格 [2026-08-11 桌面端互访](./2026-08-11-desktop-peer-access.md) 的 R16 第二句，并修订该文件。 | R16 写着「默认顺序本身在何处编辑也要有明确入口，不得只能靠某一端的覆盖间接影响它」，与决策 2 直接冲突。代码与已提交规格不得对不上，因此本轮同时提交对 R16 的修订。R14（解析规则）与 R22（单端零变化）不变。 |
| 4 | 桌面端不再消费 `HasOverride`，也不提供「恢复为账号默认顺序」。 | 决策 2 之后没有可见的「默认」，「恢复默认」失去指称对象；覆盖态徽标要区分的「自动 vs 手动」同理失去意义。已知代价：拖过一次就永远处于本端顺序，没有回到「本机自动提前」的路径 —— 等价操作是把本机拖回第 1 位，代价接近零（用户裁决接受）。`HasOverride` 字段服务端照常返回，不删。 |
| 5 | 两端都不渲染任何解释作用域的说明行。 | 用户裁决（2026-08-14）：「不需要这个多余的说明」，与既有偏好一致 —— 界面只写「现在会跑什么」，不写总述性解释条。列表本身已经回答了「会派到哪」，一句「顺序只影响这台电脑」既不改变用户能做的事，也不改变结果。**已知代价**：用户在另一端发现顺序不同时，界面不解释原因（用户已就此裁决两次）。因此桌面端不再需要判断单端/多端，R22「单端界面零变化」天然成立。拒绝多端时显示（初版设计）；拒绝恒显。 |
| 6 | web 端的顺序存服务端新表，不用 `localStorage`。 | 用户裁决（2026-08-14，在下述更正之后重新确认）。依据：① 顺序由服务端解析（决策 8），存在服务端就不必把整份排列塞进每一次取派发计划的请求；② 同一账号下多浏览器各一份是**账号内偏好命名空间**的自然形态，而不是靠同源隔离得来的巧合；③ Agent 被删除时服务端能连带清掉引用它的排列（`sync_svc.Push` 落 agent 墓碑时直接 DELETE），`localStorage` 做不到这件事。**已作废的依据**（2026-08-15 `browser_relay_clients` 迁移之后）：初版写的「解除授权 / 删除设备时能连带清掉它的顺序」与「将来设备身份若改为账号派生，顺序自动幸存」——那次迁移把浏览器从设备模型里拆了出去（键由 `device_id` 换成浏览器自持的 `client_id`，`kind='web'` 的设备行全部删除），服务端不再有「撤销某个浏览器」这个动作，这两条都没有对应物了。**已知代价**：浏览器换掉 `client_id`（清站点数据 / 换浏览器 / 隐私窗口）后旧行成为孤儿，服务端**没有任何回收触发点**。**已更正的错误依据**：先前提出「`localStorage` 会被 Safari ITP 清掉导致派发目标悄悄变」，该论据不成立 —— web 设备指纹本身就存在 `localStorage`（`agentre-server/frontend/src/lib/webDevice.ts:30,147`），清掉后会注册成一台新设备、服务端那行随之成为孤儿，用户可见结果与存 `localStorage` 完全相同。拒绝 `localStorage`（我方初版提议，已收回）。**本条已作废（superseded，2026-08-16，决策 14）**：浏览器不再有「自己那一份」顺序，因此不存在存在哪里的问题。三条依据的去向：① 顺序仍由服务端解析，且现在就住在集合里，比存另一张表更短；② 「多浏览器各一份」正是被撤销的那条；③ Agent 删除时连带清理**变成自动的** —— `sort_order` 长在 `agent_exec_target` 行上，Agent 的墓碑落地即随行消失，不再需要一次单独的 DELETE 与它的失败处理。上面那条「浏览器换掉 `client_id` 后旧行成为孤儿、服务端没有任何回收触发点」的**已知代价随之消失**。 |
| 7 | 新表键为 `(user_id, device_id, agent_sync_id)`，值为 **backend sync_id 的有序数组**。 | 问题 10：`device_id` 不唯一（一台机器可挂多个 backend），`rank` 是位置性的。`BackendSyncID` 是跨机稳定且逐档唯一的既有标识（`agentExecTargetPayload`，`workspace.go:201-205`）。键用 `device_id` 而非 `device_fingerprint`：一是隔壁 `followed_sessions.device_fingerprint` 指的是**目标**设备，同名反义必被读错；二是 `device_id` 只能由指纹解析 `devices` 行得到，从而让决策 9 的账号归属校验**结构上绕不过去**，而不是一条容易漏写的规则。形状与既有 `device_local_paths` 一致。拒绝以指纹为键（同名反义 + 校验可绕）；拒绝把顺序写回同步对象的 `sort_order`（那是账号级的，会污染所有端）。**键已变更（2026-08-15）**：`browser_relay_clients` 迁移把浏览器移出设备模型，表改为 `browser_exec_target_orders`、键改为 `(user_id, client_id, agent_sync_id)`，`client_id` 是浏览器自持、服务端无对应记录的标识。**上面第二条依据随之失效**——`client_id` 直接来自请求参数，不再需要（也无从）解析 `devices` 行，决策 9 那条「结构上绕不过去」的约束因此不复存在（见决策 9）。第一条依据（同名反义）与两条拒绝方案仍然成立。**本条已作废（superseded，2026-08-16，决策 14）**：整张表被删除，键的形状不再有指称对象。其中「拒绝把顺序写回同步对象的 `sort_order`（那是账号级的，会污染所有端）」这一条**被明确推翻** —— 理由见决策 14。 |
| 8 | 浏览器把自己的顺序**交给服务端解析**，不自行挑档。 | 浏览器自行挑档需要每一档都带 `device_fingerprint` 与 `cwd`，后者直接违反硬不变量 2（R19：路径只随选中档出现）。让服务端按调用方的顺序重排后走既有的「第一个可用」循环，红线不动。拒绝在 `DispatchTierItem` 上铺 `cwd`。 |
| 9 | 服务端读写顺序时必须校验传入的设备指纹属于调用方账号。 | `/v1/workspace/*` 走 `SessionOrDeviceAuth`（`internal/api/router.go:76-89`），身份是用户不是设备，所以设备指纹只能由参数传入。同步组的既有约定是「账号与设备一律取自 JWT claims，不接受参数里的身份」（`router.go:94`）；本组做不到这一点，因此以「先按 `(user_id, fingerprint)` 解析出 `devices` 行、解析不到即拒绝」补上：`device_id` 是主键的一部分，拿不到它就无法读写，校验因此不可绕过（决策 7）。不得直接信任参数里的身份。**本条已失效（superseded，2026-08-15）**：`browser_relay_clients` 迁移把浏览器移出设备模型之后，`client_id` 没有任何服务端记录可供比对，这条校验**没有对应物**，实现里也已经不存在。今天真实成立的边界只有一条：`user_id` 取自 JWT，因此跨账号写不进去；`client_id` 是调用方自报的浏览器标识，服务端不校验也无从校验，它只起「同一账号内的偏好命名空间」的作用。**这一条曾在代码注释里被宣称仍然生效，是比缺失本身更危险的失真**——读代码的人会以为有一道校验在——已于 2026-08-16 一并清理。 |
| 10 | web 的排序控件放在 Overview 的 Agent 卡片上，用上移 / 下移按钮，不引入拖拽库。 | 卡片上今天已经渲染这条执行目标链（`frontend/src/pages/Overview.tsx:39,300`），是「这个 Agent 会派到哪」的既有落点。`agentre-server/frontend/package.json` 没有任何拖拽依赖；此体量下上移/下移按钮足够且天然可键盘操作（桌面端那个列表本就同时提供拖拽与上下按钮）。拒绝新对话弹层（那是发起流程，不该塞配置）；拒绝为此引入 dnd-kit。 |
| 11 | `skipped_for_web` 的档不参与 web 排序。 | `device_id` 为空的「本机」相对引用在浏览器语境下永远不可派发（R15d，`workspace.go:501-503`），给它一个可排序的位置是纯噪音。它仍在只读链里显示，只是不可移动。 |
| 12 | 桌面端的 `agent_exec_target_overrides` 保持本地、保持本地自增 id 键，不上行同步。 | 「各端自己排」意味着两端本就不需要互相认识，桌面端今天这套工作正常。改成 sync_id 键要迁移 + 重写 R14 解析，换不来任何可观察收益。**已知重复**：同一个概念在两端有两套存储，若将来出现第三类客户端应重新评估。拒绝本轮统一（纯成本）。 |
| 13 | 排列以 JSON 数组整存整取，不拆成「一档一行 + rank 列」。 | 排列永远被整体读写，拆行要事务化整体替换且没有按档查询的需求；与桌面端 `AgentExecTargetOverride.OrderJSON` 同形，两端读同一种形状。拒绝一档一行（纯成本）。**本条已作废（superseded，2026-08-16，决策 14）**：顺序回到 `agent_exec_targets.sort_order`，本来就是「一档一行 + 序号列」的形态 —— 但它不是本条拒绝的那个方案，因为那一行的存在理由是执行目标本身，序号只是它的一个字段，不是为排序新建的结构。 |
| 14 | **浏览器排的就是账号默认顺序**（`agent_exec_targets.sort_order`），server 不再按浏览器区分；`browser_exec_target_orders` 整张表删除。 | 用户裁决（2026-08-16）：「server 可以当作为默认顺序，浏览器 server 这边就不用区分浏览器了」。三条依据：① **浏览器之间没有物理差异能证成不同的顺序** —— 桌面端每台排序不同是在表达「我这台机器上有本机档」这个硬事实，而浏览器永远跳过本机相对引用（决策 11 / R15d），任何浏览器面对的都是同一个候选集，那层覆盖什么都不表达；② **`client_id` 无凭据、无生命周期** —— 决策 9 那道校验已于 2026-08-15 失效且实现中不存在，`client_id` 是调用方自报、服务端无从校验的字符串，清站点数据即产生孤儿行且**没有任何回收触发点**（决策 6 的已知代价）；③ 它同时补上「账号默认由谁来写」这个洞 —— 决策 2 把账号默认从界面上取消了，但它仍是新端的初始值，而首轮之后只有桌面端能写它。**代价**：一个浏览器上的重排会改变所有**从未调整过**的桌面端的顺序 —— 这是账号默认的定义，不是副作用，见下方「跨端语义」。拒绝保留 `client_id` 维度（决策 6/7/9/13 的整套结构，换不来任何可观察收益，且带一条回收不掉的孤儿行）。 |

## 桌面端：组织架构页的执行目标列表

**列表是唯一视图。** 打开任一 Agent 详情，执行目标区显示一个列表，其内容与顺序恒等于服务端 `ListExecTargetAvailability` 返回的解析后顺序 —— 也就是这台电脑此刻真正的派发顺序。不存在第二个视图、不存在作用域选择。

**排序写本端。** 拖拽、拖拽柄上的方向键、上移 / 下移按钮三条路径都只写本端顺序覆盖，互不区分；它们的键盘等价物与当前行为一致。任何一次重排立即持久化并重新拉取可用性。

**增删恒可用。** 添加、移除、更换在任何状态下都在标题行/行内可用，写的仍是账号级执行目标集合。列表只剩一档时退化成今天的单行形态：无拖拽柄、无序号、无移除，只提供「更换」。

**加载与空态分离。** 顺序数据尚未到达时渲染骨架行，不得渲染空态卡片，更不得同时出现「还没有执行目标」与「尚未加载」两句互相否定的文案。真正的空态（该 Agent 没有任何执行目标）保持今天的形态与「至少要有一项」的保存约束。

**「当前生效」只贴一处且必然正确。** 徽标标记的是当前列表里第一个可用档；由于只剩一个列表，且它就是派发顺序，问题 8 的错贴不再可能发生。全部档不可用时保持既有的逐档原因横幅。

**窄面板。** 详情面板宽 380px（`frontend/src/components/agentre/org-chart-page.tsx:365`），去掉切换器后标题行只剩标题与一个增删按钮，长机器名在行内截断，面板不得横向溢出。

## web 控制台：浏览器排的就是账号默认顺序

> 本节为 2026-08-16 修订后的形态（决策 14）。首轮的「每浏览器一份 + `browser_exec_target_orders` 表」已整体撤销，不再是本规格的一部分。

**顺序住在集合里，没有第二处存储。** 被排序的是 backend，序号就是 `agent_exec_targets.sort_order` —— 它是同步组 `KindAgentExecTarget` 载荷里的既有字段（`workspace.go:280` 今天就读它来渲染执行目标链）。浏览器重排 = 改这些行的 `sort_order`，走 server 直写 `sync_objects` 的写入路径，`Version` 照常由账号级单调序列分配。**server 侧不新增任何表。**

**写入是整行读-改-写。** 前置规格决策 4 把字段级冲突合并列为非目标，`sync_objects` 是整行 last-write-wins。浏览器改 `sort_order` 时必须带上该行的完整载荷回写，**不得把 `skills_json`（R15e 的按档技能授权）冲掉**。浏览器渲染执行目标链用的就是这份完整载荷，无需额外拉取。这张表的字段是 `(agent_id, agent_backend_id, sort_order, skills_json)` + SyncMeta，没有路径 / `CLIPath` / `EnvJSON`，因此浏览器持有整行不触 R19。

**Agent 删除时顺序自动消失。** `sort_order` 长在 `agent_exec_target` 行上，Agent 的墓碑落地时那些行随之失效，不再需要一次单独的 DELETE、也不再需要它的失败处理与「只判 accepted 会漏掉 conflict」那条判据。首轮那条「一个再也不回来的浏览器留下的行没有回收触发点」的已知限制**随之消失**。

**派发计划直接按集合顺序解析。** 服务端加载执行目标链时顺序已经是最终顺序，不再需要按调用方重排 —— `WebDispatchPlan` 去掉 `deviceFingerprint` 参数，`Chosen` / `Current` / 逐档原因照旧由既有的「跳过本机相对引用、取第一个可用」循环产出。首轮那条「读路径与写路径对解析失败处理刻意不同」的规则一并作废：没有偏好行要读，也就没有读失败这回事。

**每一档要有稳定标识。** 执行目标链的解析中间结果与对外的档结构都要带上 backend sync_id，供浏览器指名要移动哪一档。它是既有同步载荷里的字段，不新增任何用户数据。（本条首轮即成立，修订后仍然需要。）

**排序界面。** Overview 的 Agent 卡片上，执行目标链的每一档提供上移 / 下移；`skipped_for_web` 的档只读、不可移动。一次移动即提交并重新拉取该 Agent 的链，卡片上的「当前」标记随之更新 —— 用户因此当场看到自己这一下改变了派发目标。提交失败时保持原顺序并就地说明，不得只在控制台打日志。

**界面上仍然不出现「账号默认顺序」这五个字。** 决策 2 不变：用户看到的就是一个列表，拖一下即生效。他不需要知道自己编辑的这一份同时是别的端的初始值。

## 跨端语义

**账号默认一份（浏览器编辑），桌面端每台一份覆盖。** 桌面端的解析规则不变：有本端覆盖用覆盖，无覆盖用账号默认且本机档提前（`ResolveExecTargetOrder`）。因此：

- 浏览器上的重排**会**改变所有**从未调整过**的桌面端的顺序 —— 这是账号默认的定义，不是副作用。
- 已经拖过一次的桌面端有本端覆盖，**不受影响**；而决策 4 取消了回到默认的路径，所以它此后一直用自己那份。
- 任一端的排序都不改变执行目标**集合**（增删）；集合仍只能在桌面端修改。
- 两个浏览器同时排会互相覆盖，语义与其它账号级对象一致（last-write-wins，`Version` 由账号序列裁决）。

**已知代价（用户裁决接受）：** 界面不解释这件事（决策 5：不渲染任何解释作用域的说明行）。一个只用浏览器、从没在桌面端拖过的用户，在浏览器上改顺序会看到桌面端跟着变，而界面不会告诉他为什么。这与决策 5 已经接受的那条代价同源。

## Out of scope

- **桌面端顺序上行同步。** 决策 12：桌面端的每端覆盖仍是本地表、不上行。（2026-08-16：原文「与 server 新表统一」已无指称对象 —— server 那张表被删了。剩下的两层没有需要统一的重复。）
- **web 端增删执行目标。** 本轮 web 只排序，不改集合；集合的编辑仍只在桌面端。（组织架构 / Agent 的 web 侧写入是另一轮的事。）
- **一个叫「账号默认顺序」的编辑入口。** 决策 2 明确取消，不在任何一端提供。2026-08-16 后浏览器编辑的**实际上**就是它，但界面上不这么称呼、也不提供第二个视图。
- **按使用频率或最近活跃自动学习顺序。** 沿用前置规格「非目标」，本轮不做。
- **移动端专属排序界面。** web 控制台的移动布局沿用 Overview 既有响应式，不为排序单开一套。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 桌面 `exec-target-list.test.tsx`（vitest） | 单列表形态：无作用域切换器；增删在任意状态可用；一档时退化成单行；加载态是骨架而非空态；「当前生效」贴在第一个可用档 | 既有 `frontend/src/components/agentre/org/__tests__/exec-target-list.test.tsx` |
| 桌面 `org-detail-agent-r16.test.tsx`（vitest，**重写**） | 重排只写 `orderOverride`、不写账号执行目标；不再存在「恢复为账号默认顺序」入口 | 既有同名文件（当前断言双作用域，须随决策 1/4 重写） |
| 桌面 `i18n.test.ts` + locale parity | 删除的 `org.agent.execTargets.scope*` / `restoreDefault` / `deviceScopeHint` / `deviceEmptyHint` 键无残留引用；新增说明行的键两套 locale 齐备 | 既有 `frontend/src/__tests__/i18n.test.ts` |
| ~~server 顺序仓储（sqlmock）~~ | **2026-08-16 删除**：`browser_exec_target_orders` 与 `exec_order_entity` 一并删除，无仓储可测 | — |
| ~~Agent 删除连带清顺序（server 单测）~~ | **2026-08-16 删除**：`sort_order` 长在 `agent_exec_target` 行上，Agent 墓碑落地即随行失效，没有单独的清理动作可测 | — |
| server `sort_order` 写入（**新建**，sqlmock + mock 仓储） | 重排只改 `sync_objects` 里该 Agent 各 `agent_exec_target` 行的 `sort_order`；**整行回写不得改动 `skills_json`**（守卫断言 —— 这是不写就会错的默认行为）；`Version` 由账号序列分配；跨账号写不进去（`user_id` 取自 JWT） | `internal/repository/sync_repo/sync_test.go` 既有 sqlmock 结构 |
| server `workspace_svc` 顺序解析（纯函数 + mock 仓储） | 执行目标链按 `sort_order` 升序；`skipped_for_web` 档不被提前成 `Chosen`；不再存在「有排列 / 无排列」两条分支 | `internal/service/workspace_svc/workspace_test.go` |
| server `workspace_ctr`（controller test） | 顺序写端点不再接受 `client_id`；派发计划不再接受 `deviceFingerprint` 重排参数；派发计划返回 `Chosen` 与逐档原因 | `internal/controller/follow_ctr/follow_test.go` |
| server 迁移链 | `DROP TABLE browser_exec_target_orders` 追加在 `migrationList()` 末尾且全新库跑通；不修改既有迁移 | 既有 `migrations/migrations_test.go` |
| web `overview.test.tsx` / 新增排序测试（vitest） | 上移/下移提交并刷新；`skipped_for_web` 档不可移动；提交失败保持原顺序并就地说明 | 既有 `agentre-server/frontend/src/__tests__/overview.test.tsx` |

**无法自动化的部分：** 「浏览器改了、没拖过的桌面端跟着变、拖过的不变」这条端到端事实需要同时具备一台桌面端与一个浏览器会话，本轮不建这样的 e2e 装置。收尾时以源码复核覆盖：确认桌面端写入路径**只**触碰 `agent_exec_target_overrides`（不写 `sort_order`）、web 写入路径**只**改 `agent_exec_target` 行的 `sort_order`（不碰 `skills_json`、不碰桌面端覆盖表），两者没有交叉引用。

**删除后要确认无残留：** `git grep -n "browser_exec_target_orders\|exec_order_entity\|client_id" -- agentre-server/` 在改动树上不得再命中执行目标顺序相关的代码路径（`client_id` 在 device flow 里另有用途，逐处确认）。

## 相关链接

- 前置规格：[2026-08-11 桌面端互访](./2026-08-11-desktop-peer-access.md)（R14 解析规则不变；**R16 由本轮修订**；R22 单端零变化）
- 前置规格：[2026-08-10 浏览器接入中继读写会话](./2026-08-10-web-session-access.md)（R15/R15d 派发计划与 web 语境跳过本机档；R19 路径红线）
- Mockup（本地产物，不在 Git 里）：`.dev-kit/artifacts/2026-08-14-exec-target-order-ux/mockups/` —— `?view=now` 现状与 8 处问题标注、`?view=a` 定稿形态三态、`?view=sheet` 砍掉了什么 vs 后端保持不动、`?view=b` 被否决的方案 B、`?view=states` 状态矩阵

## Open questions

无。

**已裁决（2026-08-16）：删表时现存行直接丢弃，不回填。** 用户裁决「a」。`DROP TABLE browser_exec_target_orders` 即全部内容，不写回填 SQL；账号 `sort_order` 保持桌面端定的那份。**已知代价**：在浏览器上排过序的用户会看到顺序回到桌面端那一份，界面不解释（与决策 5 同源）。依据是本项目未发布、迁移可硬删老数据不需要兼容层；回填方案（按 `updatetime` 最新行择一）虽然只是一条一次性 SQL，但要在「哪个浏览器说了算」上引入一条只用一次的裁决规则，不值。
