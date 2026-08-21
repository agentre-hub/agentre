package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CurrentSchemaVersion is the schemaVersion value written to state.json.
// Increment on any incompatible on-disk layout change.
const CurrentSchemaVersion = 1

const stateFileName = "state.json"

func newInstanceUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Load reads <dir>/state.json. When absent, a fresh default state is written and
// returned. When schemaVersion does not match CurrentSchemaVersion the call
// returns an error containing "schemaVersion" rather than silently migrating.
func Load(dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir state dir: %w", err)
	}
	path := filepath.Join(dir, stateFileName)
	b, err := os.ReadFile(path) //nolint:gosec // path is derived from caller-controlled data dir, not request input
	if errors.Is(err, os.ErrNotExist) {
		uuid, err := newInstanceUUID()
		if err != nil {
			return nil, err
		}
		st := NewDefault(uuid)
		st.dir = dir
		if err := st.Save(); err != nil {
			return nil, err
		}
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state.json: %w", err)
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	if st.SchemaVersion != CurrentSchemaVersion {
		return nil, fmt.Errorf(
			"state.json schemaVersion %d does not match expected %d; refusing to auto-migrate",
			st.SchemaVersion, CurrentSchemaVersion,
		)
	}
	if st.PairedPeers == nil {
		st.PairedPeers = map[string]PairedPeer{}
	}
	if st.LLMProviders == nil {
		st.LLMProviders = map[string]LLMProviderMeta{}
	}
	st.dir = dir
	st.mu = &sync.RWMutex{}
	return &st, nil
}

// Mutate calls fn while holding the write lock. fn must not panic (panics leave
// the mutex locked). Do not call Save inside fn.
func (s *State) Mutate(fn func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// Dir returns the directory that state was loaded from. Useful for co-locating
// sockets, certs, and log files next to state.json.
func (s *State) Dir() string { return s.dir }

// InstanceUUID returns the stable daemon identity UUID written to state.json
// on first boot. Safe for concurrent reads.
func (s *State) InstanceUUID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DaemonInstanceUUID
}

// IsClaimed reports whether this daemon belongs to an account.
func (s *State) IsClaimed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AccountID != ""
}

// Claim records the opaque account identity, the public key used to verify its
// credentials, and the refreshable credential obtained from the device flow.
func (s *State) Claim(accountID, verificationPublicKeyPEM string, credential AccountCredential) {
	s.Mutate(func(st *State) {
		st.AccountID = accountID
		st.VerificationPublicKeyPEM = verificationPublicKeyPEM
		st.Credential = credential
	})
}

// ClaimWithKeySet records the versioned verification-key set distributed by the
// account server. The legacy single-key field remains populated for downgrade
// compatibility with older agentred binaries reading the same state file.
func (s *State) ClaimWithKeySet(accountID, currentKID string, publicKeys map[string]string,
	maxTokenLifetimeSeconds int64, credential AccountCredential) {
	s.Mutate(func(st *State) {
		st.AccountID = accountID
		st.VerificationCurrentKID = currentKID
		st.VerificationPublicKeys = cloneStrings(publicKeys)
		st.VerificationPublicKeyPEM = publicKeys[currentKID]
		st.MaxTokenLifetimeSeconds = maxTokenLifetimeSeconds
		st.Credential = credential
	})
}

// Unclaim removes all account-bound material and returns the daemon to its
// pairing-only state. It is intentionally a state-only operation.
func (s *State) Unclaim() {
	s.Mutate(func(st *State) {
		st.AccountID = ""
		st.VerificationPublicKeyPEM = ""
		st.VerificationCurrentKID = ""
		st.VerificationPublicKeys = nil
		st.MaxTokenLifetimeSeconds = 0
		st.Credential = AccountCredential{}
		// The cached revocation list is pulled from the claimed account and
		// only ever consulted for that account's credentials, so it is part of
		// the claim: leaving it behind would keep one account's data on a
		// daemon that has returned to the unclaimed state (R19).
		st.RevokedJTIs = nil
		st.RevocationsAsOf = 0
	})
}

// AdoptClaimFromDisk re-reads state.json and adopts an account claim that
// appeared there while this in-memory state was still unclaimed. It reports
// whether a claim was adopted.
//
// This exists because `agentred login` is a separate process: it writes the
// credential to state.json and exits, so a daemon that was started before the
// login holds a stale in-memory copy and would otherwise stay unclaimed — and
// therefore off the relay — until the next restart.
//
// It is deliberately one-way and claim-only: an already-claimed state is left
// untouched (this process owns its own claim, including a refresh rotation it
// has not flushed yet), and nothing outside the claim is read back from disk.
func (s *State) AdoptClaimFromDisk() (bool, error) {
	if s.IsClaimed() {
		return false, nil
	}
	dir := s.Dir()
	if dir == "" {
		return false, errors.New("state: dir not bound; load via Load(dir) first")
	}
	// 直接读文件而不是走 Load：Load 在文件缺失时会写一份带**新** UUID 的默认状态，
	// 那会把这个 daemon 的身份换掉。这里只想看一眼盘上有没有认领，不该有任何写入。
	b, err := os.ReadFile(filepath.Join(dir, stateFileName)) //nolint:gosec // 与 Load 同一路径，来自数据目录而非请求输入
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read state.json: %w", err)
	}
	var onDisk State
	if err := json.Unmarshal(b, &onDisk); err != nil {
		return false, fmt.Errorf("parse state.json: %w", err)
	}
	if onDisk.SchemaVersion != CurrentSchemaVersion {
		return false, fmt.Errorf(
			"state.json schemaVersion %d does not match expected %d; refusing to adopt its claim",
			onDisk.SchemaVersion, CurrentSchemaVersion,
		)
	}
	// onDisk 是 Unmarshal 出来的，mu 为 nil —— 只能直接读字段，不能调它的方法。
	if onDisk.AccountID == "" {
		return false, nil
	}

	adopted := false
	s.Mutate(func(st *State) {
		// 再查一次：读盘这段时间里本进程可能已经自己完成了认领（比如 RPC 触发的
		// 登录），那份是更新的，不能被盘上这份盖掉。
		if st.AccountID != "" {
			return
		}
		st.AccountID = onDisk.AccountID
		st.HubServerURL = onDisk.HubServerURL
		st.Credential = onDisk.Credential
		st.VerificationPublicKeyPEM = onDisk.VerificationPublicKeyPEM
		st.VerificationCurrentKID = onDisk.VerificationCurrentKID
		st.VerificationPublicKeys = cloneStrings(onDisk.VerificationPublicKeys)
		st.MaxTokenLifetimeSeconds = onDisk.MaxTokenLifetimeSeconds
		st.RevokedJTIs = append([]string(nil), onDisk.RevokedJTIs...)
		st.RevocationsAsOf = onDisk.RevocationsAsOf
		adopted = true
	})
	return adopted, nil
}

// Snapshot returns a deep copy safe for read-only callers. The returned
// value's internal mutex is nil; calling Mutate or Save on a snapshot will
// panic by design (snapshots are read-only).
func (s *State) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := *s
	// Drop the lock pointer on the copy so the snapshot cannot share the
	// live state's mutex. Snapshots are strictly read-only data bags.
	out.mu = nil
	out.PairedPeers = make(map[string]PairedPeer, len(s.PairedPeers))
	for k, v := range s.PairedPeers {
		out.PairedPeers[k] = v
	}
	out.LLMProviders = cloneLLMProviders(s.LLMProviders)
	out.RevokedJTIs = append([]string(nil), s.RevokedJTIs...)
	out.VerificationPublicKeys = cloneStrings(s.VerificationPublicKeys)
	return out
}

func cloneLLMProviders(in map[string]LLMProviderMeta) map[string]LLMProviderMeta {
	out := make(map[string]LLMProviderMeta, len(in))
	for key, provider := range in {
		provider.Models = append([]LLMModelMeta(nil), provider.Models...)
		provider.ModelRoutes = cloneStrings(provider.ModelRoutes)
		out[key] = provider
	}
	return out
}

func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// ReplaceLLMProviders atomically replaces the complete provider snapshot in
// memory and on disk. If persistence fails, the live map is left untouched.
func (s *State) ReplaceLLMProviders(providers map[string]LLMProviderMeta) error {
	if s.dir == "" {
		return errors.New("state: dir not bound; load via Load(dir) first")
	}
	replacement := cloneLLMProviders(providers)
	s.mu.Lock()
	defer s.mu.Unlock()

	next := *s
	next.LLMProviders = replacement
	if err := writeStateFile(s.dir, &next); err != nil {
		return err
	}
	s.LLMProviders = replacement
	return nil
}

// Save writes state.json atomically via a tmp-file + rename. Permissions: 0o600.
func (s *State) Save() error {
	if s.dir == "" {
		return errors.New("state: dir not bound; load via Load(dir) first")
	}
	// Save holds the write lock through rename so two whole-file writers cannot
	// reorder stale snapshots on disk. Callers must not invoke Save from Mutate.
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeStateFile(s.dir, s)
}

func writeStateFile(dir string, value *State) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, stateFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
