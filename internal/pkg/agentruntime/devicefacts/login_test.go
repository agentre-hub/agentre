package devicefacts_test

import (
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// fakeFS 只回答「这个文件在不在」与 config.toml 的正文 —— 登录态检测从不读凭据文件。
type fakeFS struct {
	files   map[string]string
	statErr error
	read    []string
}

func (f *fakeFS) exists(path string) (bool, error) {
	if f.statErr != nil {
		return false, f.statErr
	}
	_, ok := f.files[path]
	return ok, nil
}

func (f *fakeFS) readFile(path string) ([]byte, error) {
	f.read = append(f.read, path)
	body, ok := f.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(body), nil
}

func loginProbe(fs *fakeFS, env map[string]string, keychain func(string) (bool, error)) devicefacts.LoginProbe {
	return devicefacts.LoginProbe{
		Home:     "/home/u",
		Getenv:   func(k string) string { return env[k] },
		Exists:   fs.exists,
		ReadFile: fs.readFile,
		Keychain: keychain,
	}
}

func TestLoginProbe_ClaudeCode(t *testing.T) {
	Convey("Claude Code 原生登录态", t, func() {
		Convey("Given ~/.claude/.credentials.json 在, Then 已登录", func() {
			fs := &fakeFS{files: map[string]string{"/home/u/.claude/.credentials.json": "{}"}}
			So(loginProbe(fs, nil, nil).ClaudeCode(), ShouldEqual, syncwire.NativeLoginLoggedIn)
			So(fs.read, ShouldBeEmpty) // 只看在不在,不读正文
		})
		Convey("Given CLAUDE_CONFIG_DIR 指向别处, Then 看那个目录", func() {
			fs := &fakeFS{files: map[string]string{"/cfg/.credentials.json": "{}"}}
			So(loginProbe(fs, map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}, nil).ClaudeCode(), ShouldEqual, syncwire.NativeLoginLoggedIn)
		})
		Convey("Given 没有凭据文件且平台没有钥匙串, Then 未登录", func() {
			So(loginProbe(&fakeFS{}, nil, nil).ClaudeCode(), ShouldEqual, syncwire.NativeLoginLoggedOut)
		})
		Convey("Given 没有凭据文件但钥匙串里有 CLI 的条目, Then 已登录,问的是 CLI 自己的服务名", func() {
			var asked string
			kc := func(service string) (bool, error) { asked = service; return true, nil }
			So(loginProbe(&fakeFS{}, nil, kc).ClaudeCode(), ShouldEqual, syncwire.NativeLoginLoggedIn)
			So(asked, ShouldEqual, "Claude Code-credentials")
		})
		Convey("Given 钥匙串里没有条目, Then 未登录", func() {
			kc := func(string) (bool, error) { return false, nil }
			So(loginProbe(&fakeFS{}, nil, kc).ClaudeCode(), ShouldEqual, syncwire.NativeLoginLoggedOut)
		})
		Convey("Given 钥匙串问不了(锁着 / 无会话), Then 未知", func() {
			kc := func(string) (bool, error) { return false, errors.New("interaction not allowed") }
			So(loginProbe(&fakeFS{}, nil, kc).ClaudeCode(), ShouldEqual, syncwire.NativeLoginUnknown)
		})
		Convey("Given 文件系统查不了, Then 未知", func() {
			So(loginProbe(&fakeFS{statErr: errors.New("permission denied")}, nil, nil).ClaudeCode(), ShouldEqual, syncwire.NativeLoginUnknown)
		})
		Convey("Given 找不到主目录, Then 未知", func() {
			p := loginProbe(&fakeFS{}, nil, nil)
			p.Home = ""
			So(p.ClaudeCode(), ShouldEqual, syncwire.NativeLoginUnknown)
		})
	})
}

func TestLoginProbe_Codex(t *testing.T) {
	Convey("Codex 原生登录态", t, func() {
		Convey("Given ~/.codex/auth.json 在, Then 已登录且不读它", func() {
			fs := &fakeFS{files: map[string]string{"/home/u/.codex/auth.json": "{}"}}
			So(loginProbe(fs, nil, nil).Codex(), ShouldEqual, syncwire.NativeLoginLoggedIn)
			So(fs.read, ShouldNotContain, "/home/u/.codex/auth.json")
		})
		Convey("Given CODEX_HOME 指向别处, Then 看那个目录", func() {
			fs := &fakeFS{files: map[string]string{"/ch/auth.json": "{}"}}
			So(loginProbe(fs, map[string]string{"CODEX_HOME": "/ch"}, nil).Codex(), ShouldEqual, syncwire.NativeLoginLoggedIn)
		})
		Convey("Given 没有 auth.json 也没把凭据放进系统钥匙串, Then 未登录", func() {
			fs := &fakeFS{files: map[string]string{"/home/u/.codex/config.toml": "model = \"gpt-5\"\n"}}
			So(loginProbe(fs, nil, nil).Codex(), ShouldEqual, syncwire.NativeLoginLoggedOut)
		})
		Convey("Given config.toml 把凭据放进系统钥匙串, Then 没有 auth.json 也只能说未知", func() {
			fs := &fakeFS{files: map[string]string{"/home/u/.codex/config.toml": "cli_auth_credentials_store = \"keyring\"\n"}}
			So(loginProbe(fs, nil, nil).Codex(), ShouldEqual, syncwire.NativeLoginUnknown)
		})
		Convey("Given config.toml 明说存文件, Then 没有 auth.json 就是未登录", func() {
			fs := &fakeFS{files: map[string]string{"/home/u/.codex/config.toml": "cli_auth_credentials_store = \"file\"\n"}}
			So(loginProbe(fs, nil, nil).Codex(), ShouldEqual, syncwire.NativeLoginLoggedOut)
		})
		Convey("Given 文件系统查不了, Then 未知", func() {
			So(loginProbe(&fakeFS{statErr: errors.New("io")}, nil, nil).Codex(), ShouldEqual, syncwire.NativeLoginUnknown)
		})
	})
}
