# ctl 控制通道的技能交付：Claude Code 插件 + 通用 Agent Skill 目录

> Status: Approved
> Owner: 桌面端 skill 域 / ctl 域
> Last updated: 2026-09-01

**Objective:** 把 agentre 已有的 ctl 控制通道（`ctl_svc` + `agrctl ctl`）以 Agent Skill 的形式交付到用户机器上，使运行中的 agent 知道它存在、会正确调用它；其中 Claude Code 那一份以插件形态落地，从而出现在「组织架构 → 管理技能」里，可按执行档逐档授权。

**Hard invariant:** 五条不得回退。

1. **不改 ctl 的鉴权语义。** endpoint 与 bearer token 仍只从 `<AppDataDir>/ctl-endpoint.json` 握手文件（或 `--endpoint/--token`、`AGENTRE_CTL_*`）解析。**token 绝不写入任何 SKILL.md 或插件文件**。
2. **安装不 exec 任何 CLI。** 注册信息由 agentre 直接写 JSON 文件完成，不调用 `claude plugin install` / `marketplace add`。安装发生在 app 启动路径上，exec CLI 会引入 PATH 解析、子进程超时、交互式提示三类失败面。
3. **全局默认不启用。** 写入用户 `~/.claude/settings.json` 的 `enabledPlugins["agrctl@agentre"]` 值为 `false`。是否生效由每个执行档在组织架构里的授权决定。
4. **合并写，不覆盖。** `installed_plugins.json` / `known_marketplaces.json` / `settings.json` 三份用户全局配置一律读→改本 key→写回；解析失败时只重建本插件相关的键，不清空用户其他插件、marketplace 与设置。
5. **发现侧与授权链路一字不改。** `agentskill` 注册表、`skill_svc` 目录合并、`buildSkillsSettings` 渲染 `--settings` 的既有行为本轮不动；本轮只往它们的输入端放进一个新插件。

## Problem

以下事实均于 2026-09-01 对工作区当前代码与本机环境核实。

1. **ctl 通道对 agent 完全不可见。** 内置工具注册表 `internal/pkg/agenttool/agenttool.go:24-30` 只有 `org` / `subagent` / `hook` 三项；全仓库与用户 skill 目录搜 `agrctl` / `ctl send` / `AGENTRE_CTL` 命中 0 个 skill。结果是 agent 只能经 `subagent` 的 `agent_call` 派活，`ctl send` 提供的跨项目、`--isolated`、`--wait` 三种能力没有任何入口暴露给它。
2. **想在「管理技能」里管，就必须是插件而非裸 skill。** `skill_svc` 的目录只收 `Discoverer.Discover()` 的结果（`internal/service/skill_svc/skill.go:149` 的 `catalogOf`），而 `~/.claude/skills/*` 这类独立 skill 走 `DiscoverCommands`，`internal/pkg/agentskill/agentskill.go:80-83` 明写它们 *must not appear in the organization picker*。
3. **`agrctl` 不在 CLI 子进程的 PATH 上。** 它被装在 `<AppDataDir>/bin/agrctl`（`internal/pkg/agrctlinstall/install.go:31`、`internal/bootstrap/cago.go:254`），而 `internal/pkg/clienv/clienv.go:139-146` 组装的搜索路径由「binary 所在目录 + 继承 PATH + 登录 shell PATH + 常见目录 + 用户工具目录」构成，不含 AppDataDir。skill 里写裸 `agrctl` 必然 command not found。
4. **两种落地形态在本机都已被验证可行。** `claude plugin list --json` 同时报出目录型 marketplace 装出来的 `opsctl@opskat`（`installPath` 指向 `~/.claude/plugins/marketplaces/opskat/opsctl`）与 skills-dir 自动加载的 `dev-kit@skills-dir`；`~/.claude/plugins/known_marketplaces.json` 里 `opskat` 条目为 `source: directory`。
5. **opskat 已有完整先例可照抄。** `~/Code/opskat/opskat/internal/app/system/settings.go:1136-1408` 里有：通用目录 `~/.agents/skills/<name>`（注释列明被 Pi / Codex / OpenCode / Cursor / Copilot / Windsurf / Gemini CLI / Cline / Warp / Rovo Dev / Amp / DeepSeek Harness 读取）、Claude Code 单独的插件+marketplace 安装、三份注册文件的手写合并、`pathTraversesSymlink` 跳过开发软链、`skillMDWithDataDir` 在安装时把数据目录注入 SKILL.md、以及对称的卸载与旧副本迁移。

## Actors and user stories

1. 作为**桌面用户**，我希望装完 agentre 就能在「组织架构 → 管理技能」里看到 ctl 技能包，并只给需要它的那个 agent 打开，这样调度类 agent 能派活，而普通编码 agent 的上下文不被它污染。
2. 作为**被授权的 agent**，我希望知道 ctl 存在、知道二进制的确切路径与命令契约，这样我能列出可用 agent / 项目并把子任务派出去，而不是猜一个不存在的命令。
3. 作为**同时用 codex / pi / 其他 CLI 的用户**，我希望这份技能在那些宿主里也可用，不必为每个 CLI 手抄一遍。
4. 作为**不想要它的用户**，我希望能在设置页里关掉并把已铺的文件清理干净，且下次启动不会被重新铺回来。

## Design decisions

| # | 决策 | 依据与被否方案 |
|---|---|---|
| 1 | Claude Code 侧用**目录型 marketplace**（`agrctl@agentre`），插件根落在 `~/.claude/plugins/marketplaces/agentre/agrctl/` | 用户决定。与本机已验证的 `opsctl@opskat` 同形，`claudeskill/pluginroot.go` 的 directory-marketplace 回落路径本就为这种形态存在。否决 **skills-dir 自动加载**（`<name>@skills-dir`）：虽然零注册，但插件 ID 与安装位置都寄生在 `~/.claude/skills` 下，语义上和用户手装的独立 skill 混在一起，卸载与版本升级都没有干净的边界 |
| 2 | 安装发生在 **app 启动**，幂等，带版本标记 | 用户决定。与 `agrctlinstall.EnsureInstalled` 同一套「拷贝 + 版本标记 + 版本变则重装」模型，用户零操作即可在管理技能里看到它。否决**首次进入管理技能时惰性安装**：目录要在用户打开页面之前就已存在，否则第一次打开必然是空的 |
| 3 | 全局 `enabledPlugins` 写 **false** | 用户决定。管理技能的三态模型（`agentskill/catalog.go:52-56`：未显式授权则 `EffectiveEnabled = Installed && GloballyEnabled`）下，全局关 = 默认不注入，只有逐档「强制开」才生效。否决 opskat 现行的写 `true`：那会让用户在终端里手开的每个 claude 会话都载入这个 skill，且组织架构里只剩「强制关」一种有意义的操作 |
| 4 | 同一份 SKILL.md 同时铺到通用目录 `~/.agents/skills/agrctl/` | 用户决定，且已知代价。该目录被 12 个宿主读取（依据见 Problem 5），一次安装全生效。**已知边界：这一份不可逐档开关** —— codex 的 `Discover()` 走 `codex plugin list --json`、裸 skill 不是插件，pi 的 `Discover()` 按设计返回空，它们只会经 `DiscoverCommands` 出现在会话的技能命令列表里。否决**本轮逆向 codex 的插件磁盘格式**：仓库现在只有 codex 插件的读侧，写侧要先逆向，体量翻倍 |
| 5 | 设置页提供**安装/卸载开关**，卸载落一条持久化的「用户已拒绝」标记 | 用户决定。这与决策 2 的自动安装直接冲突：没有标记的话用户卸载后下次启动就被铺回来。标记存进已有的 app_settings 键值表（形如 `proxy.listen_host` 的既有约定，`app_settings.go:155`），**不需要新迁移** |
| 6 | 安装时把 `agrctl` 的**绝对路径注入 SKILL.md** | 解决 Problem 3，且与 opskat 的 `skillMDWithDataDir` 同一手法。否决**把 `<AppDataDir>/bin` 加进 `clienv` 搜索路径**：那会改变所有 CLI 子进程的 PATH 语义，影响面远超本轮；也否决**让 skill 自己去猜路径**：跨平台的 AppDataDir 推导写在提示词里既冗长又易错 |
| 7 | 命名为 marketplace `agentre` / 插件与技能 `agrctl` | 与二进制同名，和 opskat 的 `opsctl@opskat` 同构，用户在管理技能里看到的 ID 能直接对应到他能在终端里敲的命令 |

## 交付物与两种落地形态

仓库里维护**一棵插件源码树**（含 `plugin.json`、marketplace 清单、`skills/agrctl/SKILL.md` 及其 references），由 `//go:embed` 打进二进制。安装时从这一份源展开出两种形态：

- **Claude Code 形态**：`~/.claude/plugins/marketplaces/agentre/` 下写 marketplace 清单，`.../agentre/agrctl/` 下写插件根（`.claude-plugin/plugin.json` + `skills/agrctl/SKILL.md`），随后把该插件登记进 `installed_plugins.json`（scope `user`、`installPath` 指向插件根、带版本与时间戳）、把 `agentre` 登记进 `known_marketplaces.json`（`source: directory`，`installLocation` 指向 marketplace 根）、并在 `~/.claude/settings.json` 的 `enabledPlugins` 里写入 `false`、在 `extraKnownMarketplaces` 里登记同一个 directory 源。观察结果：`claude plugin list --json` 报出 `agrctl@agentre`（`enabled: false`），因而它出现在「组织架构 → 管理技能」的目录里，状态为「继承全局（关）」。
- **通用形态**：`~/.agents/skills/agrctl/` 下写 `SKILL.md` 与 references，不写任何注册文件。观察结果：codex / pi 等宿主下次启动时把它算进各自的技能集。

两份 SKILL.md 内容一致（含注入后的 `agrctl` 绝对路径）。通用形态不带插件专有的 `commands/`；若插件形态提供斜杠命令，其内容以 references 的形式并入通用形态，避免同一份技能在两个宿主里给出不同的行为承诺。

## 安装时机、幂等与失败降级

启动时执行一次安装：先读「用户已拒绝」标记，标记为拒绝则整段跳过；否则比对已落地的版本标记，版本一致则 no-op，不一致或缺失则重写全部文件并重新登记。

失败一律**降级不阻断启动**，与 `bootstrap/cago.go:249-252` 对 agrctl 安装失败的处理同口径：写不动用户 home、JSON 解析失败、目录不可创建，都记一条 warn 后继续启动。观察结果：agent 照常可用，只是管理技能里没有这个包。

安装目标路径若自身或任一祖先是**符号链接**，跳过安装并记一条 info。这一条直接对应开发场景：开发者会把 marketplace 目录软链到仓库源码树，此时把注入过绝对路径的 SKILL.md 写回去会污染仓库。

## 授权与生效

用户在组织架构里给某执行档把 `agrctl@agentre` 打开后，该档的 `skills_json` 记录一条 `{id, enabled:true}`，经 `EnabledPluginsMapForTarget` → `RunRequest.EnabledPlugins` → claudecode spawn 时渲进 `--settings` 的 `enabledPlugins`（`claudecode/skills.go:15`）。本轮不改这条链路上的任何一环，只依赖它。

## 设置页：安装状态、安装与卸载

设置页新增一节，与现有的「外观 / Agent 后端 / 本地代理 / LLM 供应商」同构。它呈现三件事：Claude Code 插件是否已安装及其路径、通用目录是否已安装及其路径、以及通用目录这一份会让哪些宿主受益（一个只读的宿主名单）。用户可以在这里执行安装或卸载。

- **卸载**：删除两处已铺的文件，从 `installed_plugins.json`、`known_marketplaces.json`、`settings.json` 的三处登记里**只移除本插件/本 marketplace 的键**，并写下「用户已拒绝」标记。观察结果：`claude plugin list --json` 不再报出 `agrctl@agentre`，管理技能里该行消失；下次启动不会重新安装。
- **安装**：清除拒绝标记并立即执行一次安装，同样保持全局 `enabledPlugins` 为 `false`。
- 已存在的逐档授权（`skills_json` 里的条目）在卸载后**保留不动**。它们指向一个已不存在的包，`MergeCatalog` 对未发现的 id 天然不产出行，重新安装即恢复原授权。删授权是「删档连带删授权」那条既有语义的事，不由卸载触发。

新增文案全部经 `t(...)`，同步 `zh-CN` 与 `en` 两棵 locale 树。宿主名单里的产品名（Pi、Codex、Cursor…）是专有名词，不进 i18n。

## SKILL.md 的行为承诺

技能内容需要让 agent 做到、且只做到以下几件事：说明这是控制**本机正在运行的 agentre 桌面**的通道；给出注入后的 `agrctl` 绝对路径；给出 `ctl agents` / `ctl projects` / `ctl send` 三条命令及其参数契约（`--agent` / `--agent-id` / `--project` / `--wait` / `--isolated`）；说明桌面未运行时的报错形态（"control endpoint not found — is the desktop app running?"）。

同时要写清两条约束：`--wait` 会阻塞到对方那一轮结束，长任务应当不带 `--wait` 派发；不要把任务派回给自己所在的 agent，那会形成互相等待。

技能里**不出现 token、不出现端点 URL**——两者都由 `agrctl` 自己从握手文件解析。

## Out of scope

- **codex 的真插件形态**（让通用形态那一份也能逐档授权）。需要先逆向 codex 的插件磁盘布局与注册文件，另开一轮。
- **远端执行档（agentred）上的安装**。daemon 侧的技能目录由 `skills.catalog` RPC 回答，本轮不往远端机器铺文件。
- **`agentre-server` / `agentre-hub` 侧的任何改动。**
- **把 ctl 做成第四个内置 MCP 工具**（`agenttool` 注册表新增 `ctl`）。与 `subagent` 的 `agent_call` 大面积重叠，且与本轮"用技能而非工具交付"的方向相反。
- **旧副本迁移**（opskat 的 `legacySkillDirs`）。agentre 从未铺过任何 skill，没有历史副本要收敛。

## Testing decisions

| Seam | 验证的行为 | 现有先例 |
|---|---|---|
| 安装器（leaf 包，注入临时 HOME 与 AppDataDir） | 首次安装写出两种形态的全部文件；三份注册 JSON 的键被正确写入且**用户既有条目原样保留**；重复安装幂等；版本变化触发重写 | `internal/pkg/agrctlinstall/install_test.go` 的 EnsureInstalled 幂等/版本测试 |
| 安装器：降级路径 | JSON 损坏时不清空用户其他插件；目标路径经软链时跳过；写失败返回错误而不 panic | 同上 |
| 安装器：卸载 | 两处文件被删；三份注册 JSON 里**只有本插件的键**消失；拒绝标记被写下 | 无（新增） |
| 服务层（mockgen 注入设置项仓储） | 拒绝标记存在时启动安装被跳过；安装动作清除标记 | `internal/service/app_settings_svc/app_settings_test.go` |
| 嵌入内容的 guard test | `plugin.json` / marketplace 清单是合法 JSON 且 name 与注册用的常量一致；SKILL.md 含可被替换的路径占位；替换后不残留占位符 | `docs/testing.md` 的 guard test 约定 |
| 前端设置页（RTL） | 未安装/已安装两种状态的呈现；点击安装与卸载各调用一次对应绑定；文案全部来自 `t(...)` | `frontend/src/__tests__/i18n.test.ts` 的静态 key 与 locale 覆盖校验 |

**不能自动化的部分**：真实 `claude plugin list --json` 是否确实报出 `agrctl@agentre`、以及逐档授权后 skill 是否真的进了 CLI 的上下文——这两条依赖真实 CLI 与真实用户 home，单测里只能验到文件与注册项的形状。由收尾时的一次真机验证覆盖，按 [docs/verification.md](../verification.md) 的 start → drive → record → stop 留证据：安装后跑一次 `claude plugin list --json` 看到该行、在组织架构里给一个 agent 打开、发一轮让它跑 `ctl agents` 并拿到真实 agent 列表。

## Open questions

（无）
