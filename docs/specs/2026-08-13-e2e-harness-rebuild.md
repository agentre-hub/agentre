# 重建隔离的桌面 E2E Harness

> Status: Approved
> Owner: Agentre desktop maintainers
> Last updated: 2026-08-13

**Objective:** 将 `agentre/` 仓库的自动化 GUI E2E 重建为一个无外部基础设施依赖、强制隔离且只有 `make e2e` 一个正式入口的最小测试体系，同时将真实外部环境留给独立的本地验证流程。

**Hard invariant:** 自动化 E2E 或本地真实验证均不得发现、打开、读取、比较或修改正式 `agentre`、开发态 `agentre-dev` 的业务数据库及其 WAL/SHM，也不得读取或写入 system keychain。隔离证明只能检查本次随机运行根内实际解析和打开的 SQLite/file-keychain 路径，不能以正式数据静默为前提。E2E 专用 fake 不得进入正式 `agentre` 或 `agentred` 可执行程序的依赖图。

## Problem

1. **正式桌面入口依赖 E2E 包。** 当前 `main.go:12` 导入 `e2e/fakes`，并在完成 `bootstrap.Init` 后由 `main.go:47` 调用 `fakes.Install`；`e2e/fakes/install.go` 与 `install_noop.go` 再通过互斥 build tag 决定该调用是真实安装还是空操作。正式入口因而知道测试装配，生产与测试边界依赖编译参数而不是依赖方向。
2. **错误启动 E2E 构建会在隔离检查之前打开应用数据。** 当前 fake 在 `bootstrap.Init` 之后安装；而 `internal/pkg/paths/paths.go` 在未提供 `AGENTRE_DATA_DIR` 时会回退到正式或开发数据目录，`internal/bootstrap/keychain.go` 在未提供 `AGENTRE_KEYCHAIN_DIR` 时会使用 system keychain。独立执行带 E2E tag 的 Wails/Go 启动命令因此可能先连接真实存储，再执行 seed。
3. **已有证据表明隔离失败曾污染正式数据库。** 对本机正式库的只读检查发现 `E2E Local Backend` 与 `E2E Codex Backend` 各有 25 条软删除残留；对应名称由 `e2e/fakes/install.go` seed。该检查没有修改数据库。
4. **桌面 E2E 越过了仓库责任边界。** 当前 `make e2e-sync` 经 `e2e/run-e2e-sync.mjs` 启动真实 `agentre-server`，并要求其 MySQL/Redis 可用；对 `agentre/` 而言，Server、OAuth 及其数据基础设施都是外部系统，不应成为桌面仓库自动化 E2E 的前置条件。
5. **入口和 harness 分裂。** 当前 Makefile 同时暴露 `e2e`、`e2e-scratch`、`e2e-sync` 及 fake/real 两种 verification flavor；`e2e/` 又维护普通、scratch、sync 多套 runner/config。相同的进程、端口、数据目录和清理约束分散在多处，难以证明所有入口都 fail closed。
6. **现有 committed suite 超出本轮需要的最小回归面。** `e2e/tests/` 当前包含聊天、会话刷新、Git 预览、组织工具和子代理工具等多条规格。本轮已获准删除原有普通 E2E 内容，以三个桌面责任边界的基础冒烟重新建立可信基线。
7. **正式数据库元数据 canary 与正在运行的正式应用冲突。** 修正前 runner 会在 E2E 前后比较正式/开发 SQLite、WAL、SHM 的元数据；本地运行观察到 installed Agentre 自己的正常 WAL 写入会让 7 条 smoke 全部通过后仍判定隔离失败。该机制既不能把正式应用写入与 E2E 污染归因区分开，也迫使测试要求正式应用静默，不符合 E2E 不接触生产数据的边界。

## Actors and user stories

1. As a desktop maintainer, I want one hermetic `make e2e` command, so that I can validate the desktop stack without provisioning or trusting sibling services.
2. As a contributor, I want an incorrectly started E2E application to fail before storage initialization, so that a missing environment variable cannot mutate my real application state.
3. As a release reviewer, I want the production dependency graph to exclude all E2E composition and fake implementations, so that test behavior cannot be activated in a shipped binary.
4. As a developer investigating integration behavior, I want a separate isolated real-environment verification flow, so that I can exercise real Server, daemon or CLI integrations without weakening automated-test determinism.
5. As a contributor running the installed desktop, I want `make e2e` to coexist with it without discovering or inspecting its data files, so that automation cannot disturb or depend on my production activity.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | `agentre/` 只保留 `make e2e` 作为正式自动化 GUI E2E 入口。 | 一个入口才能统一隔离、进程生命周期和失败产物。Rejected: 保留 `make e2e-sync` 或其他正式子入口——会继续复制启动与安全契约。 |
| 2 | 删除所有 E2E build tag，使用不会被正式入口引用的独立 E2E main。 | Go 的入口依赖图能天然隔离测试装配，正式 main 不再需要测试 no-op。Rejected: 继续由正式 main 调用 build-tag no-op——依赖方向仍然错误，且错误 tag 启动仍危险。 |
| 3 | 正式 main 与 E2E main 复用同一桌面启动壳和同一应用层，只在 composition root 选择外部依赖。 | E2E 必须覆盖真实 React、Wails IPC、service、repository、migration 和 SQLite，同时避免复制窗口/lifecycle 配置。Rejected: 复制一份完整桌面 main——两份启动行为会漂移。 |
| 4 | E2E main 在 `bootstrap.Init` 之前验证 runner 签发的运行 manifest 与随机 token，并对路径做规范化和 containment 校验。 | 安全门必须先于数据库、日志和 keychain 初始化。Rejected: 只检查 `AGENTRE_ENV=test` 或目录名称——环境变量可手工误设，名称也不能证明目录由本次运行创建。 |
| 5 | `make e2e` 使用真实桌面内部栈和每次运行独占的临时 SQLite/file keychain；所有进程外边界均由本仓库 harness 提供确定性 fake。 | 这是桌面仓库拥有且能稳定验证的边界。Rejected: 启动 sibling `agentre-server`、MySQL、Redis、真实 OAuth、真实 Agent CLI 或真实 `agentred`——这些会把外部可用性变成桌面 E2E 的失败源。 |
| 6 | 自动化 suite 只重建 desktop、sync client、remote peer 三条高价值冒烟。 | 三条分别保护本地核心路径、Server 客户端协议路径和远程执行客户端协议路径。Rejected: 搬运全部旧规格——会在新隔离模型尚未稳定时恢复大而脆弱的 suite。 |
| 7 | 本地真实验证使用正式 main、真实依赖和隔离本地存储，不复用 E2E main 或 fake flavor。 | “真实验证”与“确定性自动化”职责不同。Rejected: verification 默认启动 E2E fake——无法证明真实外部集成，且继续混淆两个入口。 |
| 8 | 不修改 sibling `agentre-server/` 仓库及其 E2E。 | 三个仓库独立提交，桌面仓库只验证自己的客户端责任。Rejected: 同时重建 Server E2E——扩大范围并混合仓库所有权。 |
| 9 | `make e2e` 全程 headless，E2E 桌面原生窗口保持隐藏，由无头 Chromium 驱动 Wails bridge。 | 自动化运行不应抢占桌面、闪窗或依赖人工观看。Rejected: 先显示原生窗口再由 browser 调 `WindowHide`——当前 `App.tsx` 会在 mount 时调用 `WindowShow`，会产生可见闪窗且把隐藏时机交给测试脚本。 |
| 10 | 隔离证明只验证 E2E 自己的实际解析/打开路径，不发现或比较正式/开发数据文件。 | 启动前使用与 bootstrap 相同的数据目录解析结果核对 manifest，启动后再核对 runtime/SQLite/file-keychain 均位于本次运行根，能直接证明测试使用的资源且不依赖正式应用静默。Rejected: 继续比较正式 DB/WAL/SHM 元数据——无法归因并会把正式应用正常写入误判为 E2E 污染；要求停止正式应用——让自动化反向影响生产使用。 |

## 自动化 E2E 流程

`make e2e` 是一次自包含的 headless 运行。runner 为本次运行创建随机临时根目录、运行 manifest、随机 token、数据目录、file-keychain 目录、浏览器目录、日志目录和动态端口，然后启动由独立 E2E main 构建的桌面应用及 harness 所需的 fake 协议端点。Playwright 通过无头 Chromium 连接真实 Wails dev bridge 并驱动真实 React UI；运行期间不打开 Playwright headed browser，也不向用户显示桌面原生窗口。

桌面应用内部保持真实的链路是：React UI → Wails IPC → `internal/app` binding → service → repository → migration 后的临时 SQLite。fake 只位于应用进程外部边界或 E2E composition root，不替换内部 service/repository，也不通过前端直接写数据库来伪造业务结果。

三个冒烟场景在同一次运行的全新状态上串行执行；fixture 使用互不冲突的测试身份和对象名，不能依赖另一个场景留下的数据。Playwright 失败、应用退出、超时或 runner 收到终止信号时，runner 都负责停止自己创建的进程。成功时清理临时运行根；失败时将日志、trace、截图和只包含 fake 数据的 SQLite 复制/保留到明确报告的位置，并打印该位置。

## 独立入口与生产隔离

正式 `agentre` main 只装配正式依赖，不导入 `e2e/`、E2E fake runtime 或任何 E2E seed 包。正式 `agentred` 也不安装 E2E runtime。删除 `//go:build e2e`、`//go:build !e2e` 及对应的 no-op 文件；fake 可以是普通 Go 包，但只能从独立 E2E main 的依赖图到达。

正式 main 和 E2E main 通过一个可复用的桌面启动壳共享 Wails lifecycle、bindings、平台差异和数据目录相关行为；启动壳接受由入口显式提供的窗口可见性策略。正式入口保持当前启动后显示窗口的产品行为，E2E 入口则从创建起保持 `StartHidden`，并使前端初始化不执行 `WindowShow`、`WindowCenter` 或窗口尺寸读写等原生窗口显示/管理动作。该差异只能由入口显式注入的运行模式决定，不能通过可被正式应用偶然继承的环境变量秘密切换。共享壳不依赖 E2E 包，业务页面、Wails bindings 和后端 lifecycle 仍保持相同。

headless 不等于绕过桌面应用：Wails backend 和 dev bridge 仍然真实运行，Playwright 访问同一 React 页面并调用真实 Wails IPC。若当前平台无法在不展示原生窗口的条件下提供该 bridge，runner 必须明确失败，不能回退为显示窗口，也不能退化为纯 Vite 浏览器测试。

E2E main 不是受支持的手工启动入口。缺少有效 runner manifest/token 时，直接启动它必须以非零状态退出，且退出发生在 bootstrap、数据库迁移、日志目录创建、keychain 初始化和 seed 之前。正式构建流程始终以正式 main 为根，不构建或打包 E2E main。

## 强制存储隔离

runner 使用操作系统临时目录下由安全随机后缀创建的独占运行根。manifest 位于该运行根内，记录规范化后的运行根、SQLite 数据目录和 file-keychain 目录；环境只传递 manifest 路径、匹配 token 及应用已有的存储覆盖变量。token 不写入日志或测试报告。

E2E main 在任何持久化初始化前执行以下验证：

- manifest 可读、格式有效，且 token 与环境中的值匹配；
- manifest、数据目录与 keychain 目录经解析符号链接和规范化后仍位于同一个本次运行根内；
- `AGENTRE_DATA_DIR` 与 `AGENTRE_KEYCHAIN_DIR` 分别等于 manifest 声明的目录；
- 两个目录都不是平台默认的 `agentre`、开发态 `agentre-dev`、它们的父目录或子目录；
- 目录由本次 runner 创建，且在支持 POSIX 权限的平台上没有向其他用户开放；
- 运行根/token 不可被另一次 E2E run 复用。

任一条件失败时，错误信息只说明失败的隔离条件和正确入口，不回显 token，不尝试“修复”路径，也不回退到任何默认目录。E2E main 在调用 bootstrap 前还必须使用 bootstrap 的实际数据目录解析入口，确认结果与 manifest 的规范化 `dataDir` 完全一致；不一致时在日志、数据库和 keychain 初始化前失败。

bootstrap 完成后，E2E composition 再确认 runtime 报告的数据目录等于同一 `dataDir`。SQLite oracle 通过实际 UI 写入与 `PRAGMA database_list` 证明默认数据库文件位于 `<runRoot>/data/agentre.db`；E2E 生成的 keychain 凭据必须作为 file-keychain 文件出现在 manifest 的 `keychainDir`。任一实际路径越出运行根时测试失败并只清理本次运行资源。runner 不枚举、不 `stat`、不打开，也不比较正式或开发数据目录下的任何 SQLite、WAL、SHM 文件，因此正式 Agentre 可在 E2E 期间继续运行。

## Desktop smoke

在全新临时数据库上启动应用时，无头 Playwright 页面能观察并操作完整主界面，而用户桌面上不出现原生窗口或 headed browser。Playwright 创建会话并发送一条消息后，独立 E2E composition 提供的确定性 agent runtime 返回稳定的流式回复；页面显示用户消息和最终回复，且没有启动 Claude Code、Codex、Pi 或其他真实 CLI。

页面 reload 或等价的前端重载后，会话、用户消息和回复仍可见。只读 SQLite oracle 从本次 manifest 指定的数据库读取对应记录，证明结果经过真实 Wails/service/repository/migration 持久化路径，而不是仅停留在 DOM 或 fake 中。

fake runtime 返回失败时，桌面按既有用户可见错误契约结束该轮，不留下永久 running 状态；该失败行为作为 desktop 场景的边界用例。

## Sync client smoke

E2E composition 将桌面的真实 Server/sync client 指向 harness 内的 fake HTTP server，并建立等同于“已完成登录”的临时测试身份。测试不进入 GitHub OAuth，也不启动 Server Web UI、`agentre-server`、MySQL 或 Redis。

用户从桌面 UI 创建或修改一个由桌面同步客户端负责的账号级对象时，真实 sync service 和 HTTP client 将协议请求发送到 fake server。fake 记录请求后返回协议有效的确认；测试观察请求中的身份、对象类型、版本/游标和业务 payload，证明桌面正确转换并推送本地状态。

fake server 随后以另一设备身份提供一条有效远端变更。桌面执行正常 pull/apply 路径后，UI 与临时 SQLite 都出现收敛后的对象。fake 返回鉴权拒绝或无效响应时，桌面必须表现为可观察的同步失败，保持已有本地对象不被静默覆盖；测试不评价 Server 内部如何鉴权、存储、事务提交或解决 Server 自己的并发。

## Remote peer smoke

E2E composition 将桌面的真实 remote client 指向 harness 内实现最小现有协议的 fake relay/agentred peer。桌面仍使用正式 WebSocket/JSON-RPC 编解码、连接状态、事件转换、chat service 和 SQLite 持久化逻辑；不启动真实 `agentred` 进程或真实 agent runtime。

用户选择测试用远端执行目标并发送消息后，fake peer 收到桌面发出的运行请求，按协议返回确定性流式事件和终态。UI 显示最终回复，SQLite oracle 证明消息与远端执行状态已经通过桌面自己的落库路径持久化。

当 fake peer 在流中断开或返回协议错误时，桌面按已有重连/失败契约进入可观察的终态，不永久停留在 running，也不把该次失败写入正式数据目录。本场景不宣称覆盖真实 daemon 的数据库、runtime 选择、LAN 网络、Server relay 或浏览器 Web UI。

## 本地真实验证

本地真实验证保留“启动 → 驱动 → 记录 → 停止”的 workflow 和 `drive.mjs` 证据机制，但启动的是正式 main，不提供 fake flavor，也不使用 E2E manifest。它通过现有隔离 target 为正式应用提供专属 `AGENTRE_DATA_DIR`、file-keychain、浏览器目录和端口；缺少这些隔离条件时 launcher 拒绝启动。verification 的浏览器仍可默认 headless，并按场景选择 headed；因为它运行正式 main，原生桌面窗口保持正式产品行为，不受 `make e2e` 的隐藏策略约束。

验证者可按场景配置真实 `agentre-server`、真实 `agentred` 或真实 Claude Code/Codex/Pi CLI。真实外部服务不可用时，验证明确失败或保持未验证，不能自动降级到 fake。停止默认保留隔离数据用于调查，显式 wipe 只删除 launcher 已证明属于当前 checkout/session 的目录。

`make e2e-scratch` 不再作为正式自动化入口；一次性功能验收使用 driven verification workflow。verification 不属于 CI 的 `make e2e`，其环境与结论按 `docs/verification.md` 记录。

## 兼容性、安全与隐私

本轮不改变用户可见产品功能、数据库 schema、现有业务数据格式或前端文案。正式应用的数据目录优先级和真实 keychain 行为保持不变；新安全门只存在于独立 E2E main 和 verification launcher。E2E runner 不解析正式/开发数据目录，也不要求正式应用停止或静默。

E2E fake 只使用生成的测试身份、测试凭据和测试内容。日志、trace、截图和保留数据库不得包含开发者正式 token、keychain secret、真实账号数据或 sibling 仓库配置。fake HTTP/WebSocket 端点只绑定 loopback 和 runner 分配的动态端口，不监听 LAN。

支持从不同 git worktree 并行运行 E2E：每个 run 使用独立随机目录和动态端口。单次 run 内不并行启动多个桌面应用，也不采用固定 `/tmp/agentre-e2e-*` 目录作为所有运行共享状态。

## 删除与文档收敛

删除原有普通、scratch 和真实 Server sync 的 committed specs、runner/config 及只服务于旧架构的 seed/no-op 文件；以新的单 runner、单 Playwright 配置、三条 smoke 和对应协议 fake 重建 `e2e/`。不保留 `make e2e-sync`、`make e2e-scratch`、`test:sync` 或 build-tag 兼容别名。

`e2e/README.md` 继续拥有自动化 harness 和本地 verification 的机械说明；`docs/verification.md` 继续拥有真实验证的证据流程。所有关于 `-tags e2e`、fake verification flavor 和真实 Server sync suite 的当前态文档必须删除或改写，不留下 deprecated 说明。历史 specs 不因本轮重写历史事实；仅当它们被当前 contributor docs 当作现行操作指引时，修正其链接或表述。

## Out of scope

- 修改或删除 `agentre-server/`、`agentre-hub/` 及其 E2E。
- 自动化真实 GitHub OAuth、MySQL、Redis、Server Web UI 或 sibling Server 的持久化行为。
- 自动化真实 `agentred` 进程、daemon SQLite、LAN 发现、真实 relay 或真实 Claude Code/Codex/Pi CLI。
- 恢复旧 suite 的 Git 预览、组织工具、子代理工具等扩展回归；后续只有在明确归属桌面边界且具备稳定 seam 时才逐条加入。
- 清理正式数据库中已存在的 E2E 残留；任何清理需另行确认、备份和只针对已证明的测试数据执行。
- 产品 UI、业务 schema、迁移或同步协议的功能变更。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| E2E 启动 preflight 的 Go 测试 | 有效 manifest 能进入启动流程；缺 token、token 不匹配、路径越界、符号链接逃逸、正式/开发目录及不安全权限均在 bootstrap 前失败 | 现有 `e2e/fakes/identity_test.go`、`keychain_test.go` 和 `e2e/lib/target.test.mjs` 提供部分隔离先例，但当前没有独立 main preflight |
| 生产依赖图 guard | 正式 `agentre` 与 `agentred` 不依赖 `e2e/` 或 fake runtime；仓库不存在 E2E build tag/no-op seam | 现有 daemon runtime import guard；本轮新增针对入口依赖图的机械检查 |
| Runner/target Node 测试 | 每次运行使用独占路径和动态端口；不同 worktree/run 不冲突；signal/失败清理只触及本次运行资源；runner 不发现或读取正式/开发数据路径 | `e2e/lib/run-context.test.mjs`、`target.test.mjs` 与 `lib/procs.mjs` |
| E2E 实际存储路径证明 | bootstrap 前的实际 data-dir 解析、bootstrap runtime、SQLite `PRAGMA database_list` 和 file-keychain 产物均指向 manifest 声明的本次运行根；越界时 fail closed | `e2e/app`、`e2e/composition`、`e2e/fixtures/db.ts` |
| Desktop Playwright smoke + SQLite oracle | 无头 Chromium 下真实 UI/IPC/service/repository/migration 的启动、聊天、流式回复、reload 持久化及 runtime 失败终态 | 现有 `smoke-chat.spec.ts`、`session-reload.spec.ts` 和 `fixtures/db.ts`，但规格将重写 |
| Sync client Playwright smoke + fake HTTP protocol recorder | 桌面拥有的 push/pull 转换、身份/游标传递、远端变更 apply，以及拒绝/无效响应时不破坏本地状态 | 现有 `sync/workspace-sync.spec.ts` 覆盖真实 Server 路径；本轮改为仓库内 fake Server 边界 |
| Remote peer Playwright smoke + fake WebSocket/JSON-RPC peer | 桌面拥有的连接、请求编码、事件流转换、持久化，以及中断/协议错误后的终态 | 现有 `e2e/fakes/remote.go` 与跨仓库 dual 路径提供协议先例；本轮不启动真实 daemon/Server |
| E2E 窗口策略测试 | E2E 入口保持 `StartHidden`，前端在该显式运行模式下不调用 `WindowShow`/`WindowCenter`/窗口尺寸 API；正式模式仍保持既有显示顺序 | 当前 `App.test.tsx` 已覆盖正式窗口恢复后显示；本轮补充 headless 分支 |
| 正式构建检查 | 正式 app/agentred 能在无 E2E tag、无 E2E 环境变量时构建，产物不包含 E2E composition | 当前 `make build`、`make agentred`；本轮由依赖图 guard 补强 |
| Driven real verification | 自动化无法证明的真实 Server、daemon、CLI 和平台集成，由隔离正式 main 的本地运行观察并按 verification report 记录 | `verify.mjs`、`drive.mjs`、`docs/verification.md` |

CI 继续调用与本地相同的 `make e2e`，只安装 Wails、前端依赖、Playwright/Chromium 与桌面 GUI 系统依赖；不配置 Server、OAuth、MySQL、Redis、真实 daemon 或 Agent CLI。完成实现时还需运行仓库既有 backend、frontend、lint 和文档链接检查，以证明入口重构没有破坏非 E2E 构建。

## Relevant links

- [`../architecture.md`](../architecture.md)
- [`../testing.md`](../testing.md)
- [`../verification.md`](../verification.md)
- [`../../e2e/README.md`](../../e2e/README.md)
- [`../../AGENTS.md`](../../AGENTS.md)

## Open questions

无。
