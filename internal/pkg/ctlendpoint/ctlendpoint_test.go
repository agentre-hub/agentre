package ctlendpoint

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Endpoint{URL: "http://127.0.0.1:52401", Token: "secret-token"}
	if err := Write(dir, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestWriteRejectsEmptyURL(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Endpoint{URL: "  ", Token: "x"}); err == nil {
		t.Fatal("expected error for empty url, got nil")
	}
	if _, err := os.Stat(FilePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty-url Write must not create the file, stat err = %v", err)
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestWriteFilePerms0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file perms not meaningful on windows")
	}
	dir := t.TempDir()
	if err := Write(dir, Endpoint{URL: "http://127.0.0.1:1", Token: "t"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(FilePath(dir))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perms = %o, want 0600", perm)
	}
}

func TestFilePathUsesDataDir(t *testing.T) {
	if got, want := FilePath("/data"), filepath.Join("/data", FileName); got != want {
		t.Fatalf("FilePath = %q, want %q", got, want)
	}
}
