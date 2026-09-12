# agentred 容器镜像：daemon 本体 + claude / codex / pi 三个 CLI。
#
# 两个构建目标，共用同一份 CLI 版本钉——版本只在这个文件里写一次：
#
#   从源码构建(外部用户、也是默认目标)：
#     docker build -f deploy/Dockerfile -t agentre/agentred:latest .
#
#   拿现成二进制打(dev 流水线在目标机上用)：
#     docker build -f Dockerfile --target prebuilt -t agentre/agentred:dev bin
#
# CLI 的二进制来自镜像，CLI 的配置来自挂载(见 docker-compose.yml)。升级 CLI 靠改
# 这里的默认版本重新发版，不靠改宿主——同一个 commit 两次构建出的环境要一样。

ARG GO_IMAGE=golang:1.26-alpine
# 运行段必须是 glibc：claude 不是 npm 包，是动态链接 /lib64/ld-linux-x86-64.so.2
# 的原生 ELF，musl 的 alpine 上跑不起来。node 则是 codex/pi 的运行时。
ARG RUNTIME_IMAGE=node:24-bookworm-slim

# ── 运行底座：三个 CLI 与运行期依赖 ────────────────────────────────────
# 单独成段，让「从源码构建」和「拿现成二进制打」共用同一批层。版本 arg 不变时
# 目标机上这些层全部命中 layer cache，每次部署只重打最后那个 COPY。
FROM ${RUNTIME_IMAGE} AS runtime-base

# 版本钉死并暴露为 build-arg。取值是 coding.local 上的现状
ARG CLAUDE_VERSION=2.1.224
ARG CODEX_VERSION=0.146.0
ARG PI_VERSION=0.84.3

# git: workspacefs 运行期唯一的常规外部命令(status/diff/for-each-ref/check-ignore)
# ca-certificates: 反向外拨到账号服务器要校验 TLS
# curl: 装 claude 用，留着也便于容器内排障
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

# codex 与 pi 是 npm 包；node 镜像的全局前缀就是 /usr/local，bin 直接落到 /usr/local/bin
RUN npm install -g --no-fund --no-audit \
      @openai/codex@${CODEX_VERSION} \
      @earendil-works/pi-coding-agent@${PI_VERSION} \
 && npm cache clean --force

# claude 走官方安装器，它只往 $HOME 下装；把真正的二进制挪到规范路径，
# 再把 $HOME 里的残留清掉——/root/.claude 运行时是挂载点，镜像里不该有东西占位
RUN curl -fsSL https://claude.ai/install.sh | bash -s "${CLAUDE_VERSION}" \
 && mv "/root/.local/share/claude/versions/${CLAUDE_VERSION}" /usr/local/bin/claude \
 && chmod 755 /usr/local/bin/claude \
 && rm -rf /root/.local/share/claude /root/.local/bin/claude /root/.claude

# 三个 CLI 的自证。「版本可复现」这条承诺只有这里守得住，版本不符就让构建红掉
RUN set -eu; \
    got=$(claude --version); \
    case "$got" in "${CLAUDE_VERSION} "*) ;; *) echo "claude 版本不符: 期望 ${CLAUDE_VERSION}, 实际 '$got'" >&2; exit 1;; esac; \
    got=$(codex --version); \
    case "$got" in *"${CODEX_VERSION}"*) ;; *) echo "codex 版本不符: 期望 ${CODEX_VERSION}, 实际 '$got'" >&2; exit 1;; esac; \
    got=$(pi --version); \
    case "$got" in *"${PI_VERSION}"*) ;; *) echo "pi 版本不符: 期望 ${PI_VERSION}, 实际 '$got'" >&2; exit 1;; esac; \
    git --version >/dev/null

# HOME 定成 /root，让所有相对 ~ 的路径与宿主同语义——挂载是按绝对路径对齐的
ENV HOME=/root
WORKDIR /root
EXPOSE 7456
ENTRYPOINT ["/usr/local/bin/agentred"]
CMD ["run"]

# ── 拿现成二进制打 ─────────────────────────────────────────────────────
# 上下文里要有一个编好的 linux/amd64 agentred。BuildKit 只构建目标所依赖的段，
# 所以 --target prebuilt 时下面的 build 段根本不会跑，上下文也不需要源码。
FROM runtime-base AS prebuilt
COPY agentred /usr/local/bin/agentred

# ── 从源码构建 ─────────────────────────────────────────────────────────
FROM ${GO_IMAGE} AS build
ARG VERSION=dev
ARG COMMIT_ID=unknown
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
# pkg/wire 是嵌在本仓库里的独立模块，go.mod 用本地 replace 指过去，
# 所以它必须先于 go mod download 就位
COPY go.mod go.sum ./
COPY pkg/wire ./pkg/wire
RUN go mod download
COPY . .
# 与 Makefile 同一组 ldflags 符号，容器内 agentred --version 才能对回具体 commit。
# 符号路径写全，不用缩写形式
RUN CGO_ENABLED=0 go build -ldflags "-s -w \
      -X github.com/cago-frame/cago/configs.Version=${VERSION} \
      -X github.com/agentre-hub/agentre/internal/buildinfo.CommitID=${COMMIT_ID}" \
      -o /out/agentred ./cmd/agentred

# 放在最后 = 默认目标，`docker build .` 走的是完整的源码构建
FROM runtime-base AS full
COPY --from=build /out/agentred /usr/local/bin/agentred
