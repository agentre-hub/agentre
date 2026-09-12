# agentred 容器化与 dev 环境自动部署

> Status: Approved
> Owner: agentre maintainers
> Last updated: 2026-09-02

**Objective:** 向 Gitea 推送 `dev` 分支后，`coding.local` 上的 agentred 在无人介入的情况下被替换为该 commit 构建出的容器，并且容器自带 claude / codex / pi 三个 CLI、既有会话与凭据不丢失。

> **Amendment 2026-09-02（本文件已按此修订）：以速度换严格，镜像不过 registry。** 用户决定 dev 链路不跑 lint / test，镜像也不在 CI 里构建：改为 runner 上 `make agentred-linux` 编出静态二进制、`scp` 到目标机，目标机用 `deploy/Dockerfile` 的 `prebuilt` 目标打镜像。三个 CLI 的安装层由 `RUNTIME_IMAGE` 与版本 arg 决定，不变就命中 layer cache，每次部署只重打最后那个 `COPY`。受影响的是决策 5 与「dev 流水线」一节，下文已改写。代价明说：dev 跑的东西没有 registry 里可追溯的 digest，只剩 `agentred --version` 里 ldflags 钉的 commit；且推 dev 之后**没有任何门禁接着**——`.github/workflows/ci.yml` 只在 push `main` / `develop/*` 与 pull_request 上触发，`dev` 不在其中且没有开着的 PR，所以这些改动第一次被 CI 看到是它合入 `main` 的那个 PR，在那之前只有本地 `make lint` / `make test`。
>
> 同时补两条实施期核实到的事实：(1) `coding.local` 与 runner 宿主**都拉不到 docker.io**（`registry-1.docker.io` 直接 EOF），`Dockerfile` 的默认值保持上游正确，dev 链路用 `--build-arg RUNTIME_IMAGE=` 指向可达镜像源；(2) `buildinfo.ShortCommitID()` 截前 7 位，而本仓库 `git rev-parse --short` 给 8 位，部署校验必须按 7 位比对。

**Hard invariant:** 已 claim 的账号凭据、会话日志与 agent 工作目录跨重部署存活；GitHub 侧的 `release.yml` / `nightly.yml` 发布链路与 `make agentred-package` 的产物形态不变。

## Problem

1. **agentred 的更新完全靠手工推二进制。**（已核实）目标机上它是 `systemd --user` 服务（`/root/.config/systemd/user/agentred.service`，`ExecStart="/usr/local/bin/agentred" run`），更新路径是本机执行 `make agentred-deploy-local-coding`——`Makefile:102-105` 经由 opsctl `cp` + `exec` 推二进制。没有任何自动化触发。
2. **`agentre` 仓库没有 Gitea 远端。**（已核实）`git remote -v` 只有 `origin`（GitHub），Gitea 上不存在对应仓库，因此当前无法用 Gitea 流水线交付 agentred。
3. **仓库零容器化基础。**（已核实）全仓库没有任何 Dockerfile / compose / `.dockerignore`；e2e 是本机 Playwright + wails 二进制方案，不涉及容器。
4. **agentred 有两个数据目录，只认一个会静默丢数据。**（已核实）`AGENTRED_DATA_DIR` 只决定 daemon 自身状态（`internal/pkg/paths/paths.go:62-78`：`state.json` / `agentred.db` / `agentred.sock` / `logs/`）；而 Agent 工作目录 `<AppDataDir>/agents/<agentID>/`（`internal/pkg/agentruntime/cwd.go:19-31,51-68`）与 pi 扩展物化目录 `<AppDataDir>/piagent/ext/`（`internal/pkg/agentruntime/runtimes/piagent/mcpbridge/bridge.go:35-42`）走的是 `AGENTRE_DATA_DIR`。目标机上后者是 `/root/.config/agentre`，实际存有 18 个 agent 工作目录（最近写入 2026-08-30）、`claudecode-runtime/`、`agentre.db`，共 492K。
5. **会话 `cwd` 与后端 `cli_path` 都是宿主下发的绝对路径。**（已核实）`daemon_sessions.cwd` 由宿主写入；CLI 路径经 `cli.resolvePath` / `CLIPathForBackend`（`internal/daemon/daemon.go:1427`）解析。容器内这些绝对路径解析不通，历史会话就打不开、后端就起不来。

## Actors and user stories

1. 作为维护者，我希望 `git push gitea dev` 之后 `coding.local` 上的 agentred 自动变成这个 commit，这样我不再需要手工推二进制。
2. 作为维护者，我希望 agentred 及其三个 CLI 的版本由仓库里的文件决定，这样 dev 环境的行为可复现，CLI 上游发版不会在我不知情时改变环境。
3. 作为想在自己机器上跑 agentred 的用户，我希望有一份能直接用的 compose，不必自己解决 CLI 安装与配置持久化。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | agentred 以容器运行，同时交付一份面向外部用户的 compose | 用户决定。Rejected: 保持 `systemd --user` 裸进程——改动最小且 CLI 已原生装好，但交付不出可复用的编排 |
| 2 | 镜像把三个 CLI 装在 `/usr/local/bin/{claude,codex,pi}`，桌面端 `cli_path` 一次性改到新路径 | 用户决定。规范路径让对外那份 compose 干净。Rejected: 容器内补建 `/root/.local/bin/claude` 等旧路径 symlink——桌面端免改，但把一台机器的历史目录布局固化进公开镜像 |
| 3 | 三个 CLI 的版本在 Dockerfile 里 pin 死并暴露为 build-arg，默认 claude 2.1.224 / codex 0.146.0 / pi 0.84.3 | 用户决定，版本取自目标机现状（已核实）。构建可复现，升级是一次显式提交。Rejected: 每次构建拉 latest——同一 commit 两次构建出的环境不同，CLI 上游行为变更难归因 |
| 4 | 桥接网络 + `7456:7456` 端口映射，接受 pair 广播退化 | 用户决定（已知情）。Rejected: `network_mode: host`——广播地址正确但对外 compose 仅 Linux 可用；Rejected: 新增 `AGENTRED_ADVERTISE_HOST` 拆分绑定与广播地址——根治但属于新特性，本轮范围明显变大 |
| 5 | dev 链路不设门禁（见上方 Amendment） | 用户决定，与 `agentre-server` 的 dev 链路同口径。Rejected: 全量 `make lint` + `make test`——要求 runner 具备 wails / node / pnpm 且每次推送等几分钟，与 dev「推上去看看效果」的定位不匹配；Rejected: 只跑 `lint-backend` + `test-backend` |
| 6 | 首次切换（停用 systemd 单元、备份数据目录、改桌面端 `cli_path`）人工执行一次，不进流水线 | 用户决定。这些动作只有第一次有意义。Rejected: 部署脚本内置接管逻辑——之后永远是死代码 |
| 7 | 不为 dev workflow 写契约测试，并在本轮删除 `scripts/test-release-workflows.py` 及其两处引用 | 用户决定，且在被告知该脚本守护的是 6 平台矩阵完整性、ldflags 符号路径（禁止缩写形式）与 keychain 步骤顺序之后重申。Rejected: 保留脚本仅不纳入 dev workflow；Rejected: 扩展它去盯 Dockerfile 与 `make agentred-package` 的构建一致性 |

## 镜像

镜像分构建与运行两段。构建段用 Go 工具链以 `CGO_ENABLED=0` 编译 `./cmd/agentred`，注入与 `Makefile` 同一组 ldflags 符号（`github.com/cago-frame/cago/configs.Version` 与 `github.com/agentre-hub/agentre/internal/buildinfo.CommitID`），使容器内 `agentred --version` 能对回具体 commit。SQLite 驱动是纯 Go 的 `glebarez/sqlite`（已核实），因此二进制无需 libc。

运行段必须提供 agentred 运行期真正依赖的东西，这份清单来自对调用点的核实而非推测：

- **`git`** —— `internal/pkg/workspacefs/git.go:27` 是 daemon 运行期唯一常规外部命令（`status --porcelain=v1`、`diff`、`for-each-ref`、`check-ignore` 等，全部带 `--no-optional-locks`）。
- **一个 shell** —— 远程终端取 `$SHELL` 缺省 `/bin/sh`（`internal/pkg/pty/local/local.go:35-50`）；`clienv` 会用 login shell 恢复 PATH（`internal/pkg/clienv/clienv.go:247`，800ms 超时，shell 不存在则整段跳过不报错）；claudecode 的 PostToolUse hook 是以**命令字符串**形式下发的（`runtimes/claudecode/active.go:335-356`），由 Claude Code 自己当 shell 命令执行。
- **`node`** —— codex 与 pi 是 npm 包；claude 的 CLI 亦依赖 node 生态子工具（`pkg/claudecode/process.go:62-65` 的注释列出 `git`/`ripgrep`/`bash`/`node`）。
- **三个 CLI 本体**，版本按决策 3 pin 死。

镜像**不需要** `agrctl`：`hookBin()` 在 `hookCLIPath` 为空时回落 `os.Executable()`（`runtimes/claudecode/hookcli.go:11-16`），而 `SetHookCLIPath` 全仓库只有桌面端 `internal/bootstrap/cago.go:261` 一处调用，daemon 自带 `claudecode` 子命令充当 hook。镜像也**不需要** ripgrep：workspacefs 的搜索是 `os.ReadDir` + `git check-ignore`（`internal/pkg/workspacefs/search.go:70-77`）。

构建镜像时验证三个 CLI 确实可执行且版本正确，版本不符即构建失败——这是「CLI 版本可复现」这条承诺的唯一自证点。

## 运行形态与持久化

容器以 `agentred run` 为入口常驻，`restart: unless-stopped`，宿主重启后自行恢复。容器内 `HOME` 为 `/root`，使所有相对 `~` 的路径与宿主语义一致。

宿主目录以**相同的绝对路径**挂入容器——这是问题 5 的直接约束，路径一旦不同，历史会话的 `cwd` 与后端的 `cli_path` 就解析不通：

| 宿主路径 | 性质 | 为什么必须有 |
| --- | --- | --- |
| `/root/code` | 读写 | 会话 `cwd` 都在这下面 |
| `/root/.config/agentred` | 读写 | daemon 状态：账号 claim 凭据、会话与通知日志 |
| `/root/.config/agentre` | 读写 | Agent 工作目录与 pi 扩展物化目录（问题 4） |
| `/root/.claude`、`/root/.claude.json` | 读写 | Claude Code 配置；`~/.claude/projects` 还是 UserAnchor 的来源（`runtimes/claudecode/active.go:366-377`） |
| `/root/.codex` | 读写 | Codex 配置与 `sessions` / `session_index.jsonl`（`runtimes/codex/transcript.go:40-55`） |
| `/root/.pi` | 读写 | Pi 配置与 `~/.pi/agent/sessions`（`runtimes/piagent/transcript.go:37-47`） |
| `/root/.agents` | 读写 | 技能目录 |
| `/root/.ssh`、`/root/.gitconfig` | 只读 | 会话内 git 操作所需 |

CLI 的**配置**来自这些挂载，CLI 的**二进制**来自镜像——这条分工是决策 2 与决策 3 的结果：升级 CLI 靠改 Dockerfile 重新发版，而不是靠改宿主。

## 网络与已知限制

容器用桥接网络并把 7456 映射到宿主。daemon 反向外拨到账号服务器（`internal/daemon/relaytransport/hub.go`）这条主路径不受影响，桌面与网页经中继找到它。

**已知限制：`agentred pair` 与 `agentred status` 报出的 LAN 地址不可用。** `LANServer.AdvertiseURLs()`（`internal/daemon/protorpc/server.go:115`）在通配绑定下枚举本进程网卡，容器内取到的是桥接网段地址而非宿主地址。直连局域网配对因此失效；用户已知情并接受，理由是当前 `state.json` 的 `pairedPeers` 为空、实际走中继。修复属于另一轮。

**次要限制：** `zalando/go-keyring` 在 Linux 需要 Secret Service（`internal/pkg/ccoauth/keychain_zalando.go:6`），容器内通常不具备，会走 `$CLAUDE_CONFIG_DIR` 或 `~/.claude/.credentials.json` 文件回退（`internal/pkg/ccoauth/fetch.go:56-73`）。影响面仅限 claudecode 用量查询，不影响会话主流程。

## 对外可用的 compose

同一份 Dockerfile 与 compose 既服务于 dev 部署，也作为外部用户可直接使用的编排交付。面向外部用户的那份以环境变量表达宿主侧路径，默认值对应「配置在用户 home 下」的常见形态，并随附说明：需要挂哪些目录、为什么工作区必须同路径挂载、桥接网络下 pair 广播的限制、以及三个 CLI 的版本由镜像决定。

dev 环境与外部用户用的是同一份编排，差异只体现在取值上——不维护两套编排，避免其中一套长期无人验证而腐化。

## dev 流水线

推送到 Gitea 的 `dev` 分支触发。门禁为全量 `make lint` 与 `make test`，全部通过才构建。这要求 runner 具备 Go 1.26、golangci-lint v2.12.2、Node 22、pnpm 10 与 wails v2.12.0，并满足两个已核实的前置条件：`frontend/dist` 占位文件必须先造出来（根包 `main.go:12` 有 `//go:embed all:frontend/dist` 而该目录被 gitignore），以及 `wails generate module` 会真的执行 `main()`，Linux 上需要一个隔离的 `AGENTRE_KEYCHAIN_DIR` 才能绕开缺失的 Secret Service（对照 `.github/workflows/ci.yml:115-122`）。

门禁通过后构建镜像、推送到 registry，tag 为 `dev.<short-sha>`，只构建目标机所需的 linux/amd64。随后 SSH 到 `coding.local` 拉取该 tag 并以新镜像重建容器。

部署成功的判据是容器内 `agentred status` 能通过本地 IPC 应答，且 `agentred --version` 输出的 commit 与本次构建一致；未在超时内满足即判定失败，流水线为红。不做自动回滚，理由与 dev 环境的定位一致：坏状态应当可见。

重启 agentred 会中断进行中的会话，这是每次部署的固有代价，不做优雅接管。

## 附带变更：删除 workflow 契约测试

按决策 7，本轮删除 `scripts/test-release-workflows.py`，并移除其仅有的两处引用：`Makefile:194` 与 `.github/workflows/ci.yml:138`。`scripts/test-install.sh` 与 `scripts/test-install.ps1` 保留，`make test-agentred-packaging` 与 ci.yml 的 `agentred-packaging` job 继续存在、只是不再跑契约断言。该脚本不在 `make test` 链上（`test: test-backend test-frontend`），因此与本轮门禁无交互。

删除后失去的保护：release / nightly 的 6 平台矩阵完整性、ldflags 符号路径不得写成缩写形式、Linux 桌面构建的 keychain 步骤必须早于 build。这些约束此后无自动校验。

## Out of scope

- **agentre-server 的 dev 部署** —— 见 `agentre-server` 仓库的 `docs/specs/2026-09-02-server-dev-deploy.md`，独立提交、无硬依赖。
- **修复桥接网络下的 pair 广播地址**（`AGENTRED_ADVERTISE_HOST` 之类）。
- **CLI 版本的自动升级**、镜像清理、多架构镜像、dev 环境自动回滚。
- **桌面端与 `agentred service` 子命令的行为** —— 容器内不使用 systemd/launchd 路径。

## Testing decisions

按决策 7，本轮不为 dev workflow 编写契约测试。验证放在构建与流水线内部：

| Seam | What it verifies | Prior art |
|---|---|---|
| 镜像构建阶段的 CLI 自证 | 三个 CLI 存在、可执行、版本等于 pin 值；这是「版本可复现」的唯一守卫 | 无 |
| 流水线内 `docker compose config` 校验 | 编排语法与变量插值可解析，缺变量在部署前失败 | 无 |
| 部署后 `agentred status` + `agentred --version` | daemon 真的起来了，且跑的是本次 commit | `scripts/install.sh:95-99` 用同样的 `--version` 自证方式验收安装 |
| 既有 `make lint` + `make test` | 代码门禁，与 `main` 同口径 | `.github/workflows/ci.yml` |

无法自动化的部分：**现有 Gitea runner 到 192.168.8.188 的 SSH 可达性是用户陈述，未经核实**——首次 `push dev` 即是验证，若不通则部署方式需重新审批。一次性迁移的正确性同样只能由那一次真实执行验证。容器内三个 CLI 能否真的驱动会话（挂载是否齐全、路径是否解析得通），只能在迁移后由一次真实会话确认。

## Open questions

（无）
