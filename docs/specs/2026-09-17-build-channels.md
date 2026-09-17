# Build-time channels

> Status: Approved
> Owner: desktop app maintainers
> Last updated: 2026-09-17

**Objective:** 正式版、Beta、Nightly、Dev 是四个彼此独立、可并排安装运行的渠道；渠道只由构建标记决定，每个渠道只更新到同渠道的发布。

**Hard invariant:** 以 `stable` 标记构建的正式版，其名称、bundle id、数据目录、钥匙串槽位、发布包名和更新来源与现状一致；任何渠道都不会读写别的渠道的数据或钥匙串，也不会装上别的渠道的发布包。

## Problem

1. **运行时更新通道与构建身份互相矛盾。** 数据目录已由构建标记决定（`internal/pkg/paths/paths.go` 的 `Channel`，当前分支 `feat/beta-build`），但更新来源仍读 `app_settings.update.channel`（`internal/service/update_svc/settings.go:14`、`schedule.go:85`）。在正式版里切到 beta 更新，装上的包没有渠道标记，仍是正式版；在 Beta 里装上正式版包，会得到一个叫 Agentre Beta、却读正式版数据的应用。（已验证）
2. **Beta 通道会回落到正式版。** `fetchLatestBetaRelease` 取最近 20 个 release 中第一个非 draft、非 `nightly` 的 release，不区分预发布与正式发布（`internal/service/update_svc/update.go:180`）。（已验证）
3. **发布包按平台子串匹配。** 桌面端更新取第一个名字包含 `<goos>-<goarch>` 的 asset（`update.go:403`），同一 release 里的 `agentred-…-<goos>-<goarch>` 包也满足该条件。（已验证）
4. **macOS 更新写死 `Agentre.app`。** 从 dmg / zip 里取新 bundle 时固定找 `Agentre.app`（`update.go:548`、`update.go:592`）。（已验证）
5. **CI 发布的 beta / nightly 包没有渠道身份。** `release.yml` 与 `nightly.yml` 构建时不注入渠道，产物统一叫 `Agentre.app` / deb `agentre` / NSIS `Agentre`，与正式版互相覆盖安装；`-beta` 与 `-rc` 都标为预发布（`.github/workflows/release.yml:294`）。（已验证）
6. **钥匙串所有安装共用，Dev 靠运行方式特判。** `internal/pkg/keychain/system.go:10` 固定 service 名 `agentre`，设备指纹、同步登录 token、后端凭据在正式版、`make dev` 与其他安装之间共享，服务器把它们视为同一台设备。Dev 的数据目录靠 wails dev 注入的 `devserver` 环境变量判定（`paths.go` 的 `IsDevMode`），没打标记的其他构建（`go run`、`make build`）直接落到正式版数据。（已验证）

## Actors and user stories

1. As a `维护者`, I want 在本机同时运行正式版、Beta、Nightly 和 Dev, so that 调试任何一个都不影响其他渠道的数据与登录。
2. As a `Beta / Nightly 用户`, I want 应用自动更新到本渠道的新发布, so that 不需要手动下载，也不会被换成别的渠道。
3. As a `发布者`, I want 推 tag 或跑 nightly 时 CI 自动产出对应渠道的安装包, so that 渠道身份不依赖手工改名。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 四个渠道 `stable` / `beta` / `nightly` / `dev` 地位相同，都只由构建标记决定；删除运行时更新通道设置 | 用户决定。Rejected: 保留设置——与构建身份冲突（Problem 1） |
| 2 | 没有渠道标记的构建属于 Dev；正式版必须显式标记 `stable` | 用户决定。任何忘记打标记的构建都碰不到正式版数据。Rejected: 默认正式版——需要为 wails dev 另设特判，Dev 与其他渠道判定方式不同 |
| 3 | 标记不是四个取值之一时：`make` 在构建前失败；已构建的进程拒绝启动并说明原因 | 不静默归入任何渠道，避免读写错误渠道的数据。Rejected: 回落到某个渠道——掩盖构建错误 |
| 4 | 桌面端、本地打包、CI 在同一轮完成 | 用户决定。Rejected: 先桌面端后 CI——过渡期线上 Beta/Nightly 无法更新 |
| 5 | Beta 只更新到 `-beta` tag 的发布；正式版 tag 不额外产出 Beta 包 | 用户决定。Rejected: Beta 也跟正式版 tag——构建与上传量翻倍 |
| 6 | 不再使用 `-rc` tag；发布 tag 只接受 `vX.Y.Z` 与 `vX.Y.Z-beta.N` | 用户决定。Rejected: rc 按正式版出包标预发布——多一种没有自动更新路径的发布形态 |
| 7 | macOS、Linux、Windows 都按渠道独立安装 | 用户决定。Rejected: 先只做 macOS——其他平台的 Beta/Nightly 会覆盖正式版 |
| 8 | 钥匙串按渠道分槽位，四个渠道都不共用 | 用户决定。Rejected: 共用——各渠道在同步服务器上是同一台设备，一处登出或改凭据影响其他渠道 |
| 9 | Dev 没有发布：CI 不发布 Dev 包，Dev 不检查、不安装更新，也不提供远端 agentred 一键升级 | Dev 构建来自源码，没有对应的发布可更新或升级到。Rejected: Dev 借用正式版的更新 / 升级来源——Dev 与正式版不共用任何东西 |
| 10 | 远端 agentred 一键升级使用桌面端自己的渠道 | 设置删除后这是唯一的渠道来源。Rejected: 升级对话框里让用户选渠道——重新引入运行时渠道 |
| 11 | agentred 不按渠道拆分，保留 `agentred update --channel` | agentred 是无界面单二进制，没有安装身份与本地数据并存问题 |
| 12 | 版本号旁只在非正式版显示渠道标签（Beta / Nightly / Dev） | 设置删除后用户仍需知道当前运行的是哪个渠道；正式版不加噪音 |
| 13 | Dev 显示名为 `Agentre Dev`，替换现有 `Agentre (Dev)` | 四个渠道统一「Agentre + 渠道名」 |
| 14 | 手动构建工作流（`manual-build.yml`）产出 Dev 渠道包 | 手动构建用于测试，不是发布；按决定 2 不打标记即 Dev，不会覆盖用户已装的正式版 |

## Channel identity

| | 正式版 | Beta | Nightly | Dev |
|---|---|---|---|---|
| 构建标记 | `stable` | `beta` | `nightly` | `dev` 或不打标记 |
| 显示名称 | Agentre | Agentre Beta | Agentre Nightly | Agentre Dev |
| 数据目录名 | `agentre` | `agentre-beta` | `agentre-nightly` | `agentre-dev` |
| macOS bundle | `Agentre.app`，`com.agentrehub.agentre` | `Agentre Beta.app`，`com.agentrehub.agentre.beta` | `Agentre Nightly.app`，`com.agentrehub.agentre.nightly` | `Agentre Dev.app`，`com.agentrehub.agentre.dev` |
| 钥匙串 service | `agentre` | `agentre-beta` | `agentre-nightly` | `agentre-dev` |
| Linux | 包 `agentre`，命令 `/usr/bin/agentre`，桌面入口 Agentre | `agentre-beta`，`/usr/bin/agentre-beta`，Agentre Beta | `agentre-nightly`，`/usr/bin/agentre-nightly`，Agentre Nightly | `agentre-dev`，`/usr/bin/agentre-dev`，Agentre Dev |
| Windows | 产品名 Agentre | 产品名 Agentre Beta | 产品名 Agentre Nightly | 产品名 Agentre Dev |
| 发布包前缀 | `agentre-` | `agentre-beta-` | `agentre-nightly-` | 无（不发布） |

- 同一台机器上四个渠道可以同时安装、同时运行，彼此看不到对方的数据库、日志、agent 工作目录、单实例锁和钥匙串条目。
- 渠道只看构建标记；`make dev`、`wails dev`、`go run` 都按各自带的标记归属，没有基于运行方式的特判。
- Windows 上各渠道的安装目录、卸载项、开始菜单与桌面快捷方式、WebView2 存储互不覆盖；卸载一个渠道不影响其他渠道。
- `AGENTRE_DATA_DIR` 覆盖数据目录的优先级保持最高；它只改变数据目录，不改变渠道的其他身份。
- 同一构建产出的 bundle 内 `agrctl` 与主程序属于同一渠道。

## Updates

前提：应用启动后自动检查或用户手动检查更新。

- **正式版** 只看 GitHub 的 latest release（不含预发布与 `nightly`），与现状一致，包括镜像回落。
- **Beta** 只看 tag 形如 `vX.Y.Z-beta.N` 的非 draft 预发布，取其中最新的一个；没有这样的发布时结果为「暂无更新」，不回落到其他渠道。
- **Nightly** 只看 `nightly` release，版本比较沿用现有 nightly 规则。
- **Dev** 不发起任何更新检查，包括启动、聚焦窗口和手动触发。
- 选包：在选中的 release 里，只接受名字以本渠道发布包前缀开头、紧跟版本号、再接本机 `<goos>-<goarch>` 的桌面端安装包；`agentred-` 包与其他渠道的包永远不会被选中。release 里没有匹配的包时报告「该版本没有适用于本平台的安装包」，不下载任何东西。
- macOS 安装时从 dmg / zip 中取本渠道的 bundle（例如 Agentre Beta 取 `Agentre Beta.app`）替换当前运行的 bundle；取不到时安装失败并保留旧应用。
- Linux 从 deb / tar.gz 中取本渠道的命令名；Windows 静默运行本渠道安装器到当前安装目录。失败回滚行为保持现状。
- 下载镜像设置保留；校验和校验保持现状。

## Settings UI

- 设置 →「版本与更新」不再有更新通道选择器，也没有保存通道的操作。
- 当前版本号旁：Beta 显示「Beta」标签，Nightly 显示「Nightly」标签，Dev 显示「Dev」标签，正式版不显示标签。新增文案走 i18n（zh-CN / en）。
- Dev 中不出现「检查更新」入口与更新提示。
- 数据库里已存在的 `update.channel` 值被忽略，不再读取也不再写入。项目未发布，不做迁移或兼容提示。

## Remote agentred upgrade

- 前提：桌面端已配对远端 agentred。在正式版 / Beta / Nightly 中，用户在设备页触发一键升级时，发给 daemon 的渠道等于桌面端的渠道；前端不再传入或选择渠道。
- 在 Dev 中，设备页不出现一键升级入口；若仍收到升级调用则拒绝，不向 daemon 发送升级请求。远端 agentred 的版本信息照常显示。
- `agentred update --channel` 命令行行为不变。

## Local build and packaging

- `make dev`、`make build`、`make install`、`make run` 接受 `CHANNEL=stable|beta|nightly|dev`，不传时为 `dev`；产出并安装上表对应身份的应用。本地安装正式版需写 `make install CHANNEL=stable`。
- `CHANNEL` 取其他值时构建前失败并列出允许的取值。
- 本地打包与 CI 使用同一份渠道身份改写逻辑，避免两处各写一遍。
- 当前分支上的 `make build-beta` / `make install-beta` 被上述通用形式取代。

## CI pipelines

- `release.yml`：tag 为 `vX.Y.Z` 时全部平台按正式版（`stable`）出包并发布为 latest；tag 为 `vX.Y.Z-beta.N` 时全部平台按 Beta 出包、发布为预发布、不更新 agentred 容器镜像的 `latest`；其他形状的 tag（包括 `-rc`）在构建前失败，不创建 release。
- `nightly.yml`：全部平台按 Nightly 出包，发布到 `nightly` release。
- `manual-build.yml`：全部平台按 Dev 出包，只上传构建产物，不创建 release。
- agentred 的二进制、归档名与容器镜像在各渠道保持现状。

## Failure and compatibility

- 已发布的旧 beta / nightly 包没有渠道前缀，新 Beta / Nightly 应用不会选中它们，显示「暂无更新」或「没有适用于本平台的安装包」。
- 旧版应用（运行时通道设为 beta/nightly 的）不在本规格控制范围内，其行为由旧代码决定。
- 钥匙串分槽后，`agentre-beta` 与 `agentre-dev` 中已有数据的后端凭据与同步登录不再可用，需要在各自渠道中重新填写和登录。正式版不受影响。
- 本地以前用 `make install` 装出的 `/Applications/Agentre.app` 不受影响；之后不带 `CHANNEL` 的 `make install` 装出的是独立的 `Agentre Dev.app`。

## Out of scope

- 在渠道之间迁移或复制数据。
- agentred 的渠道身份。
- 各渠道独立的应用图标。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `paths` 包单测 | 标记解析（四个取值、不打标记即 Dev、非法标记报错）与渠道 → 数据目录、覆盖优先 | `internal/pkg/paths/paths_test.go` |
| 渠道身份单测 | 渠道 → 显示名、bundle id、钥匙串 service、发布包前缀、Linux 命令名 | none |
| 启动路径单测 | 非法渠道标记时启动失败并给出原因 | `internal/bootstrap/cago_test.go` |
| `keychain` 单测 | 系统钥匙串按渠道使用 service 名 | none |
| `update_svc` 单测 | 各渠道 release 选择（Beta 不回落、Nightly 只看 nightly、Dev 不检查）、按前缀精确选包（不选 agentred 与他渠道包）、macOS bundle 名 | `internal/service/update_svc/*_test.go` |
| `remote_device_svc` / Wails 绑定单测 | 一键升级发出的渠道等于桌面端渠道；Dev 拒绝升级且不发请求 | `internal/service/remote_device_svc` 现有 upgrade 测试 |
| 前端 Vitest | 版本与更新区域无通道选择器；Beta/Nightly/Dev 显示标签、正式版不显示；Dev 无检查更新与远端一键升级入口；i18n 覆盖 | `frontend/src/components/agentre/update-section.test.tsx`、`src/__tests__/i18n.test.ts` |
| 打包 / Info 模板 | 渠道身份改写逻辑对 macOS bundle 产出正确的名称与 bundle id；Makefile 拒绝非法 `CHANNEL` | `internal/desktop/darwin_bundle_test.go` |

无法自动化：CI 工作流只在真实推 tag / nightly / 手动触发时执行；Windows NSIS 与 Linux deb 的并排安装无法在本机验证。收尾时对 workflow 做源码审查，并在本机分别以 `CHANNEL=stable` 与默认 Dev 构建，检查 bundle 名称、bundle id 与渠道数据目录。

## Open questions
