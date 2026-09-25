package remote_device_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// EnsureFromAccount 拉一次账号设备清单，交给 AdoptListedDevices 收编并带回本机
// 自己那一行的展示名。
//
// 给 backendWriter.deviceID（ctl 解析 --device 找不到本地记录时）用：它手里没有
// 清单，要自己拉一次；否则只能指望用户已经先打开过设备面板、悄悄收编过这个名字。
// App.ServerListDevices 手里已经有清单（它本来就要把清单交给前端），直接调
// AdoptListedDevices，不为收编再拉第二次。
//
// server 没接线（bootstrap 顺序）或还没登录不是错误：这种情况下账号本来就没有
// 设备可看，返回 ("", false, nil)。拉取本身失败（网络 / 服务端错误）原样上抛，
// 不当成「空清单」吞掉，ctl 把它并进 --device 解析失败的原因直接报给用户。
func (s *service) EnsureFromAccount(ctx context.Context) (selfName string, ok bool, err error) {
	server := server_svc.Server()
	if server == nil {
		return "", false, nil
	}
	devices, err := server.ListDevices(ctx)
	if errors.Is(err, server_svc.ErrNotLoggedIn) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return s.AdoptListedDevices(ctx, devices)
}

// AdoptListedDevices 把一份刚从 server 拉到的账号设备清单翻成 AccountDevice 收编
// （AdoptAccountDevices，见 adopt.go），并带回清单里「本机自己」那一行
// （server_svc.Device.IsThisDevice）的展示名。翻译只在这一处，App.ServerListDevices
// 与 EnsureFromAccount 共用。
func (s *service) AdoptListedDevices(ctx context.Context, devices []server_svc.Device) (selfName string, ok bool, err error) {
	adopting := make([]AccountDevice, 0, len(devices))
	for _, d := range devices {
		adopting = append(adopting, AccountDevice{Fingerprint: d.Fingerprint, Name: d.Name, Kind: d.Kind})
		if d.IsThisDevice {
			selfName, ok = d.Name, true
		}
	}
	if _, aerr := s.AdoptAccountDevices(ctx, adopting); aerr != nil {
		return selfName, ok, aerr
	}
	return selfName, ok, nil
}
