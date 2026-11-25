package ctlaccount

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 供应商测试连接与模型发现：base URL 可能只有设备访问得到（内网、localhost），所以经 relay 在
// 一台在线设备上执行（ENGINE_TEST / ENGINE_DISCOVER），不在 server 上执行。设备用它快照里的
// 供应商配置——刚创建或修改的供应商还没同步到那台设备时，设备答 ProviderMissing。

// ProbeInput 指定探测哪个供应商，以及在哪台设备上。
type ProbeInput struct {
	// ProviderKey 是供应商的账号级同步标识（CtlProvider.sync_id）。
	ProviderKey string
	// ModelKey 只用于测试：空 = 供应商的默认模型。
	ModelKey string
	// Device 是 --device：设备的 id、备注名或自报名字；空 = 第一台在线且已收到这个供应商的设备。
	Device string
}

// ProbeResult 是一次测试连接的结果；Message 不含密钥。
type ProbeResult struct {
	OK        bool
	Message   string
	LatencyMs int64
	Device    string
}

// DiscoveredModel 是上游列出的一个模型。
type DiscoveredModel struct{ ModelID, Name string }

// NotSyncedError 是设备还没有收到这个供应商（稍后重试即可）。Device 为空 = 没有任何在线设备有。
type NotSyncedError struct{ Device string }

func (e *NotSyncedError) Error() string {
	if e.Device == "" {
		return "no online device has received this provider yet — try again shortly, or pick a device with --device"
	}
	return "device " + e.Device + " has not received this provider yet — try again shortly"
}

// TestProvider 在一台设备上用已保存的配置发一次测试请求。
func (a *Client) TestProvider(ctx context.Context, in ProbeInput) (*ProbeResult, error) {
	var out ProbeResult
	device, err := a.onDevice(ctx, in, func(conn *relaypeer.Conn) error {
		resp, err := wirecall.EngineTest(ctx, conn, &agentrewire.EngineTestRequest{ProviderKey: in.ProviderKey, ModelKey: in.ModelKey})
		if err != nil {
			return err
		}
		out = ProbeResult{OK: resp.GetOk(), Message: resp.GetMessage(), LatencyMs: resp.GetLatencyMs()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Device = device
	return &out, nil
}

// DiscoverModels 在一台设备上列出上游可用的模型，并交回执行的设备名。
func (a *Client) DiscoverModels(ctx context.Context, in ProbeInput) ([]DiscoveredModel, string, error) {
	var out []DiscoveredModel
	device, err := a.onDevice(ctx, in, func(conn *relaypeer.Conn) error {
		resp, err := wirecall.EngineDiscover(ctx, conn, &agentrewire.EngineDiscoverRequest{ProviderKey: in.ProviderKey})
		if err != nil {
			return err
		}
		for _, m := range resp.GetModels() {
			out = append(out, DiscoveredModel{ModelID: m.GetModelId(), Name: m.GetName()})
		}
		return nil
	})
	return out, device, err
}

// onDevice 在目标设备上跑 call。点名的设备必须在线；没点名就按列表顺序试在线的 agentred /
// 桌面，跳过还没收到这个供应商或够不着的，用第一台成功的。
func (a *Client) onDevice(ctx context.Context, in ProbeInput, call func(*relaypeer.Conn) error) (string, error) {
	devices, err := a.devices(ctx)
	if err != nil {
		return "", err
	}
	if in.Device != "" {
		// 只有 agentred 与桌面能跑供应商请求；同名的 cli 设备（与主机同名）不参与定位。
		var runners []Device
		for _, d := range devices {
			if d.Kind == "agentred" || d.Kind == "desktop" {
				runners = append(runners, d)
			}
		}
		d, err := LocateDevice(runners, in.Device)
		if err != nil {
			return "", err
		}
		if !d.Online {
			return "", fmt.Errorf("device %s is offline", d.Label())
		}
		err = a.callOn(ctx, d, call)
		if rpcCode(err) == rpcerror.CodeProviderMissing {
			return "", &NotSyncedError{Device: d.Label()}
		}
		return d.Label(), err
	}
	var candidates, missing int
	var lastErr error
	for _, d := range devices {
		if !d.Online || (d.Kind != "agentred" && d.Kind != "desktop") {
			continue
		}
		candidates++
		err := a.callOn(ctx, d, call)
		switch {
		case err == nil:
			return d.Label(), nil
		case rpcCode(err) == rpcerror.CodeProviderMissing:
			missing++
		case errors.Is(err, context.Canceled):
			return "", err
		default:
			lastErr = err
		}
	}
	switch {
	case candidates == 0:
		return "", errors.New("no online device to run this on — start agentred or the desktop, or name a device with --device")
	case lastErr != nil && missing == 0:
		return "", lastErr
	default:
		return "", &NotSyncedError{}
	}
}

func (a *Client) callOn(ctx context.Context, d Device, call func(*relaypeer.Conn) error) error {
	conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(d.Fingerprint))
	if err != nil {
		return a.ReachErr(err, d.Label(), "")
	}
	defer func() { _ = conn.Close() }()
	if err := call(conn); err != nil {
		var rpcErr *rpcerror.Error
		if errors.As(err, &rpcErr) {
			return err // 设备的拒绝保持原样：调用方按 code 区分
		}
		return a.ReachErr(err, d.Label(), "")
	}
	return nil
}

// LocateDevice 把定位（数字 id、备注名、自报名字）解析成一台设备。找不到是执行失败；
// 多个匹配是用法错误。
func LocateDevice(devices []Device, ref string) (Device, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Device{}, Usagef("empty device reference")
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, d := range devices {
			if d.ID == id {
				return d, nil
			}
		}
	}
	var matches []Device
	for _, d := range devices {
		if d.DisplayName == ref || d.Name == ref {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return Device{}, fmt.Errorf("device %q not found", ref)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, d := range matches {
			ids[i] = strconv.FormatInt(d.ID, 10)
		}
		return Device{}, Usagef("device %q is ambiguous — %d matches (ids %s); use an id", ref, len(matches), strings.Join(ids, ", "))
	}
}
