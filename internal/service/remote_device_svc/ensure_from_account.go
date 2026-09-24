package remote_device_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// EnsureFromAccount 拉一次账号设备清单，收编本机还没有本地记录的 agentred
// （AdoptAccountDevices，见 adopt.go），并带回账号清单里「本机自己」那一行
// （server_svc.Device.IsThisDevice）的展示名。
//
// 为什么把「拉取」和「收编」并成一次调用：App.ServerListDevices（前端刷新设备
// 面板）与 backendWriter.deviceID（ctl 解析 --device 找不到本地记录时）都要做
// 同一件事——拉一次账号设备、翻成 AccountDevice、收编；此前只有前者实现了，后者
// 只能指望用户已经先打开过设备面板、悄悄收编过这个名字。收进这一个方法，两处
// 调用方不再各自重复翻译循环。
//
// server 没接线（bootstrap 顺序 / 还没登录）不是错误：这种情况下账号本来就没有
// 设备可看，返回 ("", false, nil)。拉取本身失败（网络 / 服务端错误）原样上抛，
// 不当成「空清单」吞掉——调用方各自决定要不要吞：app 层只记日志（见
// App.ServerListDevices），ctl 把它并进 --device 解析失败的原因直接报给用户。
func (s *service) EnsureFromAccount(ctx context.Context) (selfName string, ok bool, err error) {
	server := server_svc.Server()
	if server == nil {
		return "", false, nil
	}
	devices, err := server.ListDevices(ctx)
	if err != nil {
		return "", false, err
	}
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
