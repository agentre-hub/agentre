# 会话级思考力度：composer 选择器 + 三后端力度下发的兼容收敛

> Status: Approved
> Owner: 桌面端 chat 域 / agentruntime 域 / 跨宿主前端
> Last updated: 2026-09-01

**Objective:** 让用户在对话输入框里为**当前这条会话**单独选定思考力度（六档，空档 = 跟随后端配置），自下一轮生效，并让 claude / codex / pi / builtin 四个后端实际下发的档位与各自 CLI 真正支持的档位一致。

**Hard invariant:** 六条不得回退。

1. **档位词表只有一份。** `agent_backend_entity` 的六档（`""` / low / medium / high / xhigh / max）是全工作区唯一的思考力度词表；桌面、agentred、server、共享包一律不得各自定义第二份枚举。
2. **不新增第二条「有效力度」算法。** 「会话覆盖 > 后端配置」的合成只允许发生在一个边界函数里；`launchIdentity`、各 runtime 的 session 构造、「复制启动命令」三条下游继续只读 `Backend.ReasoningEffort` 一个字段，不得各自再判一次会话覆盖。
3. **启动身份规则不被绕开。** 力度是 spawn 时烤死的参数，改动必须经 `CLISessionPool.GetWithIdentity` 的既有「身份变了就重开」规则生效，不得新增任何旁路的进程重启或缓存失效机制。
4. **后端级配置的语义不变。** `agent_backends.reasoning_effort` 仍是该后端所有会话的默认值；本轮不改设置页那颗 Select 的写入语义，也不让 composer 的选择反写后端行。
5. **openclaw 不获得思考力度。** `agent_backend_entity/kinds.go:221` 那条「openclaw 的 `reasoning_effort` 必须为空」的校验保持原样，本轮只让 UI 据能力位不渲染。
6. **字段缺省 ≠ 用户选了默认档。** run 参数上的 `reasoning_effort` 为空时必须回落到 backend 负载里的值，不得当成「这条会话显式选了默认」——否则未设覆盖的会话会集体丢掉后端配置。（本轮实施中发现：方法集变更迫使协议窗口按既有守卫收成单点 0.2.0，跨代对端在握手期即被拒，因此这条回落的受众不是「旧版桌面端」，而是同代但把该字段留空的调用方：没有会话级覆盖的轮次，以及尚未接线该字段的浏览器派发。）

## Problem

以下事实均于 2026-09-01 对工作区当前代码与本机 CLI 核实。

1. **思考力度只能按后端配，改一次影响所有会话。** 落库字段是 `agent_backends.reasoning_effort`（`internal/model/entity/agent_backend_entity/agent_backend.go:77`），唯一入口是设置页的后端编辑器（`frontend/packages/agentre-ui/src/engine/agent-backends-fields.tsx:716` 的 `ReasoningEffortField`）。同一个后端上并行的多条会话无法各用各的力度：想让某条会话跑 xhigh，就得把该后端的所有会话一起改过去。composer 底栏当前只有 permission mode 与模型两颗 pill（`frontend/src/components/agentre/chat-panel.tsx:1026-1048`）。
2. **改了 codex 的力度不会生效，且没有任何提示。** `internal/pkg/agentruntime/runtimes/codex/runtime.go:86` 的 `launchIdentity` 只拼 model / modelKey / providerKey，**不含 `ReasoningEffort`**；而力度是 spawn 时经 `-c model_reasoning_effort=` 烤进 app-server 进程的（`internal/pkg/agentruntime/runtimes/codex/session.go:388`）。池里那条会话因此不会被 `GetWithIdentity` 驱逐（`internal/pkg/agentruntime/cache.go:123-142`），整轮继续跑在旧力度上。同一位置的 claudecode（`runtimes/claudecode/runtime.go:685`）与 piagent（`runtimes/piagent/runtime.go:114-120`）都把力度算进了身份 —— 只有 codex 漏了。
3. **codex 的 `max` 被无谓降到 `high`。** `internal/pkg/agentruntime/clienv.go:139` 与 `internal/pkg/agentruntime/runtimes/codex/session.go:452` 各有一份**内容完全相同**的 `max → high` 映射；UI 因此对 codex 隐藏 max 档（`agent-backends-fields.tsx:60` 的 `REASONING_EFFORTS_CODEX`）。本机实测 codex-cli 0.150.1 对 `-c model_reasoning_effort` **不做本地枚举校验、原样透传**（传入 `bogus` 时 CLI 照常打印 `reasoning effort: bogus` 并发出请求），上游明确回 `valid levels: low, medium, high, xhigh, max`。降档与隐藏的前提已不成立。
4. **pi 的 `max` 被无谓降到 `xhigh`。** `internal/pkg/agentruntime/launch_command_piagent.go:39` 与 `pkg/piagent/types.go:159` 同样各有一份**内容完全相同**的 `max → xhigh` 映射。本机 `pi --help` 明写 `--thinking <level>  Set thinking level: off, minimal, low, medium, high, xhigh, max`。
5. **claude 侧档位是对的，可作为基准。** 本机 claude 2.1.251 `--help` 写明 `--effort <level>  Effort level for the current session (low, medium, high, xhigh, max)`，与 `pkg/claudecode/args.go:25` 下发的五档完全一致。
6. **浏览器发起的轮次根本不带后端配置。** agentre-server 派发时送出的是空壳 `backend: { type: choice.backend_type }`（`agentre-server/frontend/src/lib/dispatch.ts:208`），agentred 侧按它反序列化出一个几乎全默认的 `AgentBackend`（`internal/daemon/handlers/runtime.go:323-326`），模型目标是靠 `RuntimeRunRequest` 上单列的 `llm_provider_key` / `llm_model_key` 两个字段过线并由 daemon 每轮 `resolveTarget` 解析的（`internal/daemon/handlers/runtime.go:397`）。力度若只塞在 backend 负载里，这条路径上恒为空。

## Actors and user stories

1. 作为**桌面用户**，我希望在输入框底栏直接把当前这条会话调到更高的思考力度，这样一道难题可以单独加力，而不必把整个后端连同其它会话一起改过去。
2. 作为**桌面用户**，我希望事后翻看转录时能看出某几轮是用什么力度跑的，这样「同一条会话前后表现不同」有据可查。
3. 作为**浏览器端用户**（agentre-server），我希望同一颗选择器在网页里也在、选完在桌面端打开这条会话看到的是同一档，这样两个宿主对同一条会话不说两句话。
4. 作为**codex 用户**，我希望改完思考力度它真的按新档位跑，而不是安静地继续用旧档位。
5. 作为**pi / codex 用户**，我希望选了 `max` 就是 `max`，不被悄悄降档。

## Design decisions

| # | 决策 | 依据与被否方案 |
|---|---|---|
| 1 | 作用域为**会话级持久化**：新增 `chat_sessions.reasoning_effort`，空串 = 跟随后端配置 | 用户决定。与同为会话级的 ModelTarget（`chat_sessions.provider_key/model_key`，`chat_entity/session.go:92-105`）完全同构，前端、IPC、peer、daemon 四条链路都有可直接照抄的先例。否决**仅本次发送的瞬态值**：刷新或重开会话即丢，与旁边 ProviderPill 的行为不一致；否决**下拉框直接改后端行**：那会让用户在对话框里的一次操作悄悄改掉其它所有会话 |
| 2 | 档位表**统一六档**，删除全部降档 | 用户决定 + 问题 3/4/5 的实测。四个后端都能吃下 low…max，降档只制造「存的是 max、跑的是 high」这种用户无法证伪的偏差。否决**保留降档只统一 UI**：迷惑点原样保留；否决**扩到八档补上 pi 的 `off`/`minimal`**：这两档在 claude 与 codex 上无对应值，会重新引入按后端过滤的选项表与跨后端切换时的降档规则 —— 正是本决策要消灭的东西 |
| 3 | 有效力度在**一个边界函数**上合成，结果写进本轮 backend 副本 | 硬不变量 2。`buildRunRequest`（`internal/service/chat_svc/chat.go:2397`）与「复制启动命令」（`chat.go:842`）是仅有的两个把 `Backend` 交给 agentruntime 的地方；在这里合成后，`launchIdentity`、四个 runtime 的 session 构造、`BuildLaunchCommand` 三条下游一字不改就同时拿到会话覆盖。否决**在各 runtime 里读会话行**：agentruntime 不依赖 chat 域，且会变成四份重复判断；否决**就地改 `be` 而非副本**：`be` 是本轮解析出来的实体，写进去会让同一实体在别的读路径上带上会话专属的值 |
| 4 | wire 上**单列 `reasoning_effort` 字段**，与 `llm_provider_key`/`llm_model_key` 同一形态 | 问题 6：浏览器发的是空壳 backend，塞进负载里那条路上恒为空。否决**只塞 backend 负载**：桌面端能通、浏览器端不通；否决**让 daemon 读自己的 `daemon_sessions` 行**：那两列在现行设计里是纯显示镜像（`internal/daemon/repository/session_repo/session.go:61` 明写「两格只有 SetModelTarget 一条路」），让执行路径依赖它等于给它加一层未经验证的权威性 |
| 5 | daemon 取值优先级：**run 参数非空 > backend 负载**，缺省不视为「用户选了默认」 | 硬不变量 6。把缺省解读为空档，会让所有没设会话级覆盖的轮次丢掉后端配置——那是绝大多数轮次。空档本身经桌面端合成后仍会以「后端配置值」的形态出现在 backend 负载里，两者不冲突。（受众范围见硬不变量 6 的括注：协议窗口收成单点后不含跨代对端） |
| 6 | 新增能力位 `CapReasoningEffort`，openclaw 为假，**整个控件不渲染** | 用户决定。与 permission mode pill 的既有做法一致（`chat-panel.tsx:1029` 的 `isModeSwitchable` 为假即不渲染）；composer 底栏有「恒为单行、不得横向溢出」的硬约束（`packages/agentre-ui/src/composer/chat-composer.tsx:129-140`），少一格是好事。否决**置灰禁用 + 悬停说明**：多占一格底栏，换来的只是告诉用户一个他改不了的事实 |
| 7 | 切换**落一条转录 notice** | 用户决定。力度是下一轮才生效的 spawn 参数，没有转录痕迹就无法回溯哪几轮用了什么档位。复用切换供应商那条既有 notice 通道（`internal/service/chat_svc/session_provider.go:253` 的 `appendProviderSwitchNotice` + `ChatBlock.NoticeKind`），不新开消息类型。否决**不落痕迹**：问题正是「事后看不出来」 |
| 8 | 控件视图与档位表出在共享包 `@agentre-hub/agentre-ui` | AGENTS.md 跨宿主前端所有权：桌面与 server 都要渲染这颗选择器。宿主只提供状态、传输与持久化。否决**两端各写一份**：与 ModelTargetPicker 曾经漂成两套失效判定是同一个坑 |
| 9 | 控件落在底栏**右侧**、紧邻提交键，且采用右侧既有的**读数形态**（无填充无描边、hover 才显边框，与两个计量器同款），只靠常驻 chevron 表明可展开 | 用户决定（见 `agentre.pen` 的「Section — 会话思考力度」）。底栏现有分区是「左＝怎么跑（权限、模型身份）／右＝这一轮花多少（配额、上下文）」，思考力度属于后者；右侧计量器是 `border-transparent` + `cursor-default` 的只读读数（`packages/agentre-ui/src/composer/context-meter.tsx:76-82`），一颗带描边填充的控件插进去会打断那片安静区。否决**右置但保留 pill 描边填充**：一眼可识，代价是上述观感断裂；否决**左置与另两颗同排**：左侧密度上升，且它在窄档本就该最先让位 |
| 10 | 五档强度用 `heat-0…heat-4` 色阶的一枚 8px 圆点；该色阶**本轮从 agentre-server 提升进共享包** | 用户决定。该色阶原本只定义在 `agentre-server/frontend/src/styles/globals.css:111-130`，其注释写明不放进包里的理由是「活跃热力图只有服务端控制台画——桌面端没有这一屏」——本轮正好推翻这条前提：桌面端的弹层成为第二个消费者，色阶因此升为共享 token（共享包 `styles/tokens.css` 的 `:root` / `.dark` 加五对值、`@theme inline`（`tokens.css:352`）加 `--color-heat-*` 别名），server 侧同步删掉自己那两处（同一文件 `:98` 明写「多写一条同名别名不会报错，只会让包里那条失效——所以别加回来」）。否决**自造 5 格强度条**：往设计系统里塞一个从未出现过的图形元素；否决**用 primary 透明度梯度**：那是自造一条没有名字的新色阶；否决**去掉圆点只靠排列**：决策 12 已去掉说明副行，再去掉色阶后五行只剩五个等宽单词 |
| 11 | 「默认」是弹层顶部的**独立区块**，不是第六档 | 它表达的是「不设定」，与五个档位不同类；单独成区并带 `→ 跟随后端配置 · <档位>` 解析副行，用户在选之前就看得见跟随会拿到什么（形态取自 `ProviderPillResolution`）。否决**并入档位列表当第一项**：把「不设定」伪装成一个档位 |
| 12 | 弹层每行**只有档位名**，不配说明副行 | `low < medium < high < xhigh < max` 是自明的序数，序列由色阶与排列承载。PermissionModePill 每行有说明，是因为 `plan`/`acceptEdits`/`bypassPermissions` 语义不自明——这里不成立。否决**每档一句说明**：正是仓库既有反馈里「拒绝总述性解释条」说的东西，且把弹层从 340px 撑到 436px |

## 会话级思考力度的选择与生效

**选择。** 会话详情打开时，若该会话所用后端声明了 `CapReasoningEffort`，composer 底栏在右侧、配额与上下文两个计量器之后、紧邻提交键处渲染一个「思考力度」控件，脸上写的是该会话当前的**有效档位**：会话行上的值非空时显示它，为空时显示后端配置的档位；后端配置也为空时显示「默认」。它的外观取自右侧既有的读数形态——无填充无描边、hover 才显边框——只以一枚常驻的展开箭头把「可点」与旁边两个只读计量器区分开。后端未声明该能力时整个控件不渲染，底栏其余部分不变。

**弹层。** 顶部是「默认」独立区块，带 `→ 跟随后端配置 · <档位>` 解析副行；分隔线之下是 low / medium / high / xhigh / max 五行，每行一枚取自 `heat-0…heat-4` 色阶的圆点加档位名，当前生效那一行加底色与「当前」徽章。不配说明副行。四个支持的后端呈现同一张表，不按后端裁剪。底部常驻一行说明生效时机，写库失败时在其下追加一条错误行。

**生效时机。** 选定后立即写库，但**当前正在跑的那一轮不受影响**，新档位自下一轮 spawn 生效；弹层底部常驻一行说明这件事。轮中切换不打断本轮、不驱逐任何进程池条目 —— 生效完全依赖既有的启动身份比对：下一轮 `launchIdentity` 因力度变化而与池里那条不符，池当场驱逐并重开。

**可达性。** 控件是一个常驻的按钮式触发器，带朗读得出当前档位的无障碍标签（念出的是当前有效档位而不是一串枚举值）。它取的是右侧计量器的**外观**而非其交互语义：计量器是 `cursor-default` 的只读读数，这个控件必须保持指针形状、可聚焦、可键盘展开，焦点环与弹层内的键盘定位由既有 Picker 基类提供，本轮不另做一套。所有静态文案经 i18n，中英两语齐备。

**no-op。** 选中的就是会话行上已有的同一个值时不写库、不落 notice、不发 IPC —— 否则每点一次已选中项就往转录里塞一条「已切换到 high」。注意比较的是**会话行上的值**而非有效档位：会话行为空而后端配置是 high 时，用户显式选 high 是一次真实的写入（把「跟随后端」钉成「就是 high」），不是 no-op。

**新建会话。** 还没有会话行时（草稿态）所选档位是纯瞬态的，随首条消息一并落库，与 ProviderPill 在同一场景下的做法一致。首条消息发出前切走或放弃草稿，该选择随草稿消失。

**转录提示。** 一次成功的切换在转录区追加一条 info 级 notice，说明切换到了哪一档（切回默认档时说明「已改回跟随后端配置」）。notice 写失败只记日志、不把切换报成失败 —— 库里的档位已经改了，报成失败会让用户重试并追加第二条痕迹。

**色阶的归属变更。** `heat-0…heat-4` 本轮由 agentre-server 独有升为共享 token：共享包的 `:root` / `.dark` 持有五对深浅值，其 `@theme inline` 持有 `--color-heat-*` 别名；agentre-server 删掉自己那两处同名定义，它的活跃热力图改由包提供同一份值渲染，观感不得变化。值本身一个字节都不改 —— 两个消费者从此共用同一份，正是升为共享 token 的意义。

图见 `agentre.pen` 的「Section — 会话思考力度」（底栏 Light / 弹层 Light / Dark 三板）。已知瑕疵：暗色下 `heat-0`（`#23262c`）在 popover 底（`#262931`）上几乎没有对比，最低档那枚点实际读成一个空环；序列关系仍成立（空环 → 实心），实现时若判定不可接受，只能为弹层这一处另做处理，**不得改动色阶本身的值** —— 它同时供 agentre-server 的活跃热力图使用。


## 有效力度的合成边界

有效力度 = 会话行上的值非空则用它，否则用后端配置的值。这一合成只发生在把 `Backend` 交给 agentruntime 的两个地方：组本轮 RunRequest 时，以及生成「复制启动命令」时。两处都把合成结果写在 backend 的**副本**上，本轮解析出的后端实体本身不被改写。

其下游一字不改：启动身份比对、四个 runtime 各自的 `--effort` / `-c model_reasoning_effort` / `--thinking` / cago `ThinkingConfig` 下发、以及复制出来的那行 shell 命令，全部继续只读 `Backend.ReasoningEffort` 一个字段，因而对「这个值是后端配的还是这条会话选的」完全无感。「复制启动命令」复制出来的命令行因此与这条会话下一轮实际起的进程带同一个力度参数。

## 三后端下发档位的收敛

**codex 的启动身份补齐力度。** 前提：一条 codex 会话的进程已在池中；动作：把该会话的有效力度改成另一档并发起下一轮；可观察结果：池中旧进程被驱逐、新进程按新档位起来。这是本轮唯一的行为**修复**（其余两项是取值范围放宽）。

**取消 codex 的 `max → high` 与 pi 的 `max → xhigh`。** 前提：会话有效力度为 max；动作：起一轮；可观察结果：codex 收到 `model_reasoning_effort="max"`、pi 收到 `--thinking max`。两组各两份重复实现收敛为每个后端一处：一处被另一处调用，或两处共用同一个函数 —— 不保留两份内容相同的映射。合法档位之外的输入（含大小写错、含空格）继续映射为「不下发」，走 CLI 自身默认。

**设置页不再对 codex 藏 max。** 后端编辑器的思考力度选项表对四个支持的后端呈现同一张六档表，`REASONING_EFFORTS_CODEX` 这条按后端裁剪的分支连同类型切换时的历史值降档一并去掉。已存的值不做迁移：现存 codex 后端行上的值本来就在六档之内。

## 跨宿主：agentred 与 agentre-server

**过线。** 桌面端发起的每一轮在 run 参数上单列携带本轮有效力度；agentred 取值时 run 参数非空优先，为空则回落到 backend 负载里的值。会话列表回传的会话摘要携带该会话在 agentred 侧记录的力度，供浏览器端在列表与重开时显示。

**agentred 侧的会话行。** agentred 为自己的会话镜像新增一列并提供一个「设置这条会话的思考力度」的 RPC，语义与既有的设置模型目标那条完全对齐：写不存在的会话报错而非折成成功（改一条不存在的会话的力度没有任何东西可以幂等，折成成功只会让调用方以为下一轮会用新档位）；空串是**要写下去的值**（改回跟随后端配置），不是「不改」。这一列与模型目标那两列一样只供显示，执行路径不读它。

**agentre-server 宿主。** 网页端 composer 渲染同一个共享控件，选择经中继直接打到承载这条会话的机器，并按既有做法补写发起端那一台，好让用户在哪一台打开都看到自己刚选的档位；够不着其中一台时不回滚已经生效的那一次，在旁边如实说明只写成一台。新建对话派发成功后按既有的「钉住模型目标」同一位置补写一次力度 —— 只过线不钉住的话，用户选的档位第一轮生效、随后打开详情页却读回「跟随后端配置」，是一句用户无法证伪的假话。

**交付顺序。** 共享包的控件与档位表、以及 wire 契约都由 `agentre` 仓库拥有：先在 `agentre` 落地并让桌面宿主用上、验证、提交、推送；`agentre-server` 再钉住推送后的不可变版本、接上自己的宿主适配并删除任何临时实现。两个仓库各自提交，绝不在共享版本可用之前先动 server 侧。

## 失败与恢复

- **写库失败**：控件回滚到上一档，弹层底部追加一条错误行如实说明原因；会话行保持原值。
- **档位非法**：服务端按既有的思考力度校验拒绝并原样报错（复用已有错误码，不新增），会话保持原档位。
- **会话不存在**：报会话不存在，不折成成功。
- **notice 写失败**：只记日志，切换本身仍报成功。
- **跨机双写只成一台**：不回滚，如实说明。
- **旧对端**：不带新 wire 字段的桌面端向新 agentred 发起的轮次按 backend 负载里的力度跑，行为与本轮之前完全一致。

## Out of scope

- 让 openclaw 支持思考力度（entity 层的禁止校验保持原样）。
- pi 独有的 `off` / `minimal` 两档。
- 设置页那颗后端级 Select 的写入语义与位置（本轮只改它的选项表）。
- 按模型能力动态裁剪档位（例如某模型不支持 xhigh 时灰掉该项）。
- 把会话级力度纳入账号同步或数据导出包 —— `chat_sessions` 现不参与二者，本列不改变这一点。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 有效力度合成函数（纯函数） | 会话值优先、空则回落后端值、两者皆空则为空 | 无 |
| `codex.launchIdentity` | 仅力度不同的两次请求算出不同身份（先跑红，证明当前会复用旧进程） | `runtimes/piagent` 与 `runtimes/claudecode` 的同名函数已有身份测试 |
| codex / pi 的档位映射函数 | `max` 原样下发；非法值仍映射为不下发 | `internal/pkg/agentruntime/clienv_test.go` |
| chat_svc 设置会话力度 | 写库、落 notice、no-op 不写不落、非法值拒绝、会话不存在报错、notice 失败仍成功 | `SetChatSessionModelTarget` 的既有服务测试（mockgen 注入 repo，不连库） |
| chat_repo 单列更新（sqlmock） | 只写这一列的原子语句 | `chat_repo` 的 `UpdateModelTarget` sqlmock 测试 |
| `buildRunRequest` | 本轮 backend 副本带上合成后的力度，且原实体未被改写 | `chat_svc` 既有 RunRequest 组装测试 |
| agentred handler + session_repo（sqlmock） | 空串是要写下去的值；会话不存在报错；run 参数优先于 backend 负载、缺省回落 | `internal/daemon/handlers/session_model_target.go` 的既有测试 |
| 共享包控件（vitest） | 六档呈现、脸上显示有效档位、当前项高亮、选同一值不回调、键盘可展开可选中 | `provider-pill-trigger.test.tsx` / `picker.test.tsx` |
| 桌面 composer（RTL） | 不支持的后端整个控件不渲染；控件挂在 trailing 侧且排在提交键之前；切换发出 IPC；IPC 失败回滚并显示原因 | `chat-panel.test.tsx` / `model-pill.test.tsx` |
| i18n 静态键与两语覆盖 | 新增文案两语齐备 | `frontend/src/__tests__/i18n.test.ts` |
| 共享 token（vitest） | `heat-0…heat-4` 五对深浅值与 `--color-heat-*` 别名在共享包 tokens.css 里齐备 | `frontend/src/__tests__/design-tokens.test.ts` 的同款读文件断言 |
| agentre-server（vitest） | 双写成功/单写降级的显示；派发后补写一次力度；删掉本地 heat 定义后活跃热力图仍取到五级色值 | `dispatch.test.ts` / `session-detail.test.tsx` / `components/stats/Heatmap.tsx` 的既有测试 |

无法自动化、交由收尾人工核对的部分：三个真 CLI 是否真的接受下发的 `max`（本机已实测一次，收尾时对 codex 与 pi 各再跑一轮真会话确认无报错），以及底栏右侧加入这个控件后在窄档下不横向溢出的实际观感（计量器仍是唯一让位者，控件与提交键保持不收缩），和暗色下最低档那枚 `heat-0` 圆点的实际可辨度。

## Open questions

无。
