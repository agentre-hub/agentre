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
	})

	Convey("ReadFileCredentials 解析扁平结构（fallback）", t, func() {
		dir := t.TempDir()
		path := filepath.Join(dir, ".credentials.json")
		So(os.WriteFile(path, []byte(`{"accessToken":"flat-tok","expiresAt":42}`), 0o600), ShouldBeNil)

		creds, err := ccoauth.ReadFileCredentials(path)
		So(err, ShouldBeNil)
		So(creds.AccessToken, ShouldEqual, "flat-tok")
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
