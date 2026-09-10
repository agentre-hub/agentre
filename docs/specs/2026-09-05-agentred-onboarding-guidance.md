# agentred 接入引导：Docker 安装方式与文案收敛

> Status: Draft
> Owner: 桌面端 / 控制台前端
> Last updated: 2026-09-05

**Objective:** 让桌面端与控制台的 agentred 接入引导支持 Docker 安装，并把两端的引导文案收敛成同一份共享实现——界面只给要执行的命令。

**Hard invariant:**

- 两端的步骤顺序不变。控制台是「装+登录 → 批准 → 常驻」，桌面端是「装 → 常驻 → 配对」；这不是排版偏好，是 daemon 状态时序钉死的（见下文「步骤顺序的既有约束」）。
- 引导里出现的每一条命令都必须是当前 CLI / 镜像真实接受的形式。
- 跨仓交付顺序不可颠倒：共享实现先在 `agentre` 落地并推送，`agentre-server` 才能 pin 过去；消费方的现有实现不得先于可解析的共享版本被删除。
- 工作区内始终只有一份共享实现，两端不得各留一份手抄。

## Problem

1. **Docker 部署能力成熟，但两端引导都不知道它存在。** `deploy/Dockerfile`、`deploy/docker-compose.yml`、`deploy/README.md` 已经就位，`release.yml:13` 把多架构镜像推到 `ghcr.io/agentre-hub/agentred`（`latest` / `nightly` / `sha-` 三类 tag）。两端引导只给原生二进制的安装命令，容器用户在界面里得不到任何指引。

2. **安装命令是两份手抄的常量。** `agentre-server/frontend/src/components/AddDeviceGuide.tsx` 自己写着「与桌面端引导给的是同一条命令；改这里之前先确认那边也改了」。两端还各有一份 `CommandCard`（桌面端 `remote-devices/command-card.tsx` 走 `copyTextWithToast`，控制台是文件内联的实现走 `copyTextToClipboard`）和一份三格步骤条，语义一致、实现不同。

3. **两端引导的文案密度不对等，且大量内容在复述终端输出。** 控制台第 1 步有四条 tip 逐句描述 `agentred login` 会打印什么（`cmd/agentred/login.go:167-170` 就是那几行的来源）；桌面端同一件事只有一句话。诊断清单里的 `loginctl enable-linger` 同样是 `cmd/agentred/service_systemd.go:38-42` 已经会印出来的修复命令。

4. **控制台缺少安装自证。** 它把安装与登录挤在同一步，中间没有 `agentred --version`；桌面端有这一格。

## Actors and user stories

1. 作为在远端 Linux 机器上部署 agentred 的用户，我希望引导直接给出容器方式的命令，这样我不必先离开界面去翻部署文档才能开始。
2. 作为改动安装命令或镜像名的维护者，我希望只改一处，这样两端不会漂移。
3. 作为第一次接入的用户，我希望每一屏只告诉我该执行什么，这样我不必在一屏说明里找出哪句是动作。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | Docker 作为两端第一等的「安装方式」，与原生二进制并列 | 用户决定。镜像与 compose 已发布且被 dev 环境实际使用。Rejected: 只在两端各加一条指向 `deploy/README.md` 的链接 —— 容器用户拿不到「服务器地址 = 当前控制台」这条只有控制台知道的信息 |
| 2 | 引导只给要执行的命令；命令自己会印的内容一律不在界面复述 | 用户决定（两轮削减）。`install.sh` 会印安装路径与 PATH 提示、`service install` 会印 `Run: loginctl enable-linger <user>`、`login` 会印 `User code:` 与授权 URL、`pair` 在无可播报地址时会印 `agentred run --host` 的补救。Rejected: 保留这些提示作为「失败出口」—— 与终端输出重复，且把动作埋进说明里 |
| 3 | 容器部署的挂载、端口、`network_mode` 交给页脚指向 `deploy/README.md` 的链接 | 同上。这些内容随部署形态变化，写进界面会与文档双份维护。Rejected: 在界面里列出必需挂载 |
| 4 | 引导组件、共享文案与命令常量迁入 `@agentre-hub/agentre-ui` | 用户决定。跨端同一产品概念的渲染归共享包，见 `docs/frontend.md`。Rejected: 只抽命令常量（组件仍两份）；完全不抽（现状，靠注释提醒同步） |
| 5 | `CommandCard` 统一为按钮内联「已复制」，去掉桌面端的 toast | 内联反馈在复制失败时可以不切换标签，命令仍留在页面上可手抄；控制台已有四个用例覆盖非安全上下文与 `execCommand` 失败。Rejected: 保留 toast —— 需要宿主装有同一个 toast 实例，且失败时无法如实表达 |
| 6 | 两端保持各自的步骤顺序与语义，只共享安装/常驻两段与外壳 | 两端的第 3 步是不同产品行为（局域网配对 vs 注册服务），强行合并只会变成一堆 `isDesktop` 分支 |

## 步骤顺序的既有约束

两端顺序不同，各自都由 daemon 的状态时序决定，本轮不改动：

- `agentred login` 通过 `requireNoRunningDaemon`（`cmd/agentred/login.go:102`）拒绝在 daemon 运行时登录，并且要一直阻塞到用户批准才把凭据落盘。daemon 抢在它退出前起来会持有未登录的状态并在下次写盘时覆盖掉这次登录。**登录必须早于服务启动。**
- `agentred pair` 走本地 IPC 向运行中的 daemon 取一次性码（`cmd/agentred/pair.go`）。**配对必须晚于服务启动。**

## 安装步骤（两端共用）

**前提**：用户打开引导第 1 步。**动作**：选择安装方式（原生二进制 / Docker），原生方式下再选目标系统。**结果**：界面给出该组合下的安装命令与一条自证命令，页脚给出对应的补充入口。

- 原生分支给两张命令卡：安装命令（Linux/macOS 用 `install.sh`，Windows 用 `install.ps1`）与 `agentred --version`。页脚链接指向 GitHub Releases。
- Docker 分支给两张命令卡：`docker pull <镜像>` 与 `docker run --rm <镜像> --version`。页脚链接指向 `deploy/README.md`。
- 系统选择器只在原生分支出现——容器方式下宿主系统不改变命令。
- **控制台此前没有自证命令卡，本轮补齐**，与桌面端一致。

失败行为由命令自身承担：安装脚本自行报告安装路径与 PATH 需求，界面不重复。

## 常驻步骤

**前提**：用户已完成安装。**动作**：选择运行方式并执行给出的命令。**结果**：agentred 在目标机器上持续运行。

- 桌面端保留「后台服务 / 前台临时」选择器，仅在原生分支出现。后台给 `service install --start` 与 `service status` / `service restart`；前台给 `agentred run`。
- Docker 分支不出现该选择器，给 `docker compose up -d` 与 `docker compose ps` / `docker compose logs -f agentred`。
- 控制台的这一步是第 3 步且没有运行方式选择器：原生给 `service install --start`，Docker 给 `docker compose up -d` 与同一组状态命令。
- 控制台这一步的正文保留唯一一句因果说明：必须等设备批准之后再做，否则这次登录会被覆盖。该事实没有任何命令输出会提示，且是「授权成功却永远连不上」这一症状的唯一解释。

## 登录与配对步骤（各自宿主拥有）

- 控制台第 1 步在安装命令之后给出登录命令，服务器地址取自当前控制台自身的 origin，不写死域名。Docker 分支的登录命令以 `docker run --rm -it` 形式执行，并挂载 daemon 状态目录。
- 桌面端第 3 步给出取配对码的命令（原生 `agentred pair`，Docker 经 `docker compose exec` 在容器内执行），随后是既有的配对表单（地址、配对码、设备名、TLS 信任）。
- 控制台第 2 步不变，继续复用既有的六格设备码输入并交接给既有授权确认屏；不新增第二份批准界面。

**已接受的后果**：容器以桥接网络运行时，`agentred pair` 播报的是容器网段地址（`internal/daemon/protorpc/server.go:115` 的 `AdvertiseURLs` 枚举的是本进程网卡），照抄会连不上。按决定 2、3，界面不解释这一点，说明留在 `deploy/README.md` 的「已知限制」一节。配对本身不校验来源地址（`internal/daemon/auth/auth.go:103` 只校验一次性码与限流），因此用户改填宿主局域网地址仍可完成配对。

## 共享包边界

`@agentre-hub/agentre-ui` 新增一个引导域，导出：命令常量与派生函数、`CommandCard`、三格步骤条外壳、安装段落、常驻段落，以及它们的中英文案（进 `agentreUi` 命名空间，用 `useUiTranslation`）。

留在各自宿主：桌面端的配对表单、TLS 信任对话框与 Wails 调用；控制台的 origin 取值、设备码输入、路由跳转、设备类型选择与桌面端下载分支。宿主通过 props 与插槽把这些接进共享段落，不在共享组件里做宿主分支。

## 文案删除清单

以下键与其渲染随本轮删除，对应测试同步调整（**不是**遗漏，是决定 2 的直接结果）：

- 桌面端：服务诊断清单三条、前台模式说明、复制成功 toast 文案。
- 控制台：登录步骤四条 tip、服务步骤两条 tip、第 2 步的交接说明句。
- 两端：若干把动作埋进说明的步骤正文，收敛为标题加至多一句。

控制台既有测试 `第 3 步 · 计算节点：注册后台服务，并说明为什么必须排在批准之后` 所锚定的行为**保留**，只是承载它的文案从 tip 列表移到步骤正文并压缩为一句。

## 无障碍与状态

步骤条三格均为按钮，可 Tab 可点，当前步骤以 `aria-current="step"` 表达，序号进入无障碍名称而非仅画在装饰徽标里。完成标记只在用户点过该步的推进按钮后出现，跳步不补勾。选择器以 `aria-pressed` 表达选中态，不依赖颜色。窄视口下步骤条与命令网格转为单列。

## Out of scope

- 不修改 `deploy/` 下的 `Dockerfile`、`docker-compose.yml` 或 `README.md`。
- 不修复容器内 `agentred pair` / `status` 播报网桥地址的缺陷。
- 桌面端引导不新增「两端登录同一账号服务器」这条接入路径；面板头部已有独立的登录入口。
- 不改动设备码授权确认屏、配对握手或 TLS 信任语义。
- 不改动 `install.sh` / `install.ps1` 的行为。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 共享包引导段落（渲染） | 切换安装方式与系统后，安装段落给出的命令与自证命令随之变化；Docker 分支不出现系统选择器 | 无 |
| 共享包 `CommandCard` | 复制交给剪贴板的是当前那条命令；换命令后不误报「已复制」；非安全上下文回退与回退失败时不谎报 | `agentre-server/frontend/src/__tests__/device-guide.test.tsx` 的四个剪贴板用例，迁入共享包 |
| 共享包步骤条 | 三格可点、当前步骤可识别、跳步不补勾、序号进入无障碍名称 | `agentred-onboarding.test.tsx`、`device-guide.test.tsx` 中对应用例 |
| 共享包 i18n guard | 新文案模块在中英两份里键集一致且被 barrel 合并 | `packages/agentre-ui/src/i18n/i18n.test.tsx` |
| 共享包 boundary guard | 引导域不引入宿主耦合，裸导入均已声明 | `packages/agentre-ui/src/boundary.test.ts` |
| 桌面端宿主 | 三步顺序与配对表单接线不变；Docker 分支第 3 步给出经容器执行的取码命令 | `agentred-onboarding.test.tsx` |
| 控制台宿主 | 顺序仍是登录 → 批准 → 常驻；两种安装方式下的登录命令都带当前控制台 origin，Docker 那条挂载状态目录；第 3 步仍说明为什么排在批准之后 | `device-guide.test.tsx` |

无法自动化的部分：镜像 tag 与 `deploy/README.md` 中记载的发布产物是否一致，靠改动时对照 `release.yml` 的人工核对；容器方式端到端能否真正配对/登录成功不在本轮自动化范围内。

## 交付顺序

1. `agentre`：新增共享引导域与其测试，桌面端切换到共享实现，删除桌面端重复实现，跑共享包套件与桌面端测试，提交并**推送**。
2. `agentre-server`：pin 到上一步推送的 revision，替换为共享导入并接上 server 侧适配，删除重复的 `CommandCard`、步骤条、安装常量与仅服务于该副本的文案与测试，验证后独立提交。

两个仓库各自提交，不合成一个提交。
