package ccoauth_test

import (
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestKeychainServiceName(t *testing.T) {
	Convey("KeychainServiceName 默认配置返回 'Claude Code-credentials'", t, func() {
		So(ccoauth.KeychainServiceName(""), ShouldEqual, "Claude Code-credentials")
	})

	Convey("KeychainServiceName 自定义 CLAUDE_CONFIG_DIR 加 sha256[:8] 后缀", t, func() {
		// 与 OMC keychain 服务名约定一致: 取原始 env 字符串(不展开 ~)的 sha256 前 8 位 hex
		got := ccoauth.KeychainServiceName("~/.claude-foo")
		So(got, ShouldStartWith, "Claude Code-credentials-")
		So(len(got), ShouldEqual, len("Claude Code-credentials-")+8)

		// 相同输入应产生相同输出(纯函数)
		So(ccoauth.KeychainServiceName("~/.claude-foo"), ShouldEqual, got)

		// 不同输入应产生不同输出
		other := ccoauth.KeychainServiceName("/different/path")
		So(other, ShouldNotEqual, got)
	})
}

// fakeKeychain 用于注入 Keychain 行为而不依赖 macOS security 二进制。
type fakeKeychain struct {
	entries map[string]string // key = "service\x00account"
}

func newFakeKeychain() *fakeKeychain {
	return &fakeKeychain{entries: map[string]string{}}
}
func (f *fakeKeychain) put(service, account, blob string) {
	f.entries[service+"\x00"+account] = blob
}
func (f *fakeKeychain) Get(service, account string) (string, error) {
	if v, ok := f.entries[service+"\x00"+account]; ok {
		return v, nil
	}
	return "", ccoauth.ErrNoCredentials
}

func TestReadKeychainCredentials(t *testing.T) {
	Convey("命中第一个候选 account 返回 credentials", t, func() {
		kc := newFakeKeychain()
		kc.put("Claude Code-credentials", "codfrm", `{"claudeAiOauth":{"accessToken":"tok-1"}}`)

		creds, err := ccoauth.ReadKeychainCredentials(kc, "Claude Code-credentials", []string{"codfrm", ""})
		So(err, ShouldBeNil)
		So(creds.AccessToken, ShouldEqual, "tok-1")
	})

	Convey("第一个候选缺失时回退第二个", t, func() {
		kc := newFakeKeychain()
		kc.put("Claude Code-credentials", "", `{"accessToken":"flat-tok"}`)

		creds, err := ccoauth.ReadKeychainCredentials(kc, "Claude Code-credentials", []string{"codfrm", ""})
		So(err, ShouldBeNil)
		So(creds.AccessToken, ShouldEqual, "flat-tok")
	})

	Convey("所有候选都缺失返回 ErrNoCredentials", t, func() {
		kc := newFakeKeychain()
		_, err := ccoauth.ReadKeychainCredentials(kc, "Claude Code-credentials", []string{"codfrm", ""})
		So(errors.Is(err, ccoauth.ErrNoCredentials), ShouldBeTrue)
	})

	Convey("命中条目但内容里没 accessToken 返回 ErrNoCredentials", t, func() {
		kc := newFakeKeychain()
		kc.put("Claude Code-credentials", "codfrm", `{"claudeAiOauth":{"refreshToken":"only"}}`)
		_, err := ccoauth.ReadKeychainCredentials(kc, "Claude Code-credentials", []string{"codfrm"})
		So(errors.Is(err, ccoauth.ErrNoCredentials), ShouldBeTrue)
	})
}
