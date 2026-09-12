package remote_device_svc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/client"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// directPreferenceGrace 是中转先答上来时,仍然留给直连的窗口。
//
// 两条路径不是对称的:中转是在一条早已连着的常驻链路上开一条虚拟通道(几乎不花
// 时间),直连要现做一次 TLS 握手再发凭据(同网段几十毫秒)。谁先答谁赢的话,结果
// 是每一台局域网里的机器都被钉死在账号服务上 —— 账号服务一停,它们跟着一起不可用,
// 而那条直连一直是通的(F11)。窗口取得比一次同网段握手宽裕、又远短于用户能察觉的
// 时延:直连通就用直连,不通也只是把中转晚交出来这么久。
const directPreferenceGrace = 500 * time.Millisecond

// dialOutcome 是一条路径的拨号结果。address 只有直连填(它是设备面板要记成最近
// 地址的那个值)。
type dialOutcome struct {
	conn    client.ProtobufConnection
	address string
	err     error
}

// RaceAccountDirect 连接一台「来自账号的直连」设备（D7/D9）：直连（OpenDirect——全部
// 地址、先固定证书后发凭据）与账号中转同时发起；relay 为 nil 时只有直连一条路。
//
// 两条路径同时试,但胜负不只看谁先答:直连成功就用直连,中转先成功也要把
// directPreferenceGrace 这个窗口留给直连(见该常量)。落选的那条连接就地关掉,不留
// 一条没人用、却把这台设备挂在账号服务上的通道。
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
	return racePreferringDirect(ctx,
		func(ctx context.Context) (client.ProtobufConnection, string, error) {
			c, address, err := dial.OpenDirect(ctx, args)
			return c, address, staleDirectCredential(err)
		},
		func(ctx context.Context) (client.ProtobufConnection, error) {
			return relay.Open(ctx, args.ExpectedDaemonFingerprint, peer)
		},
	)
}

// racePreferringDirect 同时发起直连与中转,按上面 RaceAccountDirect 注释里的规则
// 定胜负:直连成功就用直连,中转先成功也把 directPreferenceGrace 的窗口留给直连,
// 落选的连接就地关掉。两条路径各自的失败原因用 "direct path:" / "relay path:" 保住
// (设备面板与 watcher 的分类读这两个前缀)。
//
// 它不再复核两条路径的对端指纹:两条路径都由同一次 Borrow / 同一行解析出来,指纹
// 取自同一个变量,复核不了任何真会发生的漂移。
func racePreferringDirect(
	ctx context.Context,
	dialDirect func(context.Context) (client.ProtobufConnection, string, error),
	dialRelay func(context.Context) (client.ProtobufConnection, error),
) (client.ProtobufConnection, string, error) {
	directCtx, cancelDirect := context.WithCancel(ctx)
	defer cancelDirect()
	relayCtx, cancelRelay := context.WithCancel(ctx)
	defer cancelRelay()

	directCh := make(chan dialOutcome, 1)
	go func() {
		c, address, err := dialDirect(directCtx)
		directCh <- dialOutcome{conn: c, address: address, err: err}
	}()
	relayCh := make(chan dialOutcome, 1)
	// relayStarted 保住 R6 的「两条路径都试过」:直连可能瞬间成功并立刻返回,那时
	// 中转的 goroutine 可能还没被调度到 —— 两条路径于是变成「有时只试了一条」。
	// 等它真正进入拨号再去看直连的结果,代价是一次 goroutine 调度。
	relayStarted := make(chan struct{})
	go func() {
		close(relayStarted)
		c, err := dialRelay(relayCtx)
		relayCh <- dialOutcome{conn: c, err: err}
	}()
	<-relayStarted

	select {
	case direct := <-directCh:
		if direct.conn != nil {
			go discardOutcome(relayCh)
			return direct.conn, direct.address, nil
		}
		// 直连没成:这台机器此刻只剩中转这一条路,等它收场。
		relayed, waitErr := awaitOutcome(ctx, relayCh)
		if waitErr != nil {
			return nil, "", errors.Join(directPathErr(direct.err), waitErr)
		}
		if relayed.conn != nil {
			return relayed.conn, "", nil
		}
		return nil, "", errors.Join(directPathErr(direct.err), relayPathErr(relayed.err))
	case relayed := <-relayCh:
		if relayed.conn == nil {
			// 中转没成(账号服务不可达就长这样):直连是唯一的出路,等它收场。
			direct, waitErr := awaitOutcome(ctx, directCh)
			if waitErr != nil {
				return nil, "", errors.Join(waitErr, relayPathErr(relayed.err))
			}
			if direct.conn != nil {
				return direct.conn, direct.address, nil
			}
			return nil, "", errors.Join(directPathErr(direct.err), relayPathErr(relayed.err))
		}
		// 中转先答上来:把窗口留给直连。
		timer := time.NewTimer(directPreferenceGrace)
		defer timer.Stop()
		select {
		case direct := <-directCh:
			if direct.conn != nil {
				_ = relayed.conn.Close()
				return direct.conn, direct.address, nil
			}
			return relayed.conn, "", nil
		case <-timer.C:
			go discardOutcome(directCh)
			return relayed.conn, "", nil
		case <-ctx.Done():
			go discardOutcome(directCh)
			_ = relayed.conn.Close()
			return nil, "", ctx.Err()
		}
	case <-ctx.Done():
		go discardOutcome(directCh)
		go discardOutcome(relayCh)
		return nil, "", ctx.Err()
	}
}

// awaitOutcome 等另一条路径收场;调用方的 ctx 先结束时交回 ctx 的原因,并让迟到的
// 那条连接(如果有)自己被关掉——没人要的连接不能留在对端的登记里。
func awaitOutcome(ctx context.Context, ch <-chan dialOutcome) (dialOutcome, error) {
	select {
	case outcome := <-ch:
		return outcome, nil
	case <-ctx.Done():
		go discardOutcome(ch)
		return dialOutcome{}, ctx.Err()
	}
}

// discardOutcome 收下一条落选路径的结果并关掉它可能带回的连接。
func discardOutcome(ch <-chan dialOutcome) {
	if outcome := <-ch; outcome.conn != nil {
		_ = outcome.conn.Close()
	}
}

// directPathErr / relayPathErr 保持两条路径各自的失败原因可辨认(与竞速原先的
// 措辞逐字一致:设备面板与 watcher 的分类都读这两个前缀)。拨号端口既不给连接也
// 不给原因时补一句话,与原先竞速的兜底同义。
func directPathErr(err error) error { return fmt.Errorf("direct path: %w", missingReason(err)) }
func relayPathErr(err error) error  { return fmt.Errorf("relay path: %w", missingReason(err)) }

func missingReason(err error) error {
	if err == nil {
		return errors.New("returned no connection")
	}
	return err
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
