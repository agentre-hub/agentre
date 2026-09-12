package keychain_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

func TestMemory(t *testing.T) {
	Convey("memory keychain round-trips a secret", t, func() {
		k := keychain.NewMemory()
		So(k.Set("acc", "secret"), ShouldBeNil)
		v, err := k.Get("acc")
		So(err, ShouldBeNil)
		So(v, ShouldEqual, "secret")
	})

	Convey("Get on missing account returns ErrNotFound", t, func() {
		_, err := keychain.NewMemory().Get("missing")
		So(err, ShouldEqual, keychain.ErrNotFound)
	})

	Convey("Delete removes the secret; second Get returns ErrNotFound", t, func() {
		k := keychain.NewMemory()
		So(k.Set("acc", "s"), ShouldBeNil)
		So(k.Delete("acc"), ShouldBeNil)
		_, err := k.Get("acc")
		So(err, ShouldEqual, keychain.ErrNotFound)
	})

	Convey("SetDefault / Default round-trip", t, func() {
		k := keychain.NewMemory()
		keychain.SetDefault(k)
		So(keychain.Default(), ShouldEqual, k)
	})
}

func TestFile(t *testing.T) {
	Convey("file keychain round-trips a secret with 0600 perms", t, func() {
		dir := t.TempDir()
		k := keychain.NewFile(dir)
		So(k.Set("acc", "secret"), ShouldBeNil)

		info, err := os.Stat(filepath.Join(dir, "acc"))
		So(err, ShouldBeNil)
		So(info.Mode().Perm(), ShouldEqual, os.FileMode(0o600))

		v, err := k.Get("acc")
		So(err, ShouldBeNil)
		So(v, ShouldEqual, "secret")
	})

	Convey("overwriting an existing secret repairs its permissions to 0600", t, func() {
		dir := t.TempDir()
		secretPath := filepath.Join(dir, "acc")
		So(os.WriteFile(secretPath, []byte("old"), 0o644), ShouldBeNil)
		So(keychain.NewFile(dir).Set("acc", "secret"), ShouldBeNil)

		info, err := os.Stat(secretPath)
		So(err, ShouldBeNil)
		So(info.Mode().Perm(), ShouldEqual, os.FileMode(0o600))
	})

	Convey("Get on a missing account returns ErrNotFound", t, func() {
		dir := t.TempDir()
		_, err := keychain.NewFile(dir).Get("missing")
		So(err, ShouldEqual, keychain.ErrNotFound)
	})

	Convey("Delete removes the file; double Delete returns ErrNotFound", t, func() {
		dir := t.TempDir()
		k := keychain.NewFile(dir)
		So(k.Set("acc", "s"), ShouldBeNil)
		So(k.Delete("acc"), ShouldBeNil)
		So(k.Delete("acc"), ShouldEqual, keychain.ErrNotFound)
	})
}

func TestValidateFileDir(t *testing.T) {
	Convey("an existing 0700 directory passes", t, func() {
		dir := filepath.Join(t.TempDir(), "kc")
		So(os.MkdirAll(dir, 0o700), ShouldBeNil)
		So(keychain.ValidateFileDir(dir), ShouldBeNil)
	})

	Convey("a missing directory fails", t, func() {
		err := keychain.ValidateFileDir(filepath.Join(t.TempDir(), "does-not-exist"))
		So(err, ShouldNotBeNil)
	})

	Convey("a file path fails", t, func() {
		f := filepath.Join(t.TempDir(), "file")
		So(os.WriteFile(f, []byte("x"), 0o600), ShouldBeNil)
		err := keychain.ValidateFileDir(f)
		So(err, ShouldNotBeNil)
	})

	Convey("a group/other-accessible directory fails", t, func() {
		dir := filepath.Join(t.TempDir(), "kc")
		So(os.MkdirAll(dir, 0o755), ShouldBeNil)
		err := keychain.ValidateFileDir(dir)
		So(err, ShouldNotBeNil)
	})

	Convey("an existing probe-named file is preserved", t, func() {
		dir := filepath.Join(t.TempDir(), "kc")
		So(os.MkdirAll(dir, 0o700), ShouldBeNil)
		probe := filepath.Join(dir, ".keychain-probe")
		So(os.WriteFile(probe, []byte("existing"), 0o600), ShouldBeNil)

		err := keychain.ValidateFileDir(dir)
		So(err, ShouldBeNil)
		got, readErr := os.ReadFile(probe) //nolint:gosec // G304: probe is assembled under the test's temporary directory.
		So(readErr, ShouldBeNil)
		So(string(got), ShouldEqual, "existing")
	})

	Convey("a directory without owner write fails", t, func() {
		if os.Geteuid() == 0 {
			t.Skip("running as root; permission bits do not gate writes")
		}
		dir := filepath.Join(t.TempDir(), "kc")
		So(os.MkdirAll(dir, 0o700), ShouldBeNil)
		So(os.Chmod(dir, 0o500), ShouldBeNil)
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		err := keychain.ValidateFileDir(dir)
		So(err, ShouldNotBeNil)
	})
}
