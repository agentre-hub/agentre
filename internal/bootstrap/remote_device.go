package bootstrap

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// relayDialAdapter 把 server_svc 的账号中转拨号(DialDaemonRelay)适配成
// remote_device_svc.RelayDialPort。server_svc 不知道 ConnPool 的存在。
type relayDialAdapter struct {
	inner server_svc.ServerSvc
}

func (a relayDialAdapter) Open(ctx context.Context, daemonFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	return a.inner.DialDaemonRelay(ctx, daemonFingerprint, peerFingerprint)
}

// InitRemoteDevice wires the repo + svc default impls. Must run after the
// SQLite DB component is registered (i.e., inside bootstrap.Init after
// migrations.RunMigrations) and after keychain.SetDefault has been called
// (done inside InitHub).
//
// 构造 device-shared ConnPool 一并注入 svc:idle=30s。注入 server_svc 作 relay
// 路径后,Borrow 会并发直连+中转选路(R6)。app.Shutdown 通过
// remote_device_svc.Default().Pool().Close() 平滑回收所有 entries(见
// internal/app/app.go)。
// desktopDeviceFingerprint 交出本机设备指纹,给迁移回填存量对话的 conversation_id
// 用(spec 2026-08-31 决策 2:派生输入是这条对话的发起端指纹 —— 也就是这台桌面端
// 向对端出示、并被 server 存进 peer_fingerprint 的那一个值)。
//
// 它绕开 InitRemoteDevice 直接问 remote_device_svc 的生成函数,因为迁移跑在装配
// 之前;走的仍是同一个 keychain 账号、同一把生成逻辑,所以先跑迁移不会让这台机器
// 多出第二个"唯一"指纹。惰性调用:全新库上一行都不用回填,这里一次都不会被调到。
func desktopDeviceFingerprint() (string, error) {
	return remote_device_svc.EnsureDeviceFingerprint(keychain.Default())
}

func InitRemoteDevice(_ context.Context) error {
	remote_device_repo.RegisterPairedAgentred(remote_device_repo.NewPairedAgentred())
	repo := remote_device_repo.PairedAgentred()
	dial := remote_device_svc.NewDaemonDial()
	kc := keychain.Default()
	opts := []remote_device_svc.Option{remote_device_svc.WithIdleTimeout(30 * time.Second)}
	if svc := server_svc.Server(); svc != nil {
		opts = append(opts,
			remote_device_svc.WithRelayDial(relayDialAdapter{inner: svc}),
			// server_svc.AccessToken 即 AccountCredentialPort:没有本地配对的
			// daemon,直连改出示账号凭据(auth.account)。
			remote_device_svc.WithAccountCredential(svc))
	}
	pool := remote_device_svc.NewConnPool(repo, kc, dial, opts...)
	remote_device_svc.SetDefault(remote_device_svc.New(repo, dial, kc, pool))
	return nil
}
