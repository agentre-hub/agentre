package agrctlcred

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func sample() *Credentials {
	return &Credentials{
		ServerURL:        "https://server.example",
		AccountID:        7,
		Account:          "alice@example.com",
		DeviceID:         42,
		DeviceName:       "build-box",
		Fingerprint:      "sha256:abc",
		RefreshToken:     "refresh-1",
		RefreshExpiresAt: 2000,
		AccessToken:      "access-1",
		AccessExpiresAt:  1000,
	}
}

func TestSaveLoad_GivenCredentialsWhenSavedThenLoadReturnsThemAndTheFileIs0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet-created")
	if err := Save(dir, sample()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *sample() {
		t.Fatalf("Load = %+v, want %+v", got, sample())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(FilePath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("credentials file mode = %o, want 600", perm)
		}
	}
}

func TestSave_GivenAnExistingFileWhenOverwrittenThenNoTemporaryFileIsLeftBehind(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	next := sample()
	next.RefreshToken = "refresh-2"
	if err := Save(dir, next); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got.RefreshToken != "refresh-2" {
		t.Fatalf("Load = %+v, %v; want the rotated refresh token", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %v, want only %s", names, FileName)
	}
}

func TestLoad_GivenNoFileThenErrNotExist(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load on an empty dir = %v, want os.ErrNotExist", err)
	}
}

func TestLoad_GivenACorruptFileThenAnErrorThatIsNotErrNotExist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(FilePath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load on a corrupt file = %v, want a decode error", err)
	}
}

func TestDelete_GivenSavedCredentialsThenTheyAreGoneAndASecondDeleteIsNoError(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Load(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load after Delete = %v, want os.ErrNotExist", err)
	}
	if err := Delete(dir); err != nil {
		t.Fatalf("second Delete = %v, want nil", err)
	}
}

func TestExpired_GivenNoRefreshTokenThenTheLoginIsExpired(t *testing.T) {
	c := sample()
	if c.Expired() {
		t.Fatal("a login with a refresh token reported expired")
	}
	c.RefreshToken = ""
	if !c.Expired() {
		t.Fatal("a login without a refresh token did not report expired")
	}
}

func TestLock_GivenTwoHoldersThenTheSecondWaitsForTheFirst(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	var mu sync.Mutex
	order := []string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		u2, err := Lock(dir)
		if err != nil {
			t.Errorf("second Lock: %v", err)
			return
		}
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		u2()
	}()
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	order = append(order, "first releases")
	mu.Unlock()
	unlock()
	<-done
	if len(order) != 2 || order[0] != "first releases" {
		t.Fatalf("order = %v, want the second holder to wait for the first", order)
	}
}

func TestLock_GivenALockLeftByACrashedProcessThenItIsBrokenAfterItGoesStale(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(filepath.Join(dir, lockName), old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock over a stale lock: %v", err)
	}
	unlock()
}
