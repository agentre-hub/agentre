package chat_svc

import (
	"context"
	"net/http"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
)

// NoEligibleDeviceError 是新会话第一轮「无处可跑」（spec「会话 › 无处可跑」）：派发对参与的
// 每一台设备都给出了不通过的原因，这一轮不发出。Devices 按派发顺序带着每台设备的结果与
// 原因码，供调用方结构化消费；Error() 是同一份信息渲染成的文本（表头 + 每台设备一行
// 「设备名：原因码」），是 Wails 边界唯一透给前端的通道。原因码本身不在这里翻译 ——
// 界面按码渲染（dispatch_svc 的约定）。
type NoEligibleDeviceError struct {
	httpErr *httputils.Error
	text    string
	Devices []*dispatch_svc.DeviceResult
}

func (e *NoEligibleDeviceError) Error() string { return e.text }

// As 让调用方照常按 *httputils.Error 取错误码（code.DispatchNoEligibleDevice）。
func (e *NoEligibleDeviceError) As(target any) bool {
	if p, ok := target.(**httputils.Error); ok {
		*p = e.httpErr
		return true
	}
	return false
}

// newNoEligibleDeviceError 把派发方案渲染成那一段文本。整段在构造时按调用方 ctx 的语言
// 渲染好：Error() 没有 ctx。
func newNoEligibleDeviceError(ctx context.Context, plan *dispatch_svc.Plan) *NoEligibleDeviceError {
	var devices []*dispatch_svc.DeviceResult
	if plan != nil {
		devices = plan.Devices
	}
	headline := i18n.T(ctx, code.DispatchNoEligibleDevice)
	lines := make([]string, 0, len(devices)+1)
	lines = append(lines, headline)
	for _, d := range devices {
		if d == nil || d.Eligible {
			continue
		}
		reason := ""
		if d.Reason != nil {
			reason = d.Reason.Code
		}
		lines = append(lines, i18n.T(ctx, code.DispatchDeviceReasonLine, deviceLabel(ctx, d), reason))
	}
	return &NoEligibleDeviceError{
		httpErr: &httputils.Error{Status: http.StatusBadRequest, Code: code.DispatchNoEligibleDevice, Msg: headline},
		text:    strings.Join(lines, "\n"),
		Devices: devices,
	}
}

// deviceLabel 设备的显示名：有名字用名字；本机没有账号名时写「本机」；都没有时用指纹。
func deviceLabel(ctx context.Context, d *dispatch_svc.DeviceResult) string {
	if name := strings.TrimSpace(d.Name); name != "" {
		return name
	}
	if d.IsSelf {
		return i18n.T(ctx, code.ChatExecTargetDeviceLocal)
	}
	return string(d.Fingerprint)
}
