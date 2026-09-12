# 客户端升级路径：版本可见、agentred 自更新与一键升级

> Status: Approved
> Owner: agentred daemon 域 / 桌面端 remote-devices 域 / server device + relay 域
> Last updated: 2026-09-03

**Objective:** 让「这台 agentred 是哪一版、要不要升」在桌面端与控制台上看得见，并让升级动作在看见它的同一处可执行——机器上一条 `agentred update`，界面上一次点击。

**Hard invariant:** 三条不得回退。

1. **local-first 不变。** 升级能力不引入对账号的新依赖：纯 LAN 直连（未登录）的桌面端同样能看到远端版本并触发升级，判据与走账号那条路完全一致。
2. **R19 不变。** daemon 自报的只有版本号与短 commit，不携带路径、机器名或任何可反推宿主的信息。
3. **升级不打断正在跑的轮次。** 无论从命令行还是从界面发起，只要目标机器上有活跃轮次就一律拒绝；越过它必须是调用方显式声明的动作。

**本轮接受一次 flag day。**（用户决定）新增 RPC 方法必然改变方法集指纹，按 `internal/pkg/wireversion/methodset_test.go:62` 的守恒律，`MinSupported` 必须与新 `Protocol` 齐平，窗口因此收成一个点：升级后的桌面端 / server 会拒掉所有旧 agentred。依据是本项目**尚未发版**，没有在网的旧构建需要兼容；代价与它换来的东西（一次做完，而不是把远程升级推到下一轮）由用户权衡后选定。

## Problem

以下事实均于 2026-09-03 对工作区当前代码核实。

1. **agentred 没有任何更新入口。** `cmd/agentred/root.go:33` 注册的子命令是 run / status / pair / login / logout / llm / claudecode / service，没有 update。升级只能上机重跑安装脚本。
2. **安装脚本换完二进制就收尾，不管服务。** `scripts/install.sh` 在 `mv` 到位后只打印 `Installed agentred to ...` 与 `--version`，既不重启已注册的服务，也不提示 `agentred service restart`。用户照做完，跑的仍是旧进程——`mv` 是原子的，老进程持有老 inode 继续运行。
3. **wire 上没有 daemon 的构建版本。** `frontend/packages/agentre-wire/proto/agentre/wire/wire.proto` 里只有 `protocol_version` / `min_supported_protocol_version`（如 `AuthAccountResponse` 与 `HealthPingResponse:274`），没有任何字段说得出「这台机器跑的是哪一版 agentred」。桌面端的 `remote_device_svc.DeviceView`（`internal/service/remote_device_svc/svc.go:38`）也因此没有版本字段。
4. **控制台的版本列是装机时的化石。** `agentre-server` 的 `devices.version` 只在设备流换 token 时写入（`internal/service/device_svc/device.go:251`、`:258` 的 Upsert），此后每次刷新只走 `Touch`（`:387`）改 `last_seen_at`。设备卡上 `platform · version · lastActive` 这一行（`frontend/src/pages/Devices.tsx:404`）显示的是这台机器**第一次登录时**的版本。
5. **版本不兼容时用户看不见任何东西。** daemon 在 `internal/daemon/protobuf_registry.go:46` 用 `wireversion.Reject` 的人话拒掉握手；server 的 `internal/service/mirror_svc/machineconn.go:96` 把它包成 `relay account handshake: %w` 当普通失败退避重试。用户看到的是「设备在线、一条会话都没有」，唯一有用的那句话只在服务端日志里。桌面端虽已能把它认成 `ErrProtocolVersionMismatch`（`internal/service/remote_device_svc/dial.go:114`，错误码见 `add.go:192`），但界面上没有对应的出口。
6. **server 这一跳享受不到版本窗口。** `agentre-server/internal/pkg/wireversion` 只有 `Protocol` 常量，没有 `MinSupported`；`machineconn.go:93` 的握手也只发 `ProtocolVersion`。daemon 侧对缺该字段的对端按「floor = 它自己的 protocol」保守解读，等于 server 每次抬版本都在无声地要求全网齐平。
7. **未经 release 构建的二进制会自称 `1.0.0`。** `configs.Version` 的默认值是 `1.0.0`（cago `configs/config.go:17`），正式构建由 release workflow 用 tag 注入（`.github/workflows/release.yml:9`、`:105`）。好在 `internal/buildinfo.CommitID` 未注入时为空串，这是「非发布构建」的可靠判据。
8. **agentred 已经有容器形态。** `docs/specs/2026-09-02-agentred-container-dev-deploy.md` 把联调机改成容器运行（入口 `agentred run`、`restart: unless-stopped`），并明写「CLI 的二进制来自镜像」。

## Actors and user stories

1. 作为在桌面端管理远端机器的用户，我想在设备行上看到这台 agentred 是哪一版、要不要升，并在那里点一下就升，这样我不必为了升级去 SSH。
2. 作为控制台用户，我想在设备卡上得到同样的信息与同样的动作，这样两端对同一台机器的说法一致。
3. 作为在机器上操作的运维，我想要一条 `agentred update` 就完成下载、校验、替换与生效，这样我不必记住「重跑安装脚本之后还要重启服务」。
4. 作为遇到「设备在线却一条会话都没有」的用户，我想界面直接告诉我这是版本不兼容、以及怎么升，这样我不必去翻服务端日志。
5. 作为正在这台机器上跑长任务的用户，我不希望别人的一次升级把我的轮次掐掉。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 本轮新增自更新 RPC 方法，接受方法集指纹变更与随之而来的 flag day | 用户决定，依据是尚未发版、无在网旧构建。Rejected: 本轮只加字段（digest 不变、窗口可张开到 `[0.2.0, 0.2.1]`）、远程升级推到下一轮——零 flag day，但一件事分两轮做 |
| 2 | wire 版本抬到 `0.3.0`，三处版本号（`agentre-wire/package.json`、桌面 `wireversion`、server `wireversion`）与 `methodSetDigest` 同一轮改齐，`MinSupported` 与 `Protocol` 相等 | `methodset_test.go:20` 的指纹与 `:62` 的守恒律本来就要求方法集变更时重置窗口；不重置则新方法集落进旧窗口，旧构建能握上手、到第一次调用新方法才炸 |
| 3 | server 侧补上 `MinSupported` 常量并在 `auth.account` 握手里发出 | 本轮它与 `Protocol` 相等、不产生宽限，但从此 server↔daemon 这一跳具备窗口能力：下一次只加字段不改方法集时，server 可以让 floor 落后于 Protocol 而不打断全网。Rejected: 继续不发——把「能不能平滑升级」永久锁死在 daemon 的保守解读上 |
| 4 | daemon 在 `health.ping` 与 `auth.account` 的应答里自报**版本号**与**短 commit** 两个独立字段 | 版本号要能比较，`BuildIdentity()`（`internal/daemon/ipc.go:20`）是 `版本 (commit)` 的展示串，解析它等于把展示格式变成契约。Rejected: 只报合并串——消费端要反解；Rejected: 只报版本号——分不出发布构建与本地构建 |
| 5 | 「可升级」判定只在短 commit 非空（发布构建）时做；commit 为空的机器显示为开发构建，永不劝升 | 未注入版本的构建自称 `1.0.0`（问题 7），比任何 `0.x` 正式版都「新」，不加这道闸就会把本地构建的机器判成最新、把正式版判成过期 |
| 6 | `agentred update` 不探测宿主形态（容器 / 裸机），一律替换二进制。「有没有注册服务」不属于形态探测，它由既有的 service 管理层直接回答 | 用户决定。Rejected: 探测容器（`/.dockerenv`）后拒绝——避免下一条的静默回退，但多一处需要各形态验证的分支判断 |
| 6a | 容器形态的回退代价明说：升级写在容器可写层，`restart: unless-stopped` 重启同一容器时新二进制生效，但下一次重建容器（dev 链路每次推送都会做）回退到镜像里的旧版本 | 决策 6 的已知代价，写进文档与命令输出，而不是留给下一个人踩 |
| 7 | 生效方式统一为「换完之后让监管者把它拉起来」：装了服务就 `service restart`，没有服务的 daemon 自更新后主动退出，前台裸跑则由命令行明确告知需要手工重启 | `ServiceManager.Restart` 已存在（`cmd/agentred/service.go:81`）；容器与 systemd/launchd 都有监管者，退出即回来。Rejected: 只换文件不重启——用户在界面上点了升级却看不到版本变化 |
| 8 | 有活跃轮次时一律拒绝升级，并回报条数；越过它需要调用方显式声明（命令行 `--force`，RPC 请求里的显式标志 + 界面二次确认） | 用户决定。daemon 重启会掐掉它拉起的子进程。Rejected: 排队到空闲再生效——体验更好，但 daemon 多一份待重启的持久状态，且「什么时候生效」不可预测 |
| 9 | 下载源：默认 GitHub，失败自动回退到内置镜像（`internal/service/update_svc/mirror.go` 的 ghfast / gh-proxy），并尊重既有的 `AGENTRED_RELEASE_BASE_URL` 覆盖 | 与 `scripts/install.sh` 的既有变量名一致，内网与自建部署换源不必学第二套约定 |
| 10 | `agentred update` 支持 stable / beta / nightly 三通道，与桌面端对齐 | 用户决定。Rejected: 只支持 stable——面更小，但同一个账号下的桌面端与 daemon 会停在不同通道上 |
| 11 | 目标路径不可写时明确失败并要求以足够权限重跑，绝不改装到另一个目录 | 退回 `~/.local/bin` 会造出第二个 agentred，PATH 上先命中哪个取决于环境，症状是「升级了但版本没变」 |
| 12 | 控制台的 latest 由服务端定时拉取并缓存，浏览器读服务端自己的端点 | 用户决定。控制台常部署在内网，浏览器未必连得到 GitHub；出口收在一处，多副本共享同一份缓存，且可配置关闭或换源。Rejected: 浏览器直连 GitHub；Rejected: 由 daemon 自查后随 health.ping 上报——判定分裂成两套 |
| 13 | 「有新版本」只在设备面上出弱提示，不做横幅、不做全局汇总、不做「跳过此版本」 | 用户决定。强提示只留给「已经用不了」的协议不匹配 |
| 14 | server 在每次镜像握手成功后刷新 `devices.version`（值不同才写） | 修问题 4，且与既有的 `Touch` 同为幂等条件更新，多副本并发无害 |
| 15 | 远程升级的鉴权沿用连接自身（已鉴权的账号连接 / 已配对的 LAN 桌面端），不引入新的授权面 | 该连接上已经能跑任意会话与终端方法，升级不比它们更敏感 |
| 16 | 不新增任何设计 token：「可升级」复用既有的 `--status-waiting-*`，「已经用不了」复用 `--destructive-*` | 两组语义已经在用（等待 / 故障），升级提示不构成第三种语义。Rejected: 为升级新开一组 token——两端都要同步，且与既有状态色的关系需要重新论证 |
| 17 | 版本徽标挂在副行（`platform · version · 最后在线` 那一行），不进标题行 | 标题行已有状态点、种类 chip 与 StatusMark 三个元素，再加一枚会把设备名挤没；版本判断跟着版本走 |
| 18 | 一键升级与可复制命令**始终并列**在同一处展开区，不按状态二选一 | 协议不匹配那一态下一键升级必然够不着，命令是唯一出口；始终并列则只有一套布局，用户也能自己选 |
| 19 | 「已是最新」与「拿不到最新版信息」在卡片上同形，差别只在展开区（前者说「已是最新」，后者两句都不说） | 决策 12 的「不编」在界面上的直接后果：拿不到就是拿不到，不能借「没有徽标」冒充「已是最新」 |
| 20 | 动作菜单里的「升级 agentred」在已是最新时保留为禁用态并注明版本，不隐藏 | 入口时有时无会让人怀疑自己记错了位置 |
| 21 | 有活跃轮次时主动作降为次要样式、文案改为「仍要升级」，**不禁用**；拦截由二次确认承担 | 禁用会让一台整天有对话在跑的机器彻底没有出口，而决策 8 要的是「越过必须显式」，不是「不可越过」 |
| 22 | 界面与命令行对同一件事使用同一句话（如「这台机器上有 N 条对话在跑，升级会中断它们」） | 同一个拒绝在两处长得不一样时，用户会以为遇到了两个不同的问题 |

## 协议：版本窗口与自报版本

daemon 在两处应答里自报构建：`health.ping`（桌面端 watcher 每次心跳都会拿到）与 `auth.account`（server 每次建立镜像连接都会拿到）。两个字段——版本号与短 commit——语义相同、取值同源，消费端据此得到「这台机器现在跑的是哪一版」，而不是「它第一次登录时是哪一版」。

版本号可以是任何形态的字符串；消费端在无法解析或短 commit 为空时，一律按**版本未知 / 开发构建**处理：显示出来，但不参与「可升级」判定，也不出徽标。这条同时兜住了两种情形——旧 daemon 根本不报（本轮 flag day 之后这种机器已经握不上手，但 LAN 直连的历史配对仍可能遇到）与本地构建自称 `1.0.0`。

server 侧新增 `MinSupported` 并在握手里发出。本轮它与 `Protocol` 相等，可观察结果只有一条：daemon 收到的握手里这个字段不再是空串，因而不再需要按「对端预先窗口机制」保守推断。

## `agentred update`

命令的可观察契约：

- **`agentred update`**：解析目标通道的最新发布 → 下载对应平台资产 → 按发布附带的 SHA256 校验 → 原子替换当前可执行文件（先解析符号链接）→ 生效。
- **`agentred update --check`**：只报告「当前版本 / 最新版本 / 是否有更新」，不下载、不替换。它是脚本与 CI 可用的形态。
- **`--channel stable|beta|nightly`**：默认 stable。
- **`--force`**：越过活跃轮次这道闸。

失败路径各自有明确出口，且都不留下半个二进制：

- 目标机器上有活跃轮次 → 不下载、不替换，报出条数与「加 `--force` 越过」。
- 校验和不匹配、资产缺失、当前平台没有对应资产 → 报错退出，原二进制不动。
- 安装目录不可写 → 报错要求以足够权限重跑，并明确指出是哪个路径；不改装到别处。
- GitHub 不可达 → 自动尝试内置镜像；镜像也不可达时报出最后一次失败原因，并提示可用 `AGENTRED_RELEASE_BASE_URL` 指向可达的源。

生效：装了服务（`agentred service install` 注册过）时，替换后重启它，命令输出里说明服务已重启与新的版本号；没有注册服务时，命令输出明确告知「运行中的 agentred 需要重启才会生效」——命令行进程无法替另一个前台进程做这件事。

`scripts/install.sh` 同步补上收尾：检测到已注册的服务就重启它，检测不到就打印需要执行的重启命令。这让「重跑安装脚本」与「`agentred update`」两条路的终态一致。

## 远程一键升级

新增一个 RPC 方法，由 daemon 自己执行升级。请求携带通道与「越过活跃轮次」标志；应答只回**受理结果**，不回升级进度：

- 受理 → daemon 开始下载、校验、替换，随后让自己被监管者拉起（装了服务就重启服务，否则退出）。
- 拒绝 → 带上人话原因：活跃轮次 N 条 / 已有一次升级在进行中 / 安装路径不可写 / 已是最新 / 下载或校验失败。

同一台机器同时只允许一次升级在跑，第二次调用返回「进行中」而不是并行下载。

**升级过程的可观察性**由消费端从版本号推断，而不是由 daemon 推送进度：受理之后连接必然断开（daemon 重启），界面进入「升级中」；重连后读到的版本号发生变化即判成功，超时（5 分钟）仍未等到即判失败，并提示到机器上查看日志。没有监管者的形态（前台裸跑）下 daemon 退出后不会自己回来，界面因此按超时失败呈现——这是决策 6 与 7 的已知代价。

## 桌面端呈现

设备行显示远端 agentred 的版本；发布构建且旧于最新版时，行上出一枚弱徽标，动作菜单里多一项「升级 agentred」。触发后进入上一节描述的「升级中 → 成功 / 超时失败」流程；菜单里同时给出可复制的 `agentred update` 命令，作为一键升级不可用时的兜底。

**协议不匹配是强提示**：桌面端已经能把握手失败认成「对端协议版本对不上」，这时设备行上给出的不是一句泛泛的连接失败，而是「这台 agentred 太旧了」加升级出口。此时一键升级够不着（握手都没过），出口只能是可复制的命令卡——这也是它必须存在的理由。

不做横幅、不做全局汇总、不做「跳过此版本」。

**文案的分寸**：每个状态一行标题加一行事实。讲机制原理的话不进 UI（「握手都没过所以够不着它」属于日志），而影响用户当下决定的事实必须在——升级会不会中断正在跑的轮次、已产生的内容是否保留、还要等多久、为什么它没回来、下一步去哪。这条同时约束两端与命令行。

## 控制台呈现与 latest 来源

`devices.version` 改为每次镜像握手成功后按新值刷新，设备卡上的版本因此是实时的。

服务端定时拉取最新发布并缓存，浏览器通过服务端自己的只读端点获得「最新版本是多少」。这条链路必须满足：多副本下不重复拉取、缓存共享；拉取失败或被配置关闭时，端点如实回「不知道」，控制台**不出徽标也不编版本**——「拿不到」与「已是最新」在界面上必须分得开。下载源可配置，内网部署可以指向自己的镜像。

设备卡的呈现与桌面端同构：真版本 + 弱徽标 + 一键升级 + 命令卡兜底。协议不匹配时同样是强提示，这要求 server 把握手被拒这件事记成按 (账号, 机器) 的共享状态供设备卡读取，并把重试退避拉长——版本不匹配不是瞬时故障，按秒重试没有意义。

## 需要各自成立的界面状态

两端与命令行都要覆盖这些状态，且彼此说法一致：已是最新、可升级（机器空闲）、可升级（有对话在跑）、升级中、升级成功、升级超时失败、协议不匹配、开发构建、拿不到最新版信息。窄屏与键盘焦点同样适用。

状态划分、层级与逐句文案的取舍留有本地稿：`.dev-kit/artifacts/2026-09-03-client-upgrade-guidance/mockups/`（自包含 HTML，虚构数据，色值取自共享包 token）。稿子是证据不是契约，具结论已写进上面的决策表与本节。

## Out of scope

- **自动升级 / 定时自更新。** 本轮所有升级都由人显式发起。
- **桌面端自身更新流程的改动。** `internal/service/update_svc` 已经完整，本轮只复用它的发布解析与下载校验能力，不改它面向用户的行为。
- **升级失败的多版本回滚。** 原子替换 + 失败时原二进制不动即为全部保障；不保管历史版本、不做降级命令。
- **容器镜像的自动换 tag。** 容器形态的正式升级路径仍是换镜像，本轮只保证不把它伪装成已完成的升级。
- **`agentred service` 子命令在容器内的行为。** 沿用 `2026-09-02-agentred-container-dev-deploy.md` 的非目标。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `wireversion` 包（两仓）与方法集守卫 | 方法集变更后 digest、`Protocol`、`MinSupported` 与 pin 住的包版本四者一致；server 侧新增的 `MinSupported` 与 pin 逐字相等 | `internal/pkg/wireversion/methodset_test.go`、两仓各自的 `wireversion_test.go` |
| daemon 的 `health.ping` / `auth.account` 处理器 | 应答里带出版本号与短 commit，且取值来自构建注入而非硬编码 | `internal/daemon/protobuf_registry.go` 既有处理器测试 |
| 升级命令的纯判定层（是否有更新、通道解析、资产选择、活跃轮次闸门、路径可写判定） | 各失败路径给出各自的出口，且都不触发替换 | `internal/service/update_svc/update_test.go` 的比较与解析用例 |
| 远程升级 RPC 的受理判定 | 拒绝原因逐一可断言；同一机器的并发调用只有一次被受理 | `internal/daemon/protobuf_registry.go` 既有方法的表驱动用例 |
| server 的镜像握手后写回 | 版本变化时写、未变化时不写；握手被拒时记状态并拉长退避 | `internal/service/mirror_svc/resident_test.go` |
| server 的 latest 缓存与端点 | 拉取失败 / 被关闭时端点如实回「不知道」；多副本不重复拉取 | 既有 crontab 与 Redis 缓存用例 |
| 两端设备面的呈现 | 「需要各自成立的界面状态」一节列出的九个状态各自的渲染与出口，其中升级中→成功、升级中→超时失败是两条需要走完的路径；有对话在跑时主动作不禁用且必经二次确认 | `remote-devices/device-row.test.tsx`、`agentre-server` 的 `devices.test.tsx` |

无法自动化的部分：真实替换二进制并重启服务、容器形态下的可写层生效与重建回退。这两项由收尾时在联调机上的一次实机观察覆盖（升级前后 `agentred --version` 与设备面显示的版本），结论记入本轮的验证报告。

## Open questions

无。
