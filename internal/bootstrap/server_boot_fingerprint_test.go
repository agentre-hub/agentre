package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/gogo"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// isCanonicalFingerprint 就地写出「规范指纹」长什么样：`sha256:` + 64 位小写十六进制。
//
// 故意不去问 owner 要一个校验函数：这条用例钉的就是这条契约本身，从被验方那里借一把
// 尺子，尺子被改短了也测不出来。
func isCanonicalFingerprint(fp devicefp.Carrier) bool {
	const prefix = "sha256:"
	s := string(fp)
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	hexPart := strings.TrimPrefix(s, prefix)
	if len(hexPart) != sha256HexLen {
		return false
	}
	for _, r := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

const sha256HexLen = 64

// server_state.device_fingerprint 是一个**值空间成员**：它要与 keychain 指纹、与
// agentred 的指纹、与 server 上 devices.fingerprint 逐字节比较（pkg/wire/devicefp）。
//
// 启动期曾经在这里自己铸一个 16 字节随机十六进制串（没有 `sha256:` 前缀）写进去，
// 于是这个值从生成的那一刻起就不可能等于任何一台机器真实指纹——它不是「另一个指纹」，
// 是**另一个值空间**的值。而唯一的补救在登录路径上（server_svc 发现它与 keychain 不
// 一致时覆盖掉），也就是说：这里铸一个错值，那里专门写一段代码来收拾它。
//
// 本用例钉住不变量本身 —— 启动期写进去的值只能是空串（还不知道）或规范指纹。
func TestServerBoot_GivenFreshInstall_ThenDeviceFingerprintIsEmptyOrCanonical(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	saved := make(chan *server_state_entity.ServerState, 4)
	repo.EXPECT().Get(gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().Save(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, e *server_state_entity.ServerState) error {
			saved <- e
			return nil
		}).AnyTimes()

	prev := server_state_repo.ServerState()
	server_state_repo.RegisterServerState(repo)
	t.Cleanup(func() { server_state_repo.RegisterServerState(prev) })

	ServerBoot(context.Background())
	gogo.Wait()

	// 没写不算错：不变量说的是「写进去的值必须合法」，不是「必须写」。
	close(saved)
	for row := range saved {
		if fp := row.DeviceFingerprint; fp != "" && !isCanonicalFingerprint(fp) {
			t.Fatalf("启动期把 %q 写进了 server_state.device_fingerprint："+
				"空串表示「还不知道」，规范指纹是 `sha256:` + 64 位十六进制；"+
				"任何别的值都不可能与真实指纹逐字节相等，只会让登录路径再去覆盖它一次", fp)
		}
	}
}
