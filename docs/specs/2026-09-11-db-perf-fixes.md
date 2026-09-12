# 数据访问层性能与事务正确性修复（桌面端 + agentred）

> Status: Approved
> Owner: internal/repository、internal/daemon、internal/service（chat_svc / sync_svc / project_svc / hook_svc / agent_backend_svc / exec_target_svc）
> Last updated: 2026-09-11

**Objective:** 消除已核实的全量读、N+1、逐行写锁与缺失事务边界，使打开/补齐/同步/删除这些高频路径的数据库读写量不再随会话历史长度或行数线性（或平方）增长，并让几条多步写入在失败时不留半截状态。

**Hard invariant:** 所有改动对用户可见的结果（列表内容与顺序、帧编号、同步语义、错误码）保持不变，下文「可观察的要求」里明确写出的变化除外。

## Problem（均已核实）

证据来自 2026-09-11 的五路核实（只读，基线 `dev@4775070f`）：SQLite 部分用生产驱动栈（glebarez/go-sqlite v1.21.2，SQLite 3.41.2）对迁移 DDL 抽出的临时库、以 `?` 绑定参数跑 `EXPLAIN QUERY PLAN`，并用合成数据计时。

**读放大**
1. `daemon.go` `LatestSeqByPeer` 对对端**每条**会话读回整条转录（含全部块正文）并全量投影，只为算一个 seq；`session_catchup.go` 每请求一页都调它，与 limit 无关。
2. `daemon.go` `numberBacklog` 每次 `StartTurn` 都读回整条转录。
3. `peerstream/history.go` `attachPeerTranscript` 持 `publication.mu` 读全文与编号，期间该会话逐 token 发布被阻塞。
4. `transcript_repo/message_block.go` `findSubagentStateBlocks` 走 `(type, message_id)` 索引扫全库 subagent_state 块（72 万块库实测 1.58ms，改子查询 0.12ms，随全库线性）。
5. `chat_svc/read_path.go` `LoadMessageBlocks` 每次上翻读全部消息元数据后在 Go 里切片。
6. 六处为找一条消息/一类块读整条转录：`plan_action.go`（且整条消息块 DELETE+INSERT 重写）、`chat.go` regenerate anchor、`transcriptfork/fork.go`（pi / codex 两处）、`written_paths.go`、`ipc/deps.go`。
7. `syncstate_repo/syncstate.go` 四处裸 `sync_id = ?` 用不上 `WHERE sync_id != ''` 部分唯一索引（SCAN），每条下行记录各打一次；`agent_repo/exec_target.go`、`issue_repo/label.go` 同形。`remote_device_repo/paired_agentred.go` 两处同理。
8. `sync_svc/downlink.go` `replayDeferred` 每行多次全表读入站队列（N 行 → 2N+2 次全表读、3N 个写事务）；30 天回收全表读 + Go 里比 cutoff + 逐行删。
9. `chat_svc/chat.go` `ListChatAgents` 逐 agent 两次会话查询（2N）。
10. `agent_backend_svc.List` 每行 ≤3 次点查（含整表读配对设备）；`exec_target_svc` 每档重复查 backend/provider/项目路径（项目路径两遍）。
11. `issue_svc` `appendPosition` 读该 stage 全部 issue 只为取末位 position。
12. `daemon_sessions` 上按 createtime / lifecycle_state 的查询 SCAN + 临时 B 树；`hook_events` 最近事件列表 SCAN + 临时 B 树（20 万行 68ms）；`chat_sessions` `ListForRollup` 的 createtime 下界无索引（5 万会话 30ms）；`idx_projects_parent_id` 是 `idx_projects_parent_sort` 的严格前缀。

**写放大**（桌面端 DSN `_txlock=immediate`，每个事务 BEGIN 即取写锁，与流式落库同一把）
13. 出站队列认领每行 Create+Delete、入队逐行 Create。
14. `ClaimForAccount` 在一个写事务里逐行 UPDATE。
15. `IssueLabel.UpsertFromSync` 先查再写。

**事务正确性**
16. agentred DSN 缺 `_txlock=immediate`：先读后写的事务在另一连接提交后 **0.0ms 即 `database is locked`**（WAL + busy_timeout 实测），`FrameSeq.Allocate` 今天就是这种形状。
17. agentred `StartTurn` 的 NextSeq → Create(user) → Create(assistant) 三个独立事务；桌面端 `session_provider.go` notice 消息 NextSeq 在事务外。
18. `AppendSubagentChildren` 更新父块与追加子块不在同一事务，`appendBlocks` 的 `MAX(idx)+1` 是 check-then-act。
19. `project_svc/merge.go` 合并项目六步跨仓写无事务，中途失败留下半合并的库。
20. `hook_svc/run.go` 事件去重先查再插：竞态下 Create 撞唯一索引报错提前返回，`last_run` / `next_run_at` 不回写，剩余事件丢弃。
21. 桌面端删会话只软删会话行，转录 / 块 / 帧台账 / 替换恢复标记永久驻留。
22. daemon `InterruptAll` 把所有未结束会话的 `last_message_at` 刷成同一毫秒，抹掉「最近活动」顺序；会话列表排序无 id 兜底。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | A1 本轮只做「只对当前页会话求最新 seq」 | 用户决定。Rejected: 台账 `MAX(seq)`（未编号积压时偏小，改协议语义）/ `latest_seq` 列（迁移 + 维护不变量） |
| 2 | A2 用进程级 memo：同会话 `numberBacklog` 成功后记下，`AllocateFrameSeqs` 失败或删会话时清除 | 核实员推荐，不可观察；跨重启的积压仍由首轮补齐，与今天一致 |
| 3 | agentred 启用 `_txlock=immediate`，删掉冗余 `busy_timeout` | 用户决定。与桌面端一致，是决策 4 的前提 |
| 4 | StartTurn 三步包进一个事务；桌面端新增仓储方法 `CreateAtNextSeq(ctx, msgs ...*Message)` 收取号 + 建行 | 用户决定。Rejected: 服务层就地包事务——mockgen 观察不到边界 |
| 5 | `InterruptAll` 只写 `lifecycle_state`；daemon 会话列表排序补 `, id DESC` | 用户决定。启动清扫不是「活动」 |
| 6 | 删会话物理清除转录、块、帧台账与替换恢复状态；会话行仍软删；**不回填**历史已软删会话 | 用户决定。与 agentred、architecture.md「rows go away only with their session」一致；桌面端无恢复功能（已核实） |
| 7 | provider 批量取新增**不过滤状态**的批量方法，保留软删 provider 仍显示名字 / `ProviderInactive` 判定 | 用户决定。Rejected: 复用只取 ACTIVE 的 `BatchFindByKey`——会丢名字并改变拦截原因 |
| 8 | 合并项目的写入包进一个事务，同步通知收集后在**提交成功后**发出；成员改挂保持 Add+Remove | 用户决定。`NotifyLocalChange` 的 `context.WithoutCancel` 会把 ctx 里的 tx 带进后台同步，事务内通知会用已提交的 tx |
| 9 | 纯索引迁移不写先红单测，证据为「迁移后真库前后 EXPLAIN 留档 + 迁移链跑通」 | 用户决定（豁免 AGENTS.md TDD 第 1 条，仅限纯 DDL 索引迁移）：SQL 文本不变，`docs/testing.md` 禁止在单测里断言索引 |
| 10 | 新迁移追加到各自 `migrationList()` 末尾，ID 取 `20260911xxxx`；桌面端与 agentred 两套账本各自编号 | AGENTS.md 第 7 条 |
| 11 | hook 去重改为 `INSERT … ON CONFLICT DO NOTHING` 按受影响行数判重，删除只剩此一处调用的 `FindByDedupeKey` | 实测 `ON CONFLICT DO NOTHING` 对部分唯一索引生效，空 dedupe_key 行不受影响 |
| 12 | 部分索引可用性写进 SQL：对 `!= ''` 型谓词在查询中显式带同一条件 | SQLite 不能从 `col = ?` 推出 `col != ''`（绑定值下仍 SCAN）；`status = 1` 型谓词绑定值下可用，不改 |

## 可观察的要求

**事务正确性**
1. agentred 上一个先读后写的事务，在其读与写之间另一连接提交了写入时，该事务仍提交成功。
2. agentred `StartTurn` 中任一写入失败时，转录里不留下该轮的任何消息行；桌面端 notice 消息的取号与建行在同一事务内完成。
3. `AppendSubagentChildren` 的父块更新与子块追加要么同时生效，要么都不生效。
4. 合并项目任一步失败时，库中不留下任何已执行步骤的改动，且不发出任何同步通知；成功时同步通知在提交之后发出。
5. hook 同一运行或并发运行中遇到已存在的 dedupe key 时，事件计入重复数、运行正常结束并回写 `last_run` 与 `next_run_at`，其余事件照常写入。
6. 删除桌面端会话后，该会话的 `chat_messages`、`chat_message_blocks`、`chat_frame_seqs` 行与替换恢复标记 / 隐藏行都不存在；任一清理失败时删除返回错误；会话行保持软删。
7. agentred 启动清扫后，被中断会话的 `last_message_at` 保持清扫前的值；会话列表在 `last_message_at` 相同时按 id 降序稳定排列。

**读写放大**
8. `session.list` 一次请求只对返回页内的会话求最新 seq。
9. agentred 同一进程内同一会话第二次及以后开轮，不再读回整条转录。
10. 对端 attach 初始化读转录期间，同一会话的逐 token 发布不被阻塞。
11. subagent 状态块查询先按会话收窄（本会话消息 id 子查询），不再扫描全库同类型块。
12. 上翻分页只读 `seq < BeforeSeq` 的末尾 `limit+1` 条消息元数据；窗口内容与 `HasMore` 与今天一致。
13. 清除 plan actions 只改写 plan 块所在行；regenerate 找 user anchor、fork anchor 判断、写入路径提取、权限模式取最后一条 assistant，都不再读回整条转录（builtin 历史重放与 attach 投影除外）。
14. 入站队列重放一轮内对队列的读取次数不随队列行数增长；过期回收只读取过期行并批量删除；重新暂缓的行保持最早一次暂缓的时间；回收仍「先记录丢失、后删除」。
15. 匿名出站队列认领为一次写入；同一 kind 的认领 / 入队为一次批量写入。
16. 按 `sync_id` 的查询与更新走部分唯一索引；`ClaimForAccount` 对已有 sync_id 的行按集合更新；`paired_agentreds` 按 url / 指纹的查询走部分唯一索引，空 url 不发 SQL。
17. `IssueLabel.UpsertFromSync` 为单条语句。
18. `ListChatAgents` 的会话查询条数与 agent 数无关（每 agent 仍各取最近 5 条 / 关注 20 条，排序与过滤不变）。
19. backend 列表与执行目标可用性的数据库查询条数与 backend / 执行档数无关；软删 provider 仍显示名字、判定仍为 `ProviderInactive`。
20. 新建 issue 取末位 position 为一次聚合查询。
21. 迁移后：`daemon_sessions` 按 lifecycle / createtime / 全表最近排序的查询、`hook_events` 最近事件与按 hook 列表、`ListForRollup` 均走索引且无临时 B 树排序（`ListForRollup` 无下界分支除外）；`idx_projects_parent_id` 被删除后 projects 各查询计划不退化为 SCAN。

## Non-goals

- 保留策略 / 过期清理（hook_events、daemon_sessions 等的 retention）。
- 回填清理历史上已软删会话的转录。
- `latest_seq` 列、台账 MAX 算法。
- 名字唯一约束（A13，会破坏同步下行）、账号级同步表索引（A11）、issues 默认排序索引（A29）、FTS5、`app_settings` 恢复状态拆表、导入预取（A27）、`FindByProjectAndDevice` 索引（A32）、`remote_pool` ctx 透传（A36）、给已有迁移补 Rollback（A38）、嵌套 SAVEPOINT（A40）。
- 核实中发现但不在本轮：daemon Pull 每页读两遍转录、`peerPublications` 常驻内存、`ClaimForAccount` 的 SELECT 在事务外的竞态、label 同步改名撞唯一键卡在暂缓队列。

## Testing decisions

| Seam | What it verifies |
| --- | --- |
| repo sqlmock（`testutils.Database(t)`） | 新 SQL 形状与参数：子查询、`sync_id != ''`、集合 UPDATE、`ON CONFLICT`、`ListMetaBefore` / `LatestBeforeSeq` / `CreateAtNextSeq` 的 BEGIN→SELECT MAX→INSERT→COMMIT、错误路径 |
| service mockgen / 手写 fake 计数 | 批量方法 `Times(1)`、逐行方法 `Times(0)`；调用次数不随输入条数增长；合并失败不发通知、通知在提交后 |
| `internal/daemon` 真库测试（既有例外） | `_txlock=immediate` 下先读后写事务成功；StartTurn 在 assistant 写失败时不留行（触发器注入失败） |
| peerstream 并发测试 | attach 读转录阻塞时 `PublishEvent` 在限时内返回 |
| 迁移 | 迁移链在 `bootstrap/cago_test.go` 跑通；真库前后 EXPLAIN 输出留档于 `.dev-kit/artifacts/db-perf-fixes/`（决策 9） |
| 表征测试（改前改后都绿） | 重新暂缓保持最早 ReceivedAt；回收边界 `createtime == cutoff` 删除、`cutoff+1` 保留；软删 provider 仍显示名字 |

## Out of scope

- agentre-server 侧的同名修复归 `agentre-server` 的 `docs/specs/2026-09-11-db-perf-fixes.md`，两仓独立提交。

## Open questions

<!-- 空 -->
