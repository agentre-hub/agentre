.PHONY: run dev build agrctl agentred agentred-package agentred-linux agentred-deploy agentred-deploy-restart generate test test-backend test-frontend test-wire-swift test-cover test-agentred-packaging lint lint-backend lint-frontend lint-fix lint-fix-backend lint-fix-frontend mock install install-deps clean check e2e e2e-app verify-up verify-status verify-down

APP_NAME := Agentre
VERSION ?= 0.1.0
# 注入二进制的版本号：去掉 tag 的 v 前缀之后的那一份，与 wails.json 的 productVersion
# 和前端 package.json 的 version 同形。凡传进来的 VERSION 是 tag 形式（v1.2.3），
# 注入的自动变成无前缀形式；VERSION 本身继续服务产物名（agentre-v1.2.3-*.dmg）。
# 剥前缀只写在这一处：调用方（人或者流水线）传 tag 就行，不必各自记着再剥一次。
APP_VERSION ?= $(VERSION:v%=%)
ifeq ($(OS),Windows_NT)
NULLDEV := NUL
UNAME_S := Windows_NT
WAILS ?= wails
EXE := .exe
else
NULLDEV := /dev/null
UNAME_S := $(shell uname -s 2>$(NULLDEV) || echo unknown)
WAILS ?= $(shell command -v wails 2>$(NULLDEV) || printf "%s/bin/wails" "$$(go env GOPATH)")
EXE :=
endif
COMMIT_ID := $(shell git rev-parse --short HEAD 2>$(NULLDEV) || echo unknown)
VERSION_PKG := github.com/cago-frame/cago/configs
BUILDINFO_PKG := github.com/agentre-hub/agentre/internal/buildinfo
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(APP_VERSION) -X $(BUILDINFO_PKG).CommitID=$(COMMIT_ID)
# 构建渠道：dev / build / run / install / agrctl 接受 CHANNEL=stable|beta|nightly|dev，不传为 dev。
# 渠道决定数据目录、钥匙串槽位、窗口标题与安装身份（bundle 名/id、Linux 命令名、Windows 产品名），
# 四个渠道可并排安装；本地装正式版要写 CHANNEL=stable。非法取值在展开 recipe 时报错，什么都不会构建。
# 身份取值只在 paths.Channel.Identity：这里经 scripts/channel-identity.sh（CI 共用）读取，不另抄一份。
PATHS_PKG := github.com/agentre-hub/agentre/internal/pkg/paths
CHANNELS := stable beta nightly dev
CHANNEL ?= dev
BUILD_CHANNEL = $(if $(and $(filter 1,$(words $(CHANNEL))),$(filter $(CHANNELS),$(CHANNEL))),$(strip $(CHANNEL)),$(error CHANNEL=$(CHANNEL) 无效，允许的取值：$(CHANNELS)))
CHANNEL_LDFLAGS = -X $(PATHS_PKG).buildChannel=$(BUILD_CHANNEL)
APP_LDFLAGS = $(LDFLAGS) $(CHANNEL_LDFLAGS)
CHANNEL_IDENTITY := scripts/channel-identity.sh
channel_identity = $(or $(shell bash $(CHANNEL_IDENTITY) $(BUILD_CHANNEL) get $(1)),$(error 无法读取渠道 $(BUILD_CHANNEL) 的 $(1)（需要 go）))
CHANNEL_DISPLAY_NAME = $(call channel_identity,display-name)
CHANNEL_LINUX_COMMAND = $(call channel_identity,linux-command)
FRONTEND_DIR := frontend
BACKEND_PKGS := . ./cmd/... ./e2e/... ./internal/... ./migrations ./pkg/...
E2E_SPEC ?=
E2E_APP_BINARY := build/bin/agentre-e2e$(EXE)

MACOS_APP_INSTALL_DIR ?= /Applications
PREFIX ?= /usr/local
WAILS_PLATFORM ?=
WAILS_BUILD_FLAGS ?=
AGENTRED_BUILD_DIR ?= build/bin
AGENTRED_DIST_DIR ?= build/agentred-dist
AGRCTL_BINARY := $(AGENTRED_BUILD_DIR)/agrctl$(EXE)
AGENTRED_LOCAL_BINARY := $(AGENTRED_BUILD_DIR)/agentred
AGENTRED_GOOS ?= linux
AGENTRED_GOARCH ?= amd64
AGENTRED_PACKAGE_NAME := agentred-$(VERSION)-$(AGENTRED_GOOS)-$(AGENTRED_GOARCH)
AGENTRED_LINUX_BINARY := $(AGENTRED_BUILD_DIR)/agentred-$(AGENTRED_GOOS)-$(AGENTRED_GOARCH)
# 部署目标由调用方点名(opsctl 里的资产名),仓库里不留任何具体主机。
AGENTRED_TARGET ?=
AGENTRED_REMOTE_PATH ?= /usr/local/bin/agentred
AGENTRED_REMOTE_TMP ?= /tmp/agentred.$(COMMIT_ID)
AGENTRED_RUN_ARGS ?= run
AGENTRED_LOG_PATH ?= /tmp/agentred.log
AGENTRED_RESTART_CMD ?= pkill -x agentred || true; sleep 1; nohup $(AGENTRED_REMOTE_PATH) $(AGENTRED_RUN_ARGS) >$(AGENTRED_LOG_PATH) 2>&1 </dev/null & sleep 1; $(AGENTRED_REMOTE_PATH) status >/dev/null

# 开发模式(前后端热重载)。CHANNEL 注入渠道标记：数据目录、钥匙串与窗口标题随之切换。
# macOS 上 wails dev 用 build/darwin/Info.dev.plist 生成临时 bundle：会话期间把其中的
# bundle 名称与 id 改成渠道身份（id 决定 WKWebView 存储，否则不同渠道共用 localStorage），
# 退出（含 Ctrl-C）时还原；CHANNEL=dev 时改写结果与原文件逐字节相同。
DEV_PLIST := build/darwin/Info.dev.plist
dev:
	@mkdir -p $(FRONTEND_DIR)/dist && [ -e $(FRONTEND_DIR)/dist/.keep ] || touch $(FRONTEND_DIR)/dist/.keep
	@backup="$$(mktemp)" && cp $(DEV_PLIST) "$$backup" && \
	trap 'if [ -f "$$backup" ]; then cp "$$backup" $(DEV_PLIST); rm -f "$$backup"; fi' EXIT INT TERM HUP && \
	bash $(CHANNEL_IDENTITY) $(BUILD_CHANNEL) dev-plist $(DEV_PLIST) && \
	"$(WAILS)" dev -ldflags "$(CHANNEL_LDFLAGS)"

# 构建生产版本(默认当前平台；可用 WAILS_PLATFORM 跨平台构建)。
# wails build 之后把 agrctl 伴随 CLI 放进最终位置：mac 进 .app bundle(随 ditto 安装带走)，
# win/linux 与主二进制同目录。app 启动时从这里拷到 <AppDataDir>/bin 并把 PostToolUse hook 指向它。
# 注：跨平台构建(WAILS_PLATFORM)时 agrctl 仍按宿主工具链编译，跨平台打包为 follow-up。
# 渠道身份：wails.json 的 productName 在构建期间临时改成渠道显示名（Windows 产品名、NSIS
# 安装目录/卸载项/快捷方式都取它），无论构建成败还是被 Ctrl-C 中断都还原（dash 被信号杀死时
# 不跑 EXIT trap，所以与 dev 一样同时 trap INT/TERM/HUP）；mac 上构建后把 bundle 改名为
# "<显示名>.app"、改 bundle id 并重新 ad-hoc 签名。
build:
	@backup="$$(mktemp)" && cp wails.json "$$backup" && \
	trap 'if [ -f "$$backup" ]; then cp "$$backup" wails.json; rm -f "$$backup"; fi' EXIT INT TERM HUP && \
	bash $(CHANNEL_IDENTITY) $(BUILD_CHANNEL) wails-json wails.json && \
	"$(WAILS)" build -ldflags="$(APP_LDFLAGS)" $(if $(strip $(WAILS_PLATFORM)),-platform "$(WAILS_PLATFORM)") $(WAILS_BUILD_FLAGS)
ifeq ($(UNAME_S),Darwin)
	go build -ldflags="$(APP_LDFLAGS)" -o "build/bin/$(APP_NAME).app/Contents/MacOS/agrctl" ./cmd/agrctl
	bash $(CHANNEL_IDENTITY) $(BUILD_CHANNEL) macos-bundle "build/bin/$(APP_NAME).app"
else
	go build -ldflags="$(APP_LDFLAGS)" -o "build/bin/agrctl$(EXE)" ./cmd/agrctl
endif

# 构建 agrctl 伴随 CLI(当前平台，独立产物，供 dev/手动)
agrctl:
	mkdir -p "$(AGENTRED_BUILD_DIR)"
	go build -ldflags="$(APP_LDFLAGS)" -o "$(AGRCTL_BINARY)" ./cmd/agrctl

# 构建 agentred(当前平台)
agentred:
	mkdir -p "$(AGENTRED_BUILD_DIR)"
	go build -ldflags="$(LDFLAGS)" -o "$(AGENTRED_LOCAL_BINARY)" ./cmd/agentred

# 构建可发布的 agentred 跨平台归档（darwin/linux: tar.gz；windows: zip）。
agentred-package:
	rm -rf "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)"
	mkdir -p "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)"
ifeq ($(AGENTRED_GOOS),windows)
	CGO_ENABLED=0 GOOS=$(AGENTRED_GOOS) GOARCH=$(AGENTRED_GOARCH) go build -ldflags="$(LDFLAGS)" -o "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)/agentred.exe" ./cmd/agentred
	cd "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)" && zip -q "../$(AGENTRED_PACKAGE_NAME).zip" agentred.exe
else
	CGO_ENABLED=0 GOOS=$(AGENTRED_GOOS) GOARCH=$(AGENTRED_GOARCH) go build -ldflags="$(LDFLAGS)" -o "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)/agentred" ./cmd/agentred
	tar -C "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)" -czf "$(AGENTRED_DIST_DIR)/$(AGENTRED_PACKAGE_NAME).tar.gz" agentred
endif
	rm -rf "$(AGENTRED_DIST_DIR)/.$(AGENTRED_PACKAGE_NAME)"

# 构建 agentred Linux 版本(默认 linux/amd64，可覆盖 AGENTRED_GOOS/AGENTRED_GOARCH)
agentred-linux:
	mkdir -p "$(AGENTRED_BUILD_DIR)"
	GOOS=$(AGENTRED_GOOS) GOARCH=$(AGENTRED_GOARCH) CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o "$(AGENTRED_LINUX_BINARY)" ./cmd/agentred

# 通过 opsctl 部署 agentred 到远端(AGENTRED_TARGET=<资产名>,装到 AGENTRED_REMOTE_PATH)。
# opsctl 是维护者自己的运维 CLI,不在本仓库 Prerequisites 里,这两个 target 只服务维护者。
agentred-deploy: agentred-linux
	$(if $(strip $(AGENTRED_TARGET)),,$(error AGENTRED_TARGET must name the opsctl asset to deploy to))
	opsctl cp "$(AGENTRED_LINUX_BINARY)" "$(AGENTRED_TARGET):$(AGENTRED_REMOTE_TMP)"
	opsctl exec "$(AGENTRED_TARGET)" -- "install -Dm755 $(AGENTRED_REMOTE_TMP) $(AGENTRED_REMOTE_PATH) && rm -f $(AGENTRED_REMOTE_TMP) && $(AGENTRED_REMOTE_PATH) --help >/dev/null"
	@echo "已部署 agentred 到 $(AGENTRED_TARGET):$(AGENTRED_REMOTE_PATH)"

# 部署 agentred 到远端后重启裸进程(默认后台执行 agentred run，可覆盖 AGENTRED_RESTART_CMD)
agentred-deploy-restart:
	$(if $(strip $(AGENTRED_RESTART_CMD)),,$(error AGENTRED_RESTART_CMD must not be empty))
	$(MAKE) agentred-deploy
	opsctl exec "$(AGENTRED_TARGET)" -- "$(AGENTRED_RESTART_CMD)"
	@echo "已重启 $(AGENTRED_TARGET) 上的 agentred ($(AGENTRED_RESTART_CMD))"

# 生成 Wails 前端绑定
generate:
	"$(WAILS)" generate module

# 直接启动应用(生产构建,不监听文件变动)
run: build
ifeq ($(UNAME_S),Darwin)
	open "build/bin/$(CHANNEL_DISPLAY_NAME).app"
else ifeq ($(OS),Windows_NT)
	./build/bin/$(APP_NAME).exe
else
	./build/bin/$(APP_NAME)
endif

# 安装前端依赖
install-deps:
	cd $(FRONTEND_DIR) && pnpm install

# 构建并安装应用到系统
install: build
ifeq ($(UNAME_S),Darwin)
	@if [ -w "$(MACOS_APP_INSTALL_DIR)" ]; then \
		mkdir -p "$(MACOS_APP_INSTALL_DIR)"; \
		ditto "build/bin/$(CHANNEL_DISPLAY_NAME).app" "$(MACOS_APP_INSTALL_DIR)/$(CHANNEL_DISPLAY_NAME).app"; \
	else \
		sudo mkdir -p "$(MACOS_APP_INSTALL_DIR)"; \
		sudo ditto "build/bin/$(CHANNEL_DISPLAY_NAME).app" "$(MACOS_APP_INSTALL_DIR)/$(CHANNEL_DISPLAY_NAME).app"; \
	fi
	@echo "已安装到 $(MACOS_APP_INSTALL_DIR)/$(CHANNEL_DISPLAY_NAME).app"
else ifeq ($(OS),Windows_NT)
	@echo "Windows 安装暂未自动化；请运行 make build 后复制 build/bin/$(APP_NAME).exe。"
	@exit 1
else
	install -Dm755 "build/bin/$(APP_NAME)" "$(DESTDIR)$(PREFIX)/lib/$(CHANNEL_LINUX_COMMAND)/$(CHANNEL_LINUX_COMMAND)"
	install -Dm755 "build/bin/agrctl" "$(DESTDIR)$(PREFIX)/lib/$(CHANNEL_LINUX_COMMAND)/agrctl"
	mkdir -p "$(DESTDIR)$(PREFIX)/bin"
	ln -sfn "../lib/$(CHANNEL_LINUX_COMMAND)/$(CHANNEL_LINUX_COMMAND)" "$(DESTDIR)$(PREFIX)/bin/$(CHANNEL_LINUX_COMMAND)"
	@echo "已安装到 $(DESTDIR)$(PREFIX)/bin/$(CHANNEL_LINUX_COMMAND)（agrctl 伴随 CLI 与主程序同在 lib/$(CHANNEL_LINUX_COMMAND)，各渠道互不覆盖）"
endif

# 运行前后端测试
test: test-backend test-frontend

# 运行后端测试
# pkg/wire 与 pkg/syncwire 都是独立 module（分别是 wire 协议生成代码与同步契约的唯一
# 来源），父 module 的 ./pkg/... 不会走进它们，因此各自单独跑一次，否则 wire 的
# descriptor 守卫与同步载荷守卫的共享向量永远不会被执行。
# pkg/wire 的那一行同时驱动 Swift 侧的跨语言对拍(goldenvectors 包在 PATH 上有
# swift 时会去跑 pkg/wire/swift 的测试,没有则跳过并说明),因此它在装了 Xcode 的
# 机器上比另外两行慢,首次还要 SwiftPM 解析一次依赖。
test-backend:
	go test $(BACKEND_PKGS)
	go test -C pkg/wire ./...
	go test -C pkg/syncwire ./...

# 只跑 wire 协议的 Swift 侧:金向量对拍 + 产物守卫。
# 中继上载的是二进制 protobuf,原生端直接消费 protobuf 结构,这一条因此是 iOS 端
# 与本仓之间唯一的契约检查点。
test-wire-swift:
	go test -C pkg/wire ./goldenvectors/ ./guard/ -count=1 -v

# 运行前端测试
# typecheck 与 vitest 一起跑：vitest 走 esbuild 只转译不查类型，而 tsc 此前只挂在
# build 上 —— 于是 make test / make lint 全绿、打包那一刻才红，类型闸形同虚设。
test-frontend: generate
	cd $(FRONTEND_DIR) && pnpm typecheck && pnpm test

# Build only the dedicated E2E composition root. Production build/package targets
# remain rooted at main.go and never import e2e/composition or fake runtimes.
e2e-app:
	mkdir -p "$(AGENTRED_BUILD_DIR)"
	go build -o "$(E2E_APP_BINARY)" ./e2e/app

# Unified hermetic desktop E2E: one runner/config, three serial smoke specs.
e2e:
	cd e2e && pnpm test

# Local real verification: launch the formal desktop main with checkout-scoped
# data/keychain/browser state, then drive it one action at a time with drive.mjs.
# External services and real agent CLIs are configured explicitly by the verifier;
# an unavailable dependency fails or remains unverified rather than using a fake.
#   make verify-up
#   make verify-up VERIFY_FLAGS=--headed
verify-up:
	cd e2e && node verify.mjs up $(VERIFY_FLAGS)

verify-status:
	cd e2e && node verify.mjs status

# State is retained for investigation; VERIFY_FLAGS=--wipe removes only the
# checkout-scoped directories validated by the launcher.
verify-down:
	cd e2e && node verify.mjs down $(VERIFY_FLAGS)

# 测试覆盖率
test-cover:
	go test -coverprofile=coverage.out $(BACKEND_PKGS)
	go tool cover -html=coverage.out -o coverage.html
	@echo "覆盖率报告已生成: coverage.html"

# 发布资产与安装脚本的聚焦测试。
test-agentred-packaging:
	bash scripts/test-install.sh
	@if command -v pwsh >/dev/null 2>&1; then pwsh -NoProfile -File scripts/test-install.ps1; else echo "本机没有 pwsh，跳过了 install.ps1（要验它就找一台 Windows 手动跑这个目标）"; fi

# 前后端代码检查
lint: lint-backend lint-frontend

# 后端代码检查
lint-backend:
	golangci-lint run --timeout 10m

# 前端代码检查
lint-frontend: generate
	cd $(FRONTEND_DIR) && pnpm lint

# 前后端代码检查并自动修复
lint-fix: lint-fix-backend lint-fix-frontend

# 后端代码检查并自动修复
lint-fix-backend:
	golangci-lint run --timeout 10m --fix

# 前端代码检查并自动修复
lint-fix-frontend: generate
	cd $(FRONTEND_DIR) && pnpm lint:fix

# 本地完整检查
check: lint test

# 生成 mock(go.uber.org/mock)
mock:
	go generate ./...

# 清理构建产物
clean:
	rm -rf build/bin $(FRONTEND_DIR)/dist coverage.out coverage.html
