# web 控制台向桌面端收敛：能搬什么、长什么样

<!-- File: docs/specs/2026-08-16-web-console-convergence.md -->

> Status: **讨论中（未批准）** —— 2026-08-16 与用户讨论的记录，用户明确「其它的后面再讨论」。
> 本文只记录**已经查证的事实**与**讨论到的方案**，不构成实施授权。
> Owner: 组织架构 / 项目 / web 控制台（跨 `agentre` 与 `agentre-server`）
> Last updated: 2026-08-16
> Mockup（本地产物，不在 Git 里）：`.dev-kit/artifacts/2026-08-16-web-org-management/mockups/`
> —— `?view=gap` 两端外壳对照、`?view=inventory` 全功能面可行性清单、`?view=converged` 收敛后的项目、
> `?view=org` 收敛后的组织、`?view=mobile` 移动端分叉、`?view=walls` 两堵墙。

## 起因

用户问：agentre-server 能不能也做项目、组织架构、Agent 管理，让 server 连到一台 agentred 就能干活
（不需要桌面端）。随后指出两个问题：**（一）为什么只有组织架构和项目；（二）为什么和桌面端的
UI/UX 差距这么大。** 本文是这两问的查证结果与方案记录。

## 事实一：账号级同步组只有七类对象，这条线决定了 web 能碰什么

`internal/model/entity/sync_entity/sync.go:16-22`（agentre-server）声明的同步 Kind：

```
project / department / agent / agent_backend /
agent_exec_target / project_agent / project_location
```

它们恰好拼成**项目 + 组织架构 + Agent 管理**这三样 —— 这就是「为什么只有这些」的答案。
而 `issue_entity` / `hook_entity` / `llm_provider_entity` **一个 SyncMeta 都没有**
（三个文件里 `grep -c SyncMeta` 全为 0），从来没上过账号，web 不是没做，是拿不到。

### 桌面端全功能面 × web 可行性

| 桌面端功能面 | 数据在账号里吗 | web | 拦路的是什么 |
|---|---|---|---|
| 对话 `/chat` | 会话不同步（workspace-sync 决策 11） | ✓ 已实现 | 经中继直连 agentred，内容不过 server |
| 项目 `/projects` | ✅ project / project_agent / project_location | ✓ 可做 | 路径只能盲写（R19，见事实三） |
| 组织 `/org` | ✅ department / agent / agent_backend / agent_exec_target | ✓ 可做 | 供应商凭据不跨机（决策 24） |
| 议题 `/issues` | ❌ `issue_entity` 无 SyncMeta | ◐ 要先上账号 | 一次迁移 + 一个 Kind + 载荷设计 |
| Hooks `/hooks` | ❌ `hook_entity` 无 SyncMeta | ◐ 配置可上，执行不行 | 脚本必须跑在 agentred 上，归属要重新定义 |
| 设置 · 供应商 | ❌ `llm_provider` 含 APIKey | ✕ 结构上不做 | 决策 24 明确排除「凭据的账号级托管」 |
| 设置 · 数据备份 | 本地文件 | ✕ 不做 | 要读写本机文件系统 |
| 会话内 · 终端 | — | ✕ 不做 | 要 pty，且终端语义就是「这台机器的」 |
| 会话内 · 文件 / Git | — | ◐ 可做但要新 RPC | 经中继读远端 fs；路径呈现受 R19 约束 |
| 会话内 · 技能授权 | ✅ 挂在 agent_exec_target | ◐ 可做 | 要问那台机器有哪些技能包（中继查询） |
| 命令面板 | 纯前端 | ✓ 可做 | 无 |

**这条线可以移，但每移一格要付一次代价**（一次迁移 + 一个同步 Kind + 一次载荷设计）；
而供应商凭据、终端、数据备份三样**不该移** —— 它们的本质就是「这台机器的」。

## 事实二：UI/UX 的差距来自两份设计稿，不是疏忽

agentre-server 的控制台原语在注释里写着自己的出处：

- `console/ConsoleNavItem.tsx:7` —— 「Pencil 正式组件 ZC7pI NavItem」
- `console/FilterChip.tsx:4` —— 「rNQXR FilterChip」
- `console/StatusMark.tsx:4` —— 「zF5jv StatusPill」
- `console/MobileTabBar.tsx:7` —— 「A6Z3k TabBar」

它们出自 `设计稿/agentre-server.pen`（workspace-sync 规格注明「本地产物，不在 Git 里」），
而桌面端来自另一份。两边各自成立，但**合起来不是一个产品**。

外壳的具体差距：

| | 桌面端 | server 控制台 |
|---|---|---|
| 外壳 | 44px 顶栏 + 56px 图标 rail + 320px 可调侧栏 + 38px 标签条 + 28px 状态栏 | 224px 带文字 SideNav + 52px 顶栏 + 页面 |
| 导航 | 对话 / 项目 / 议题 / 组织 / Hooks / 设置 | 总览 / 对话 / 设备 / 审计 |
| 会话 | 多标签并存 | 单路由 `/devices/:id/sessions/:sid` |
| 形态 | 工作台 | 管理控制台 |

## 讨论到的收敛方案（未批准）

| 面 | 处理 |
|---|---|
| **工作台面**（对话 / 项目 / 组织） | 完全用桌面端的外壳与组件，走共享包 `@agentre-ai/agentre-ui` |
| **账号管理面**（设备 / 审计 / 配对） | 保持 server 独有，桌面端没有对应物，进 rail 当第 4/5 个图标 |
| **移动端** | 唯一必须分叉：保留底部 TabBar，侧栏升整页，标签条退化成一次一个会话 |

web 版相对桌面端唯一的结构差异：顶栏右侧放账号，没有窗口按钮。

共享包的职责随之变大：从「转录渲染器」扩成 **「工作台外壳 + 索引 + 转录」**。

## 事实三：R19 不需要放宽

R19 原文约束的是「不出现在 agentre-server **发往浏览器的任何响应里**」——
它管的是**响应方向**。而配置项目路径是浏览器 → server，方向相反，因此不受约束：

- **写**：浏览器提交路径 → 落 `project_location` 载荷。R19 不适用。
- **读**：永远只回「已配置 / 未配置 / 已失效」。R19 原样保持。
- **改**：不是「编辑」（需回显当前值），是**「重新设置」**（输入框永远是 placeholder）。

同一条判据在代码里已有先例：`workspace_svc/workspace.go:117-122` 的 `WebDispatchChoice.Cwd`
注释区分「总览 / 设备两屏的**被动读视图**」与「一次**用户主动发起的派活**」，后者路径是动作参数。

**已知代价：盲写。** 打错一个字符只能通过「跑不起来」发现。缓解办法不碰 R19：提交时经中继让
agentred 就地校验（目录存在吗 / 是 git 仓库吗），**只回布尔 + 错误码，响应里不含路径**。
桌面端已有 `ProjectDetectGitRepo` 的先例。

> ⚠️ **不要做目录选择器。** 那是浏览器主动读远端文件系统布局，是标准的被动读视图，
> 正是 R19 存在的理由。讨论中一度提出过，已否决。

## 事实四：写入路径只能是「server 直写 `sync_objects`」

三个候选，只有一个成立：

- ✅ **浏览器 → server REST → server 直写 `sync_objects`**。`Version` 本就由 server 的账号级
  单调序列分配（「R4 唯一的胜负依据」），server 已经是权威写入点，只是今天只接受 daemon /
  桌面端推上来的内容。不需要桌面端在线。
- ❌ **浏览器伪装成设备 push sync**。`device_svc/device.go:520` 明确：「浏览器是 relay 的短效
  调用方，**不是可管理设备**」，并把 `KindWeb` 从设备列表里过滤掉。同步的冲突模型是设备级的
  （`SyncOrigin` 字典序打破平局），让短效浏览器当同步来源会往冲突元数据里灌垃圾。
- ❌ **浏览器经中继让 agentred 写**。agentred 只有 `daemon_sessions` 与
  `daemon_notification_logs` 两张表，它是纯执行端，不是数据端。

**这条路径的第一个落地点**是执行目标排序改写 `sort_order`，见
[`2026-08-14-exec-target-order-ux.md`](./2026-08-14-exec-target-order-ux.md) 的 2026-08-16 修订
—— 最小、语义无歧义、端点与界面都已存在。跑通之后部门 / Agent / 项目照抄同一条路。

## 事实五：浏览器 + agentred 不需要桌面端参与

一度担心「纯浏览器用户连第一台 agentred 都配不上」，查证后**不成立**。两种关系要分开：

- **桌面端 ↔ agentred**：LAN 直连配对 + TOFU，凭据在钥匙串，`paired_agentreds` 既不同步也不上报。
- **账号 ↔ agentred**：`agentred login` 走 device flow 把 daemon 登记到中继上。

浏览器要的是后者，而中继的可见性是**账号级**的（web-session-access 规格 R12：「已归属账号的
daemon 上，任一同账号客户端可以看到并操作该 daemon 上的全部会话」），不是配对级。
在远端机器上跑一条 `agentred login`，浏览器就能够到它。

## 待裁决（讨论时未定）

1. **桌面端 `/org` 的形态要不要改。** 今天是「树 + 380px 详情面板」，mockup 画的是
   「索引 + 主区详情」。两端在这一屏**没有任何能力差异**，没有理由长得不一样，但统一意味着
   **要改桌面端** —— 这是整个方案里唯一一处反向改动。倾向索引 + 主区（提示词 / 执行目标 /
   技能在 380px 里已经很挤），未定。
2. 议题 / Hooks 要不要上账号级同步组。
3. 会话内的「文件 / Git」面板要不要经中继读远端 fs。
4. 技能授权（R15e）在 web 上的形态 —— 它跟着执行目标档走，且要问那台机器有哪些技能包。

## 相关链接

- 前置：[2026-08-07 工作区多端同步](./2026-08-07-workspace-sync.md)（七类账号级对象、R19、决策 24）
- 前置：[2026-08-10 浏览器接入中继读写会话](./2026-08-10-web-session-access.md)（块 2/块 3 的拆分、R12 账号级可见性）
- 相邻：[2026-08-14 执行目标顺序](./2026-08-14-exec-target-order-ux.md)（2026-08-16 修订：server 直写 `sync_objects` 的第一个落地点）
