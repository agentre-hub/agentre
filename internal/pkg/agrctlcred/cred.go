// Package agrctlcred 是 agrctl 账号登录态在磁盘上的唯一形态：一份 0600 的 JSON 文件，放在
// agrctl 解析出的数据目录（paths.AppDataDir，按构建渠道隔离）下。
//
// 它只管读写与跨进程互斥，不发任何网络请求——Device Flow、刷新与吊销在 ctlclient。
// 依赖只有标准库，所以 agrctl 与桌面端（代签时写入，DelegatedBy 非空）都能 import 它。
package agrctlcred

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileName 是凭据文件在数据目录下的文件名。
const FileName = "agrctl-credentials.json"

// lockName 是刷新时跨进程互斥用的锁文件名。
const lockName = "agrctl-credentials.lock"

// staleLockAge 之后锁文件视为持有者已崩溃，可以被打破。一次刷新是一个 HTTP 往返，远短于它。
const staleLockAge = 30 * time.Second

// lockWait 是等锁的上限；超过就报错而不是一直挂着。
const lockWait = 20 * time.Second

// Credentials 是本机 agrctl 作为账号下一台 `cli` 设备的登录态。
//
// RefreshToken 是一次性的：server 每次刷新都换出新的并吊销旧的，重放旧的会吊销整条链——
// 所以换到新的之后必须先 Save 再用，且刷新要在 Lock 之内做。
type Credentials struct {
	ServerURL string `json:"serverUrl"`
	AccountID int64  `json:"accountId"`
	// Account 是给人看的账号名（邮箱、显示名或 GitHub 登录名）。
	Account     string `json:"account"`
	DeviceID    int64  `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	Fingerprint string `json:"fingerprint"`
	// RefreshToken 为空表示这份登录已被 server 证实作废（吊销或过期）：文件保留，执行者
	// 仍是账号，命令据此提示重新登录，而不是悄悄换成本机桌面。
	RefreshToken     string `json:"refreshToken"`
	RefreshExpiresAt int64  `json:"refreshExpiresAt,omitempty"` // unix 秒；0 = 未知
	AccessToken      string `json:"accessToken,omitempty"`
	AccessExpiresAt  int64  `json:"accessExpiresAt,omitempty"` // unix 秒
	// DelegatedBy 非空表示这份凭据由本机桌面代签写入（值是代签的桌面）；手动 login 时为空。
	DelegatedBy string `json:"delegatedBy,omitempty"`
}

// Expired 报告这份登录是否已被证实作废（见 RefreshToken）。
func (c *Credentials) Expired() bool { return c.RefreshToken == "" }

// FilePath 是 dir 下凭据文件的完整路径。
func FilePath(dir string) string { return filepath.Join(dir, FileName) }

// Load 读出 dir 下的凭据；没有登录时返回的错误满足 errors.Is(err, os.ErrNotExist)。
func Load(dir string) (*Credentials, error) {
	raw, err := os.ReadFile(FilePath(dir))
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("read agrctl credentials %s: %w", FilePath(dir), err)
	}
	return &c, nil
}

// Save 原子地写入凭据：先写同目录下的 0600 临时文件，再改名覆盖。目录不存在时创建（0700）。
func Save(dir string, c *Credentials) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create agrctl data dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, FileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // 改名成功后这里是空操作
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	if err := os.Rename(tmpName, FilePath(dir)); err != nil {
		return fmt.Errorf("write agrctl credentials: %w", err)
	}
	return nil
}

// Delete 删除凭据；本来就没有时不算错。
func Delete(dir string) error {
	if err := os.Remove(FilePath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete agrctl credentials: %w", err)
	}
	return nil
}

// Lock 取 dir 上的跨进程互斥锁，交回释放函数。并发的 agrctl 进程刷新前都要先取它，
// 取到后重新 Load：别人可能刚刚换过 refresh token，拿旧的去刷会被判重放。
// 锁是一个 O_EXCL 创建的文件；持有者崩溃留下的锁超过 staleLockAge 后被打破。
func Lock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create agrctl data dir: %w", err)
	}
	path := filepath.Join(dir, lockName)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: 固定文件名。
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock agrctl credentials: %w", err)
		}
		if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock agrctl credentials: another agrctl is holding %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
