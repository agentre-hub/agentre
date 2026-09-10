package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 这三个守卫针对同一种失败形态：**同一个发布版本号在本仓散成好几处字面量，而它们
// 各自被不同的工具读取**。改版本号时漏掉一处不会有任何编译错误、构建也不会变红：
//
//   - Makefile 的 VERSION 进产物名与 -ldflags，
//   - wails.json 的 productVersion 进 .app bundle 的元数据，
//   - 两个 package.json 的 version 进 npm 包，
//   - workflow 从 git tag 取版本，只是流水线的第四处来源。
//
// 于是发布出去的 0.1.1 里，Finder 显示的仍是上一版号；或者本地 `make build` 与应用
// 商店以外的构建报两个不同的版本串，而两边的比较逻辑各自写着防御性 TrimPrefix，
// 谁都没发现它们其实不同形。

// releaseVersionLiterals 是「同一个发布版本号」在本仓的全部字面量来源。
//
// frontend/packages/agentre-wire 的 version 刻意不在其列：它是 **wire 协议版本**，
// 由 .proto 的 file option 拥有并与 PROTOCOL_VERSION 相等（其自测守着这件事），
// 与产品版本无关，不该跟着发布号一起改。
var releaseVersionLiterals = []struct {
	path    string
	pattern *regexp.Regexp
}{
	{"Makefile", regexp.MustCompile(`(?m)^VERSION\s*\?=\s*(\S+)\s*$`)},
	{"wails.json", regexp.MustCompile(`"productVersion"\s*:\s*"([^"]+)"`)},
	{"frontend/package.json", regexp.MustCompile(`(?m)^\s*"version"\s*:\s*"([^"]+)"`)},
	{"frontend/packages/agentre-ui/package.json", regexp.MustCompile(`(?m)^\s*"version"\s*:\s*"([^"]+)"`)},
}

// TestReleaseVersionLiteralsAgree 守住四处版本字面量同形。
func TestReleaseVersionLiteralsAgree(t *testing.T) {
	root := repositoryRoot(t)

	var want, wantSource string
	for _, literal := range releaseVersionLiterals {
		content, err := os.ReadFile(filepath.Join(root, literal.path)) //nolint:gosec // 守卫读取仓库内固定相对路径。
		if err != nil {
			t.Fatal(err)
		}
		match := literal.pattern.FindStringSubmatch(string(content))
		if match == nil {
			t.Fatalf("%s 里没找到版本字面量（模式 %s）；来源变了就该改这条守卫，而不是让它静默不匹配",
				literal.path, literal.pattern)
		}
		if want == "" {
			want, wantSource = match[1], literal.path
			continue
		}
		if match[1] != want {
			t.Errorf("%s 的版本是 %q，但 %s 是 %q；一次发布只能有一个版本号，"+
				"不一致的那一处会带着旧号发出去", literal.path, match[1], wantSource, want)
		}
	}

	if want == "" {
		t.Fatal("no version literal read; the guard would pass vacuously")
	}
}

// makefileVersionPkgAssignment 取出 Makefile 里 VERSION_PKG（-X 的符号所属包）的赋值。
var makefileVersionPkgAssignment = regexp.MustCompile(`(?m)^VERSION_PKG\s*:?=\s*(\S+)\s*$`)

// makefileLDFlagVersion 匹配 Makefile 里注入 configs.Version 的那一段取值。
var makefileLDFlagVersion = regexp.MustCompile(`-X\s+\$\(VERSION_PKG\)\.Version=(\S+)`)

// TestMakefileInjectsCanonicalAppVersion 守住本地构建与流水线构建报同一个版本串。
//
// tag 形如 v0.1.0（带前缀），而 wails.json / package.json 与本地 `make build` 都是
// 无前缀形式。注入二进制的必须是**去掉前缀**的那一份：带前缀的话同一份源码在
// 本地与发布产物里报两个串，每处读取方都得自己 TrimPrefix 兜着。
//
// 同时守住 -X 的符号路径仍然指向 cago 的 configs 包——指向一个不存在的符号时，
// linker 既不报错也不注入，产物里的版本会退回 cago 的内置缺省。
func TestMakefileInjectsCanonicalAppVersion(t *testing.T) {
	root := repositoryRoot(t)

	content, err := os.ReadFile(filepath.Join(root, "Makefile")) //nolint:gosec // 守卫读取仓库内固定相对路径。
	if err != nil {
		t.Fatal(err)
	}

	pkgMatch := makefileVersionPkgAssignment.FindStringSubmatch(string(content))
	if pkgMatch == nil {
		t.Fatal("Makefile 里没找到 VERSION_PKG 的赋值；守卫会静默不生效")
	}

	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}", "github.com/cago-frame/cago/configs")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list github.com/cago-frame/cago/configs: %v\n%s", err, out)
	}
	if want := strings.TrimSpace(string(out)); pkgMatch[1] != want {
		t.Errorf("Makefile 的 VERSION_PKG 是 %q，但真实 import path 是 %q；"+
			"linker 对未知 -X 符号既不报错也不注入，产物会退回 cago 的内置缺省版本", pkgMatch[1], want)
	}

	match := makefileLDFlagVersion.FindStringSubmatch(string(content))
	if match == nil {
		t.Fatal("Makefile 里没找到注入 $(VERSION_PKG).Version 的 -X；守卫会静默不生效")
	}
	if match[1] != "$(APP_VERSION)" {
		t.Errorf("Makefile 注入的版本取值是 %q，应为 $(APP_VERSION)：它是去掉 tag 的 v 前缀之后的"+
			"那一份，与 wails.json / 前端 package.json 同形", match[1])
	}

	// 缺省必须自己剥前缀：改成由调用方传 APP_VERSION 的话，`make build VERSION=v1.2.3`
	// 这类直接调用仍会注入带前缀的版本，剥前缀的规矩就只剩约定而没有承载物。
	if !strings.Contains(string(content), "APP_VERSION ?= $(VERSION:v%=%)") {
		t.Error("Makefile 的 APP_VERSION 缺省不是 $(VERSION:v%=%)；它是剥掉 tag 的 v 前缀的唯一一处，" +
			"本地构建与流水线因此才有同一个取值来源")
	}
}

// ldflagInjectedVersion 匹配 workflow 里注入 configs.Version 的那一段取值。
//
// 取值有两种写法：shell 变量（$APP_VERSION）与 workflow 表达式（${{ env.VERSION }}，
// 内部含空格，所以不能只吃到第一个空白）。
var ldflagInjectedVersion = regexp.MustCompile(`configs\.Version=(\$\{\{[^}]*\}\}|\S+)`)

// canonicalVersionEnv 是 workflow 为二进制版本做的归一：注入前去掉 tag 的 v 前缀。
//
// 两种写法都要认：跨步骤传递时写进 $GITHUB_ENV（不带引号），同一步内直接用则带引号。
var canonicalVersionEnv = regexp.MustCompile(`APP_VERSION="?\$\{VERSION#v\}"?`)

// TestReleaseWorkflowsInjectCanonicalAppVersion 守住每条注入了版本号的流水线都先做
// 归一，且注入的是归一后的那份。
//
// VERSION 在 workspace 里同时服务产物命名与镜像 tag（tag 原样，带 v），所以归一必须是
// 另开一个变量，而不是把 VERSION 本身改掉——后者会连带改掉发布资产的文件名。
func TestReleaseWorkflowsInjectCanonicalAppVersion(t *testing.T) {
	root := repositoryRoot(t)

	// shell 取值的两种写法：bash 的 $APP_VERSION 与 windows step 的 $env:APP_VERSION。
	allowed := map[string]bool{"$APP_VERSION": true, "$env:APP_VERSION": true}

	found := 0
	for _, path := range workflowFiles(t, root) {
		content, err := os.ReadFile(path) //nolint:gosec // 守卫读取仓库内枚举出的 workflow 定义。
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}

		matches := ldflagInjectedVersion.FindAllStringSubmatch(string(content), -1)
		injects := len(matches) > 0
		for _, match := range matches {
			found++
			if !allowed[match[1]] {
				t.Errorf("%s 注入的版本取值是 %q，应为 $APP_VERSION（bash）或 $env:APP_VERSION（windows）："+
					"直接注入带 v 的 tag 会让产物里的版本与 wails.json / 前端 package.json 不同形",
					rel, match[1])
			}
		}
		if injects && !canonicalVersionEnv.Match(content) {
			t.Errorf("%s 注入了 configs.Version，但找不到 APP_VERSION=${VERSION#v} 这一步归一；"+
				"没有它就没有可注入的无前缀版本", rel)
		}
	}

	// 自证不空过：一处注入都没找到时全绿是没有意义的。
	if found == 0 {
		t.Fatal("no configs.Version injection found in any workflow; the guard would pass vacuously")
	}
}
