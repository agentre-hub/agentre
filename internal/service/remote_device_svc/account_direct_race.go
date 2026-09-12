package remote_device_svc

import (
	"context"

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
		return dial.OpenDirect(ctx, args)
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
				return c, err
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
