package remote_device_svc

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/client"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// RaceAccountDirect 连接一台「来自账号的直连」设备（D7/D9）：直连（OpenDirect——全部
// 地址、先固定证书后发凭据）与账号中转同时发起，先成功者胜；relay 为 nil 时只有直连
// 一条路。
//
// directAddress 只在直连赢下时非空，它就是设备面板要记成最近地址的值
// （RecordDirectSuccess）；中转赢下时为空。
//
// 连接池（ConnPool.Borrow）与 watcher 的探活适配器共用这一份：两处都得先按行的来源
// 分流到这里，否则钥匙串槽里的直连凭据会被当成配对令牌走 auth.connect。
func RaceAccountDirect(
	ctx context.Context, dial DaemonDialPort, relay RelayDialPort, args DirectArgs, peer devicefp.Initiator,
) (conn client.ProtobufConnection, directAddress string, err error) {
	if relay == nil {
		conn, directAddress, err = dial.OpenDirect(ctx, args)
		return conn, directAddress, staleDirectCredential(err)
	}
	// 由 direct 路径的拨号 goroutine 写入；RaceProtobuf 收齐两条路径的结果才返回。
	var directConn client.ProtobufConnection
	var address string
	winner, err := client.RaceProtobuf(ctx,
		client.ProtobufPath{
			Name:        "direct",
			Fingerprint: string(peer),
			Dial: func(ctx context.Context) (client.ProtobufConnection, error) {
				c, a, err := dial.OpenDirect(ctx, args)
				directConn, address = c, a
				return c, staleDirectCredential(err)
			},
		},
		client.ProtobufPath{
			Name:        "relay",
			Fingerprint: string(peer),
			Dial: func(ctx context.Context) (client.ProtobufConnection, error) {
				return relay.Open(ctx, args.ExpectedDaemonFingerprint, peer)
			},
		},
	)
	if err != nil {
		return nil, "", err
	}
	if directConn != nil && winner == directConn {
		return winner, address, nil
	}
	return winner, "", nil
}

// staleDirectCredential 把直连路径上的「凭据被拒」(-32001)折成一次普通的可重试失败：被拒的
// 是本地直连凭据——agentred 对账删了它、或重新登录过——而不是账号凭据，换账号票救不了它，
// 也不是「重试也没用」：中转的账号握手会重新下发一张。原因文字照留，只是不再带着 ErrUnauthorized。
func staleDirectCredential(err error) error {
	if err == nil || !errors.Is(err, ErrUnauthorized) {
		return err
	}
	return fmt.Errorf("local direct credential rejected: %v", err)
}
