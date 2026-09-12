package ccoauth_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestReadFileCredentials(t *testing.T) {
	Convey("ReadFileCredentials 解析 claudeAiOauth 嵌套结构", t, func() {
		dir := t.TempDir()
		path := filepath.Join(dir, ".credentials.json")
		So(os.WriteFile(path, []byte(`{
			"claudeAiOauth": {
				"accessToken": "tok-abc",
				"refreshToken": "ref-xyz",
				"expiresAt": 9999999999000
			}
		}`), 0o600), ShouldBeNil)

		creds, err := ccoauth.ReadFileCredentials(path)
		So(err, ShouldBeNil)
		So(creds, ShouldNotBeNil)
		So(creds.AccessToken, ShouldEqual, "tok-abc")
		So(creds.RefreshToken, ShouldEqual, "ref-xyz")
		So(creds.ExpiresAtMs, ShouldEqual, int64(9999999999000))
	})

	Convey("ReadFileCredentials 解析扁平结构（fallback）", t, func() {
		dir := t.TempDir()
		path := filepath.Join(dir, ".credentials.json")
		So(os.WriteFile(path, []byte(`{"accessToken":"flat-tok","expiresAt":42}`), 0o600), ShouldBeNil)

		creds, err := ccoauth.ReadFileCredentials(path)
		So(err, ShouldBeNil)
		So(creds.AccessToken, ShouldEqual, "flat-tok")
		So(creds.ExpiresAtMs, ShouldEqual, int64(42))
	})

	Convey("ReadFileCredentials 在文件不存在时返回 ErrNoCredentials", t, func() {
		_, err := ccoauth.ReadFileCredentials(filepath.Join(t.TempDir(), "missing.json"))
		So(err, ShouldEqual, ccoauth.ErrNoCredentials)
	})

	Convey("ReadFileCredentials 在文件里没有 accessToken 时返回 ErrNoCredentials", t, func() {
		dir := t.TempDir()
		path := filepath.Join(dir, ".credentials.json")
		So(os.WriteFile(path, []byte(`{"claudeAiOauth":{"refreshToken":"only-ref"}}`), 0o600), ShouldBeNil)

		_, err := ccoauth.ReadFileCredentials(path)
		So(err, ShouldEqual, ccoauth.ErrNoCredentials)
	})

	Convey("ReadFileCredentials 在 JSON 损坏时返回非 nil 错误", t, func() {
		dir := t.TempDir()
		path := filepath.Join(dir, ".credentials.json")
		So(os.WriteFile(path, []byte(`broken`), 0o600), ShouldBeNil)

		_, err := ccoauth.ReadFileCredentials(path)
		So(err, ShouldNotBeNil)
		So(err, ShouldNotEqual, ccoauth.ErrNoCredentials)
	})
}

func TestCredentialsIsExpired(t *testing.T) {
	Convey("ExpiresAtMs 为 0 时 IsExpired 始终为 false（视为未知 / 永久）", t, func() {
		c := ccoauth.Credentials{AccessToken: "x", ExpiresAtMs: 0}
		So(c.IsExpired(1_000_000), ShouldBeFalse)
	})

	Convey("ExpiresAtMs 小于等于 nowMs 时 IsExpired 为 true", t, func() {
		c := ccoauth.Credentials{AccessToken: "x", ExpiresAtMs: 1_000}
		So(c.IsExpired(1_001), ShouldBeTrue)
		So(c.IsExpired(1_000), ShouldBeTrue)
	})

	Convey("ExpiresAtMs 大于 nowMs 时 IsExpired 为 false", t, func() {
		c := ccoauth.Credentials{AccessToken: "x", ExpiresAtMs: 2_000}
		So(c.IsExpired(1_000), ShouldBeFalse)
	})
}
