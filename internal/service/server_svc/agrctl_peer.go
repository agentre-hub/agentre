package server_svc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 桌面代签：本桌面登录账号后，替本机 agrctl 向 server 申请一台归属于自己的子 `cli` 设备，
// 把凭据写到 agrctl 读取的位置（agrctlcred）。凭据里 DelegatedBy 非空即「桌面代签」，
// 退出 / 被迫退出时只回收这一种；用户手动 `agrctl login` 的那份桌面既不覆盖也不吊销。
// 代签失败只记日志、不影响桌面登录，下次启动重试。

// cliChildrenPath 是 server 的子设备代签端点（凭据是桌面自己的设备 access token）。
const cliChildrenPath = "/v1/devices/cli-children"

// agrctlRevokeTimeout 限制退出时吊销子设备的等待：退出必须收得住。
const agrctlRevokeTimeout = 5 * time.Second

type cliChildResp struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	DeviceID         int64  `json:"device_id"`
}

// agrctlDataDir 解析 agrctl 凭据所在目录——与 agrctl 自己用的是同一个 paths.AppDataDir。
func agrctlDataDir() (string, error) { return paths.AppDataDir() }

// provisionAgrctlAsync 在登录成功后后台代签：目录在调用方线程上解析，之后不再读环境。
func (s *service) provisionAgrctlAsync(ctx context.Context, row *server_state_entity.ServerState) {
	dir, err := agrctlDataDir()
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctlAsync: resolve data dir failed", zap.Error(err))
		return
	}
	snap := *row
	go s.provisionAgrctl(context.WithoutCancel(ctx), dir, &snap)
}

// provisionAgrctlOnStartup 是启动时的同步入口：只在本机 agrctl 还没有凭据时代签。
func (s *service) provisionAgrctlOnStartup(ctx context.Context, row *server_state_entity.ServerState) {
	dir, err := agrctlDataDir()
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctlOnStartup: resolve data dir failed", zap.Error(err))
		return
	}
	s.provisionAgrctl(ctx, dir, row)
}

// provisionAgrctl 代签并落盘；已有手动凭据或未作废的代签凭据时什么都不做。
// 所有失败只记日志。
func (s *service) provisionAgrctl(ctx context.Context, dir string, row *server_state_entity.ServerState) {
	epoch := s.agrctlEpoch.Load()
	s.agrctlMu.Lock()
	defer s.agrctlMu.Unlock()
	if s.agrctlEpoch.Load() != epoch {
		return // 排队期间已退出登录
	}
	unlock, err := agrctlcred.Lock(dir)
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctl: lock credentials failed", zap.Error(err))
		return
	}
	defer unlock()

	fingerprint := ""
	prev, err := agrctlcred.Load(dir)
	switch {
	case err == nil:
		if prev.DelegatedBy == "" || !prev.Expired() {
			return // 手动登录的不碰；代签且仍有效的无需重签
		}
		fingerprint = prev.Fingerprint // 失效的代签凭据：沿用指纹，服务端幂等重发
	case !errors.Is(err, os.ErrNotExist):
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctl: read credentials failed", zap.Error(err))
		return
	}
	if fingerprint == "" {
		fingerprint = newCliChildFingerprint()
	}

	name, _ := osHostname()
	var env envelope[cliChildResp]
	body := map[string]string{"fingerprint": fingerprint, "name": name}
	status, err := s.getClient().do(ctx, http.MethodPost, cliChildrenPath, body, &env)
	if err != nil || status != http.StatusOK || env.Code != 0 {
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctl: server refused to mint cli child, will retry next start",
			zap.Int("statusCode", status), zap.Int("code", env.Code), zap.Error(err))
		return
	}

	label := fmt.Sprintf("#%d", row.ServerUserID)
	if me, mErr := s.fetchMe(ctx); mErr == nil && me != nil {
		for _, v := range []string{me.Email, me.DisplayName, me.GithubLogin} {
			if v != "" {
				label = v
				break
			}
		}
	}
	now := time.Now()
	c := &agrctlcred.Credentials{
		ServerURL: row.ServerURL, AccountID: row.ServerUserID, Account: label,
		DeviceID: env.Data.DeviceID, DeviceName: name, Fingerprint: fingerprint,
		RefreshToken: env.Data.RefreshToken, AccessToken: env.Data.AccessToken,
		DelegatedBy: fmt.Sprintf("desktop:%d", row.DeviceID),
	}
	if env.Data.RefreshExpiresIn > 0 {
		c.RefreshExpiresAt = now.Add(time.Duration(env.Data.RefreshExpiresIn) * time.Second).Unix()
	}
	if env.Data.ExpiresIn > 0 {
		c.AccessExpiresAt = now.Add(time.Duration(env.Data.ExpiresIn) * time.Second).Unix()
	}

	if s.agrctlEpoch.Load() != epoch {
		// 铸造期间桌面已退出：别留下一台没人持有的子设备。
		s.revokeCliChild(ctx, c)
		return
	}
	if err := agrctlcred.Save(dir, c); err != nil {
		logger.Ctx(ctx).Warn("server_svc.provisionAgrctl: save credentials failed", zap.Error(err))
		return
	}
	logger.Ctx(ctx).Info("server_svc.provisionAgrctl: agrctl credentials provisioned",
		zap.Int64("childDeviceID", c.DeviceID))
}

// revokeDelegatedAgrctl 在桌面退出 / 被迫退出时吊销并删除桌面代签的 agrctl 凭据；
// 手动登录的凭据原样保留。吊销失败只记日志——本地凭据照删，server 侧会随桌面设备
// 被吊销而连带回收。
func (s *service) revokeDelegatedAgrctl(ctx context.Context) {
	s.agrctlEpoch.Add(1) // 先作废排队中 / 铸造中的代签
	s.agrctlMu.Lock()
	defer s.agrctlMu.Unlock()

	dir, err := agrctlDataDir()
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.revokeDelegatedAgrctl: resolve data dir failed", zap.Error(err))
		return
	}
	unlock, err := agrctlcred.Lock(dir)
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.revokeDelegatedAgrctl: lock credentials failed", zap.Error(err))
		return
	}
	defer unlock()
	c, err := agrctlcred.Load(dir)
	if err != nil || c.DelegatedBy == "" {
		return
	}
	s.revokeCliChild(ctx, c)
	if err := agrctlcred.Delete(dir); err != nil {
		logger.Ctx(ctx).Warn("server_svc.revokeDelegatedAgrctl: delete credentials failed", zap.Error(err))
	}
}

// revokeCliChild 用子设备自己的 token 吊销它自己（与 agrctl logout 同一端点）。
func (s *service) revokeCliChild(ctx context.Context, c *agrctlcred.Credentials) {
	if c.ServerURL == "" || c.AccessToken == "" {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, agrctlRevokeTimeout)
	defer cancel()
	_, err := NewHTTPClient(c.ServerURL, c.AccessToken).do(rctx, http.MethodPost,
		"/v1/oauth/token/revoke", map[string]int64{"device_id": c.DeviceID}, nil)
	if err != nil {
		logger.Ctx(ctx).Warn("server_svc.revokeCliChild: revoke failed", zap.Int64("childDeviceID", c.DeviceID), zap.Error(err))
	}
}

// newCliChildFingerprint 铸一枚新的 cli 子设备指纹，格式与 agrctl login 一致（sha256: 前缀）。
func newCliChildFingerprint() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return string(devicefp.FromSeed("agrctl:" + hex.EncodeToString(b)))
}
