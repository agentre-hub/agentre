# 用容器跑 agentred

镜像里装了 agentred 本体和 claude / codex / pi 三个 CLI。**CLI 的二进制来自镜像，
CLI 的配置来自挂载**——升级 CLI 靠改 `Dockerfile` 里的版本重新发版，不靠改宿主。
同一个 commit 两次构建出的环境因此是一样的。

版本在 `Dockerfile` 顶部钉死，也可以用 build-arg 覆盖：

| CLI | 默认版本 | 装法 |
| --- | --- | --- |
| claude | 2.1.224 | 官方安装器（原生 ELF，**动态链接 glibc**，所以运行段不能用 alpine） |
| codex | 0.146.0 | npm `@openai/codex` |
| pi | 0.84.3 | npm `@earendil-works/pi-coding-agent` |

构建阶段会跑一次自证：三个 CLI 必须存在、可执行、版本等于钉的值，否则构建就红。
这是「版本可复现」这条承诺唯一守得住的地方。

## 现成镜像

不想自己构建就直接拉，`linux/amd64` 与 `linux/arm64` 都有，`docker pull` 自己选架构：

```bash
docker pull ghcr.io/agentre-hub/agentred:latest
docker pull ghcr.io/agentre-hub/agentred:nightly
```

| tag | 来自 | 是什么 |
| --- | --- | --- |
| `latest` | `release.yml`（推 `v*` tag） | 最新正式版；beta / rc 不动它 |
| `v1.2.0` | 同上 | 与 git tag 同名，要钉版本用这个 |
| `nightly` | `nightly.yml`（每天 UTC 18:00） | 最新一次 nightly 构建 |
| `nightly-20260904` | 同上 | 当天那一版，用于回退 |
| `sha-abc1234` | 两条都打 | 精确到 commit |

**镜像里的三个 CLI 版本由该次构建的 `Dockerfile` 钉死**，跟着镜像走：换 tag 就是同时
换了 agentred 和三个 CLI。要固定住一整套环境就钉 `sha-` 或版本 tag，别用 `latest`。

两条流水线都在 `ubuntu-latest` 与 `ubuntu-24.04-arm` 两个原生 runner 上各打一个架构再
合成 manifest list，不走 QEMU——构建阶段那三次 CLI 版本自证是要真跑起来的，模拟环境下
既慢又容易在那一步无谓地红。凭据只用内置的 `GITHUB_TOKEN`，不需要额外 secret；
GHCR 上新建的 package 默认 private，第一次推完要手动改成 public 才能匿名拉取。

## 起

```bash
cp .env.example .env      # 改里面的路径
docker compose up -d
docker compose logs -f agentred
```

`docker build -f deploy/Dockerfile -t agentre/agentred:latest .` 从源码构建（默认目标）。

## 挂载

挂载分两类，区别很重要：

**工作区必须容器内外同一个绝对路径。** 会话的 `cwd` 和后端的 `cli_path` 都是宿主
下发的绝对路径，容器里解析不到同一个位置的话，历史会话一个都打不开、后端起不来。
compose 里两侧用的是同一个 `${AGENTRED_WORKSPACE}` 变量，就是为了让这件事没法写错。

**配置目录只要挂到容器内 `/root` 下的对应位置即可**，宿主侧在哪都行。

| 容器内 | 默认宿主侧 | 装的是什么 |
| --- | --- | --- |
| `${AGENTRED_WORKSPACE}` | 同路径 | 所有会话的工作区 |
| `/root/.config/agentred` | `~/.config/agentred` | daemon 状态：账号 claim 凭据、会话与通知日志 |
| `/root/.config/agentre` | `~/.config/agentre` | Agent 工作目录与 pi 扩展物化目录 |
| `/root/.claude` `/root/.claude.json` | `~/.claude` `~/.claude.json` | Claude Code 配置；`projects/` 还是 UserAnchor 的来源 |
| `/root/.codex` | `~/.codex` | Codex 配置与 `sessions` / `session_index.jsonl` |
| `/root/.pi` | `~/.pi` | Pi 配置与 `agent/sessions` |
| `/root/.agents` | `~/.agents` | 技能目录 |
| `/root/.ssh` `/root/.gitconfig` | 同名 | 会话内 git 操作要用，只读挂 |

> **`/root/.config/agentred` 和 `/root/.config/agentre` 不是同一个目录**，分别对应
> `AGENTRED_DATA_DIR` 与 `AGENTRE_DATA_DIR`。只挂前一个会静默丢掉所有 agent 工作目录。

> **先把要挂的文件建出来。** `.claude.json` 和 `.gitconfig` 是文件不是目录；宿主上
> 不存在时 Docker 会替你建一个**同名目录**，之后 CLI 读配置就会拿到一个目录，报错
> 还看不出所以然。

## 已知限制

**`agentred pair` / `agentred status` 报的 LAN 地址不可用。** 通配绑定下 agentred 枚举
的是本进程的网卡，容器里取到的是桥接网段地址而不是宿主地址，直连局域网配对因此失效。
反向外拨到账号服务器那条主路径不受影响，桌面端与网页经中继照常找得到它。要 LAN 直连
就得改成 `network_mode: host`（只有 Linux 有），或者等分离绑定地址与广播地址的改动。

**claude 的用量查询会走文件回退。** `go-keyring` 在 Linux 上要 Secret Service，容器里
通常没有，会回落到 `$CLAUDE_CONFIG_DIR` 或 `~/.claude/.credentials.json`。只影响用量
查询，不影响会话主流程。

**设备名来自容器 hostname。** agentred 把 hostname 当设备名上报到账号服务器，而容器
的 hostname 默认是容器 ID，每次重建都变。compose 里用 `AGENTRED_HOSTNAME` 钉住，
否则账号的设备列表里这台机器每部署一次就换一个名字。

**拉不到 docker.io 的网络要自己指镜像源。** `Dockerfile` 里 `RUNTIME_IMAGE` 的默认值
是上游的 `node:24-bookworm-slim`；换源用 `--build-arg RUNTIME_IMAGE=<你的镜像源>`。

## dev 环境（coding.local）

推 `dev` 分支到 Gitea 就自动部署：runner 上 `make agentred-linux` 编出二进制送到目标机，
目标机用 `Dockerfile` 的 `prebuilt` 目标打镜像再 compose。三个 CLI 那几层由
`RUNTIME_IMAGE` 与版本 arg 决定，不变就全部命中 layer cache，每次部署只重打最后一层。

部署目录在机器上，不在仓库里：

```
/srv/agentred-dev/
  Dockerfile           流水线覆盖
  docker-compose.yml   流水线覆盖
  bin/agentred         流水线覆盖，同时是 docker build 的上下文
  .env                 机器本地
```

判据是容器内 `agentred status` 能应答本地 IPC，**且** `agentred --version` 的 commit
等于本次构建。只看容器 `Up` 不够——起不来时它会反复重启，某一秒抓到的也是 `Up`。
不自动回滚：坏状态应当可见。

### 一次性迁移（只做一次，不进流水线）

从 `systemd --user` 的裸进程切到容器，按顺序：

1. **备份两个数据目录**，切换失败要能原样退回：
   ```bash
   tar -czf /root/agentred-backup-$(date +%F).tar.gz \
     /root/.config/agentred /root/.config/agentre
   ```
2. **停掉并禁用现有单元**，否则它和容器会抢同一个 7456 端口和同一份状态：
   ```bash
   systemctl --user stop agentred && systemctl --user disable agentred
   ```
3. **建部署目录与 `.env`**：
   ```bash
   mkdir -p /srv/agentred-dev/bin
   cat > /srv/agentred-dev/.env <<'ENV'
   AGENTRED_IMAGE=agentre/agentred:dev
   AGENTRED_PULL_POLICY=never
   AGENTRED_WORKSPACE=/root/code
   AGENTRED_HOSTNAME=coding
   ENV
   ```
   `AGENTRED_PULL_POLICY=never` 是必须的：dev 的镜像是本机打的，registry 上没有，
   不设 compose 会去 docker.io 找一个不存在的镜像。
4. **确认要挂的文件都存在**（见上面那条 Docker 会建成目录的坑）：
   ```bash
   ls -la /root/.claude.json /root/.gitconfig
   ```
5. **把桌面端三个后端的 `cli_path` 改成 `/usr/local/bin/{claude,codex,pi}`。**
   宿主上它们在 `~/.local/bin` 和 nvm 目录下，容器里不在那个位置。
6. 推一次 `dev` 触发部署，然后**开一个真实会话确认**挂载齐全、路径解析得通——
   这件事只能靠真跑一次会话来确认。

第 2 步之后、第 6 步完成之前，agentred 是停的。重启 agentred 会中断进行中的会话，
这是每次部署的固有代价。
