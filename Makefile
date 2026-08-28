.PHONY: run dev build agrctl agentred agentred-package agentred-linux agentred-deploy agentred-deploy-restart agentred-deploy-local-coding agentred-local-coding generate test test-backend test-frontend test-cover test-agentred-packaging lint lint-backend lint-frontend lint-fix lint-fix-backend lint-fix-frontend mock install install-deps clean check e2e e2e-app verify-up verify-status verify-down

APP_NAME := Agentre
VERSION ?= 0.1.0
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
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(VERSION) -X $(BUILDINFO_PKG).CommitID=$(COMMIT_ID)
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
AGENTRED_TARGET ?= local-coding
AGENTRED_REMOTE_PATH ?= /usr/local/bin/agentred
AGENTRED_REMOTE_TMP ?= /tmp/agentred.$(COMMIT_ID)
AGENTRED_RUN_ARGS ?= run
AGENTRED_LOG_PATH ?= /tmp/agentred.log
AGENTRED_RESTART_CMD ?= pkill -x agentred || true; sleep 1; nohup $(AGENTRED_REMOTE_PATH) $(AGENTRED_RUN_ARGS) >$(AGENTRED_LOG_PATH) 2>&1 </dev/null & sleep 1; $(AGENTRED_REMOTE_PATH) status >/dev/null

# 开发模式(前后端热重载)
dev:
	@mkdir -p $(FRONTEND_DIR)/dist && [ -e $(FRONTEND_DIR)/dist/.keep ] || touch $(FRONTEND_DIR)/dist/.keep
	"$(WAILS)" dev

# 构建生产版本(默认当前平台；可用 WAILS_PLATFORM 跨平台构建)。
# wails build 之后把 agrctl 伴随 CLI 放进最终位置：mac 进 .app bundle(随 ditto 安装带走)，
# win/linux 与主二进制同目录。app 启动时从这里拷到 <AppDataDir>/bin 并把 PostToolUse hook 指向它。
# 注：跨平台构建(WAILS_PLATFORM)时 agrctl 仍按宿主工具链编译，跨平台打包为 follow-up。
build:
	"$(WAILS)" build -ldflags="$(LDFLAGS)" $(if $(strip $(WAILS_PLATFORM)),-platform "$(WAILS_PLATFORM)") $(WAILS_BUILD_FLAGS)
ifeq ($(UNAME_S),Darwin)
	go build -ldflags="$(LDFLAGS)" -o "build/bin/$(APP_NAME).app/Contents/MacOS/agrctl" ./cmd/agrctl
else
	go build -ldflags="$(LDFLAGS)" -o "build/bin/agrctl$(EXE)" ./cmd/agrctl
endif

# 构建 agrctl 伴随 CLI(当前平台，独立产物，供 dev/手动)
agrctl:
	mkdir -p "$(AGENTRED_BUILD_DIR)"
	go build -ldflags="$(LDFLAGS)" -o "$(AGRCTL_BINARY)" ./cmd/agrctl

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

# 通过 opsctl 部署 agentred 到远端(默认 local-coding:/usr/local/bin/agentred)
agentred-deploy: agentred-linux
	opsctl cp "$(AGENTRED_LINUX_BINARY)" "$(AGENTRED_TARGET):$(AGENTRED_REMOTE_TMP)"
	opsctl exec "$(AGENTRED_TARGET)" -- "install -Dm755 $(AGENTRED_REMOTE_TMP) $(AGENTRED_REMOTE_PATH) && rm -f $(AGENTRED_REMOTE_TMP) && $(AGENTRED_REMOTE_PATH) --help >/dev/null"
	@echo "已部署 agentred 到 $(AGENTRED_TARGET):$(AGENTRED_REMOTE_PATH)"

# 部署 agentred 到远端后重启裸进程(默认后台执行 agentred run，可覆盖 AGENTRED_RESTART_CMD)
agentred-deploy-restart:
	$(if $(strip $(AGENTRED_RESTART_CMD)),,$(error AGENTRED_RESTART_CMD must not be empty))
	$(MAKE) agentred-deploy
	opsctl exec "$(AGENTRED_TARGET)" -- "$(AGENTRED_RESTART_CMD)"
	@echo "已重启 $(AGENTRED_TARGET) 上的 agentred ($(AGENTRED_RESTART_CMD))"

agentred-deploy-local-coding:
	$(MAKE) agentred-deploy AGENTRED_TARGET=local-coding AGENTRED_GOOS=linux AGENTRED_GOARCH=amd64

agentred-local-coding: agentred-deploy-local-coding

# 生成 Wails 前端绑定
generate:
	"$(WAILS)" generate module

# 直接启动应用(生产构建,不监听文件变动)
run: build
ifeq ($(UNAME_S),Darwin)
	open build/bin/$(APP_NAME).app
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
		ditto "build/bin/$(APP_NAME).app" "$(MACOS_APP_INSTALL_DIR)/$(APP_NAME).app"; \
	else \
		sudo mkdir -p "$(MACOS_APP_INSTALL_DIR)"; \
		sudo ditto "build/bin/$(APP_NAME).app" "$(MACOS_APP_INSTALL_DIR)/$(APP_NAME).app"; \
	fi
	@echo "已安装到 $(MACOS_APP_INSTALL_DIR)/$(APP_NAME).app"
else ifeq ($(OS),Windows_NT)
	@echo "Windows 安装暂未自动化；请运行 make build 后复制 build/bin/$(APP_NAME).exe。"
	@exit 1
else
	install -Dm755 "build/bin/$(APP_NAME)" "$(DESTDIR)$(PREFIX)/bin/$(APP_NAME)"
	install -Dm755 "build/bin/agrctl" "$(DESTDIR)$(PREFIX)/bin/agrctl"
	@echo "已安装到 $(DESTDIR)$(PREFIX)/bin/$(APP_NAME)（含 agrctl 伴随 CLI）"
endif

# 运行前后端测试
test: test-backend test-frontend

# 运行后端测试
# pkg/wire 是独立 module（wire 协议生成代码的唯一来源），父 module 的 ./pkg/... 不会
# 走进它，因此单独跑一次，否则它的 descriptor 守卫永远不会被执行。
test-backend:
	go test $(BACKEND_PKGS)
	go test -C pkg/wire ./...

# 运行前端测试
test-frontend: generate
	cd $(FRONTEND_DIR) && pnpm test

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

# 发布资产、安装脚本与 workflow 契约的聚焦测试。
test-agentred-packaging:
	python3 scripts/test-release-workflows.py
	bash scripts/test-install.sh
	@if command -v pwsh >/dev/null 2>&1; then pwsh -NoProfile -File scripts/test-install.ps1; else echo "pwsh not found; install.ps1 runs on the Windows CI job"; fi

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
