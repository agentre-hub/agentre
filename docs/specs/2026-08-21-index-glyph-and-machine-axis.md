# 会话索引：呈现件归包、身份字形归一，桌面端补上机器轴

<!-- File: docs/specs/2026-08-21-index-glyph-and-machine-axis.md -->

> Status: **Approved**
> Owner: 会话索引 / 共享前端层（跨 `agentre` 与 `agentre-server`）
> Last updated: 2026-08-21（决策 5 收窄：只有颜色缺失退 agent-1）

**Objective:** 桌面端会话索引剩下的六件呈现件改用共享包 `@agentre-ai/agentre-ui`；
**身份字形以桌面端的 `AgentAvatar` 为准**收进包，成为两端唯一那一枚（`agentre-server`
的 `AgentGlyph` 随之删除）；桌面端新增第四根轴「按机器」，复用会话已经记着的
`chat_entity.Session.ExecDeviceID`。

**Hard invariant:** 桌面端会话索引在**既有三根轴**上对用户可见的行为逐条不变
（既有测试即守卫）—— 切包不是重画，是把已经搬进包的那一份接回来；
`AgentAvatar` 今天的 26 个调用点**入参一个不改**；
机器轴不改任何既有轴的查询、分组与渲染；
`ListIndexSessions` 认不出的 scope 仍旧当场拒绝，**不静默降级**（`chat.go:477-488`）。

## Problem

1. **同一条设计三份实现，且已经漂开。**
   桌面端已经在用包里的 `SessionGroup` / `SessionRow` / `SessionRowModel`
   （`frontend/src/components/agentre/agent-list.tsx:5-7`、
   `session-index/index-group-row.tsx:8`）与 `buildAxisGroups`
   （`session-index/index-projection.ts:22-27`），**唯独六件呈现件还是本地副本**：
   `axis-picker.tsx` / `row-leading-slot.tsx` / `row-secondary-line.tsx` /
   `project-glyph.tsx` / `free-group-header.tsx` / `own-sessions-header.tsx`。
   包里那六件本来就是照桌面端画的（注释里带的是
   [2026-08-16 统一会话索引](./2026-08-16-unified-chat-index.md) 的决策 3/4/5），
   于是同一枚记号在 `agentre` 仓里同时存在两份，且已经开始漂：
   本地 `AxisPicker` 没有 machine 档、没有 `axes` prop（清单写死在文件里），
   本地 `RowLeadingSlot` 收 `agentName` / `agentColor` 两个平铺参数而包里收 `agent` 对象。

2. **「身份方块」这一枚记号有三份实现。**
   桌面端 `primitives.tsx:57` 的 `AgentAvatar`（三档：上传头像 ▸ icon-registry 图标 ▸
   首字母，`agentColorClassNames` 上色）、包里私有的
   `session-index/glyph.tsx` 的 `Glyph`（一档：首字，`tokenToCssColor` 上色）、
   以及 `agentre-server` 的 `components/session/newconv/AgentGlyph.tsx`
   —— 第三份的注释里自己就写着「与会话索引里的 `Glyph` 是同一种字形」，
   4 个调用点（`DraftSession.tsx:178`、`AgentPickList.tsx:131`、
   `ProjectAgentPane.tsx:109/132/213`）。三份的兜底规则各不相同。

3. **桌面端没有机器轴，尽管会话早就记着自己跑在哪台机器上。**
   `chat_entity.Session.ExecDeviceID`（`session.go:86-88`）就是这一维，
   `0 = 本机`、`>0 = 配对 daemon`；`RanOnDaemon()` 也建在它上面。
   但 `lib/session-axis.ts:22` 用 `Extract<SharedIndexAxis, "project"|"agent"|"time">`
   把可选清单卡在三档，`index-projection.ts` 明写 `machines: []`。
   配对 daemon 是常态（`RemoteDeviceList` / `CatchUpRemoteDevice`），
   「哪些会话跑在这台上」今天只能靠逐条点开看。
   [2026-08-18 组织面收敛](./2026-08-18-org-index-convergence.md) 的决策 17
   把它明确列为 out of scope（那一轮的验收标准是「行为不变」），本轮承接它。

## Actors and user stories

1. 作为**桌面端用户**，我想按机器分组看会话，好知道哪些活跑在本机、哪些跑在某台配对的
   daemon 上，以及那台机器现在在不在线。
2. 作为**两端的维护者**，我想改一枚字形只改一处，好让同一个 Agent 在桌面端索引、
   桌面端设置页与 server 的「新对话」里长成同一个样子。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 六件呈现件**一次全切**，本地副本删除 | 它们互相引用（`RowLeadingSlot` / `RowSecondaryLine` 都用 `ProjectGlyph`），逐件迁移会出现「一半用包一半用本地」的中间态，两份字形同屏。Rejected: 按件分批 —— 中间态本身就是要消灭的那个问题 |
| 2 | **身份字形以桌面端 `AgentAvatar` 为准收进包**，三档齐全（上传头像 ▸ 图标 ▸ 首字母） | 用户裁决：以桌面端 UI/UX 为准。桌面端是能力更全的那一端，包里的 `Glyph` 只是它的一个子集。Rejected: 只把包内 `Glyph` 公开出来 —— 那样桌面端索引之外的 25 个 `AgentAvatar` 调用点仍是第二份实现，「同一个记号只有一份」只兑现一半 |
| 3 | 图标解析是**宿主端口**：包收 `icon?: ReactNode`，不收 icon key | icon-registry（`icon-registry.ts`，383 行、一张 lucide 图标表 + 分类 + 搜索）是宿主的产品决定，server 根本没有它。Rejected: 把注册表搬进包 —— 会把桌面端的图标集强加给所有宿主，且 server 的打包体积白付 |
| 4 | 桌面端 `primitives.tsx` 的 `AgentAvatar` 退化成**转发器**，在转发时把 icon key 解成节点 | 26 个调用点入参一个不改，与包里既有的转发先例同形（`components/ui/button.tsx`、`input.tsx`、`spinner.tsx`）。Rejected: 全量改调用点 —— 与本轮无关的 diff，违反「不碰无关文件」 |
| 5 | **字形兜底按桌面端**：颜色**缺失**（空 / 未给）→ `agent-1` 底色；名字为空 → `?`。`neutral` 与任何解析不出的 token → 中性面 | 用户裁决「以桌面端为准」，而桌面端的 `agent-1` 只作用于空值（`primitives.tsx:59` 的默认参数）。`neutral` 是两端调色板里**用户能选的正当灰**（`project_entity.allowedColors` 十七个，server 侧同源于 `ProjectColorPicker.tsx:4`），把它一并退成 `agent-1` 会让用户选的灰渲染成蓝。Rejected: 「缺失或非法一律退 `agent-1`」—— 见上；Rejected: 「一律退中性面」（包里现行）—— 桌面端没颜色的 Agent 会从蓝变灰，与「切包＝行为不变」冲突 |
| 6 | 首字母算法按桌面端 `getInitials`：拉丁多词名取前两词首字母并大写，其余取首字 | 同上。**这会让 server 出现可见变化**：「新对话」里的 `Code Reviewer` 从 `C` 变成 `CR`。尺寸与字号不变（server 的 `text-[11px]` 与桌面端 `text-2xs` 同为 0.6875rem，`tokens.css:365`） |
| 7 | server 的 `AgentGlyph` **删除**，四处调用改用共享件 | 它是重复实现而不是宿主适配，留一个转发器只是把重复藏起来。Rejected: 保留为转发器 |
| 8 | 机器这一维复用 `Session.ExecDeviceID`（`int64`，0 = 本机），**不**用 `ChatSessionLite.deviceID` | `ExecDeviceID` 是会话表上的一列，`0 = 本机` 的约定与包契约 `MachineInfo.deviceId: number` 天然对齐，无需换算、也没有「认不出机器」的兜底组。`ChatSessionLite.deviceID` 是 `string` 且**索引 RPC 根本没填**（`chat.go:574-589` 的 `sessionLiteFromEntity` 不写 device 三列），它是从 backend 推出来的另一件事。Rejected: 放宽包契约成 `number \| string` —— 为了迁就一个不该用的字段去动两端共用的契约 |
| 9 | 机器轴**每台机器一条分页查询**（`Scope=machine` + `DeviceID`），与项目轴同形 | 桌面端每轴一条分页查询是既有结构（`use-index-groups.ts:44-51`），「查看全部 N」的 N 只有取数方数得出来。Rejected: 客户端把 `recent` 那一页分桶 —— 每组总数不成立，「查看全部 N」会显示成「这一页里恰好有几条」 |
| 10 | 机器名单 = **本机恒在** + `RemoteDeviceList()` 的配对 daemon；空组照摆 | 「这台机器上一条会话都没有」是有信息量的答案，不是该被藏起来的空。组骨架归宿主是既有结构（`index-projection.ts` 只让投影分配组内的行）。Rejected: 只摆有会话的机器 —— 那样刚配好的一台 daemon 在索引里看不见，用户无从确认配对生效了 |
| 11 | 其它三根轴上**不补**机器那一维（第二行的 `machine` 传空） | 要在项目 / Agent / 时间轴上说机器，得给每条会话解析出它的机器，而那三条查询不按机器取数。机器这一维在机器轴上由组头说，已经说全。Rejected: 加宽索引 RPC 的载荷 —— 为一维展示让三条既有查询都变重 |

## 呈现件归包

桌面端六件呈现件的本地副本删除，改从 `@agentre-ai/agentre-ui` 取。宿主自带的东西经
包已经留好的插槽递进去：`ProjectGlyph.glyph` 收 icon-registry 解出来的图标，
`RowLeadingSlot.agentGlyph` / `projectGlyph` 与 `RowSecondaryLine.agentGlyph` /
`projectGlyph` 同理。`AxisPicker` 从宿主收 `axes`，桌面端本轮传四档。

前置条件是桌面端今天在渲染索引的任一轴；动作是切包后重新渲染；可观察结果是
**逐条与今天相同** —— 行首槽位仍是 14px、切轴时列表不跳动、「按时间」档第二行仍是
`〔头像〕agent · 〔字形〕项目`、自由会话仍如实写「随手对话」并把字形置灰。

一处例外要如实记下：包里的 `RowSecondaryLine` 在**每一根轴**上都会渲染
（非时间轴渲染「另外那一维」），而桌面端今天只在时间轴上调用它。桌面端切包后在其它轴上
传空的机器维，于是 `parts` 为空、返回 `null`，可见结果与今天一致；这一处在机器轴进来后
才会显形（决策 11 说了它在其它轴上仍不显形）。

## 身份字形归一

包收下桌面端 `AgentAvatar` 的三档语义：给了上传头像就整块换成图片（无底色、裁切）；
给了图标节点就在色块上画图标；都没有就画首字母。上色改走 `tokenToCssColor` 的内联
css 变量而不是 `bg-agent-*` 类名——值相同（`--agent-foreground: #ffffff`，
`tokens.css:137`），但类名要靠宿主的 Tailwind 扫到包源码才生成得出来，消费方少配一条
content 路径字形就会静默变透明。

尺寸沿用桌面端的 `sm` / `md` / `lg`（`size-6` / `size-8` / `size-10`），并**补上 `xs`**
（`size-3.5 rounded-sm text-[8px]`）：那是索引行里那一枚的实际尺寸，今天两端都靠
`className` 覆盖出来，给它一个名字不是新增能力，是把已有的一档说出口。

无障碍口径按桌面端：`role="img"` + `aria-label={name}`；名字为空时可及名为空
（一个未命名的图形），不会被念成某个具体的 Agent。

`agentre-server` 的 `AgentGlyph` 删除，四处调用改用共享件，`size="md"` 对应 `sm`、
`size="sm"` 对应 `xs`。可观察变化只有两处，都源自决策 5/6：拉丁多词名的首字母从一个字
变两个字；颜色**缺失**时从中性面变成 `agent-1` 底色（后者同样作用于 `ProjectGlyph`，
因此一次都没设过颜色的项目在索引组头上会从灰方块变成蓝方块）。**选了 `neutral` 的
项目与 Agent 仍旧是灰的** —— 那是调色板里的一个正当选项，不是「没有颜色」。

## 桌面端机器轴

**轴清单与持久化。** `lib/session-axis.ts` 的 `IndexAxis` 放开到四档，
`lib/sidebar-axis-state.ts` 的 `AXES` 跟着放开——两处各写一遍就会漂：多出来的那一档
选得着却在下次启动时被当非法值退回默认轴。默认轴仍是项目（不变）。

**取数。** `ListIndexSessions` 认第四个 scope `machine`，随 `DeviceID` 一起来；
`DeviceID < 0` 当参数错误拒绝（`0` 是本机，是合法值，这一点与项目 scope 的
`ProjectID <= 0` 判据不同，因为那里 0 有专门的 `free` scope）。
repository 侧按 `exec_device_id` 分页与计数，形状与 `ListByProjectPaged` /
`CountByProject` 对称。前端 `scopesForAxis(axis="machine")` 发出「本机 + 每台配对
daemon」各一条 scope，按需拉取、已有页缓存的不重拉，与项目轴同一套。

**分组与补齐。** `index-projection.ts` 这一层继续做唯一的翻译：机器轴的组键是
`device-<ExecDeviceID>`，行上的 `deviceId` 从组骨架来（取数时就知道，与 agent /
project 两维同理），`machines` 名单从「本机 + `RemoteDeviceList()`」拼出来喂给
`buildAxisGroups`。投影按「在线优先、再按名字」排组，本机恒为在线因而恒排最前。

**组头。** 机器组头沿用桌面端组头的形（同高、同折叠记号、同 attention 气泡位），
把项目字形那一格换成机器的在线状态点，组头文案是机器名，本机那一组的文案走 i18n。
机器离线时组头置灰并如实说明离线，但**组仍可展开、行仍可点开**——本体在本地库里，
离线只影响能不能在那台机器上继续跑，不影响读。这一条与包里 `RowSecondaryLine`
对离线的处理同源。

**空态。** 某台机器一条会话都没有时组头照摆、组内给一句空态；一台机器都没有配对时
机器轴仍成立（只有本机一组）。

## Out of scope

- **`FreeGroupHeader` / `OwnSessionsHeader` 两件的接入。** 两端谁都没接，卡在共用投影
  `buildAxisGroups` 不摆「父项目自己的会话」子分组上。改投影会同时改变 server 的分组
  结果，两端都要回归——独立一轮。
- **server 侧机器轴的既有增强**（列出机器上有、账号里还没有的对话并「保存」、
  `needsUpgrade` 角标、连接重试）。那些建在 server 的镜像语义上，桌面端所有会话都在
  本地库里，没有这一层。
- **在项目 / Agent / 时间轴上显示机器那一维**（决策 11）。
- **把 icon-registry 搬进包**（决策 3）。
- **`ChatSessionLite` 载荷加宽**。本轮一个字段都不加。

## Testing decisions

| 缝 | 验证什么 | 既有先例 |
|---|---|---|
| 包 `AgentAvatar` | 三档各自渲染什么（头像 ▸ 图标节点 ▸ 首字母）；首字母算法在多词拉丁名 / 中文名 / 空名下的结果；颜色缺失时退回 `agent-1`，而 `neutral` 与解析不出的 token 退回中性面；四档尺寸 | `packages/agentre-ui/src/session-index/project-glyph.test.tsx` |
| 包 `AxisPicker` | 只摆宿主传进来的那几档；四档各自的图标与选中态 | `packages/agentre-ui/src/session-index/axis-picker.test.tsx` |
| 桌面端切包后的索引 | **行为不变**：三根既有轴的行首槽、第二行、折叠、气泡、「查看全部 N」与今天逐条一致 | `components/agentre/__tests__/session-index-chrome.test.tsx`、`index-group-row.test.tsx`、`use-group-rows.test.tsx`（**原样跑通，不改断言**） |
| 桌面端 `AgentAvatar` 转发器 | icon key 在转发时解成节点；26 个调用点的入参形状不变 | 新增（今天没有转发器） |
| `scopesForAxis` | 机器轴发出「本机 + 每台配对 daemon」各一条 scope，且不多发 | `components/agentre/__tests__/use-index-groups.test.tsx` |
| `index-projection` | `ExecDeviceID` 落到 `IndexRow.deviceId`；`0` 归到本机那一组而不是兜底组；空机器组照摆 | `session-index/index-projection.test.ts` |
| `sidebar-axis-state` | `machine` 是合法持久值；认不出的值仍退回项目轴 | `lib/sidebar-axis-state.test.ts` |
| `chat_svc.ListIndexSessions` | `scope=machine` 的参数校验（`DeviceID < 0` 拒绝、`0` 放行）、分页 clamp、`hasMore` 判据；认不出的 scope 仍旧拒绝 | `internal/service/chat_svc/chat_test.go` 里 project scope 的用例（sqlmock） |
| `chat_repo.Session` 按机器分页 | `ListByDevicePaged` / `CountByDevice` 忠实按参数查，含 `exec_device_id = 0` | 同上（sqlmock） |
| server `shared-ui-package.test.tsx` | `AgentAvatar` 进公开面清单；`AgentGlyph` 删除后仓内无引用 | `frontend/src/__tests__/shared-ui-package.test.tsx:200-218` |

自动化盖不到的两处，留给收尾的源码复核与手动观察：**机器离线时组头的灰度与「仍可展开」
的手感**，以及**共享包发布环**（依赖是 GitHub tarball，包改完必须先 push 再在
`agentre-server/frontend/package.json` 里 pin 新 SHA，两端才真的在用同一份）。

## Open questions

（空）
