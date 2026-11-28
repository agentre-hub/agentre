package ctlaccount

import (
	"fmt"
	"strings"
	"time"
)

// Reason 是共享派发函数对一台设备「为什么不能跑」的回答：稳定的原因码加与该码相关的参数
// （server 的 dispatch-target 应答原样给出，字段与 pkg/syncwire/dispatch.Reason 对应）。
// 码是契约，措辞由各宿主自己写 —— 这里是命令行的措辞。
type Reason struct {
	Code string `json:"code"`
	// LastSeen 是设备上次在线的 Unix 毫秒；不知道为 0。
	LastSeen int64    `json:"last_seen"`
	Runtime  string   `json:"runtime"`
	Labels   []string `json:"labels"`
	Project  string   `json:"project"`
	Provider string   `json:"provider"`
	Engine   string   `json:"engine"`
}

// Text 是给人看的一句话；没有原因码（设备通过）时为空，没见过的码原样给出。
func (r Reason) Text() string {
	switch r.Code {
	case "":
		return ""
	case "offline":
		if r.LastSeen > 0 {
			return "offline (last seen " + time.UnixMilli(r.LastSeen).Local().Format("2006-01-02 15:04") + ")"
		}
		return "offline"
	case "app_not_running":
		return "Agentre is not running on that computer"
	case "not_paired":
		return "this computer is not paired with it"
	case "label_missing":
		return "lacks the label " + strings.Join(r.Labels, ", ")
	case "not_in_devices":
		return "not one of the devices the agent is pinned to"
	case "runtime_desktop_only":
		return r.Runtime + " only runs on a desktop"
	case "runtime_missing":
		return r.Runtime + " is not installed"
	case "runtime_too_old":
		return r.Runtime + " is too old"
	case "not_logged_in":
		return r.Runtime + " is not logged in"
	case "provider_unreachable":
		return "provider " + r.Provider + " cannot be reached from that device"
	case "no_credential":
		return "no credential for " + r.Engine + " on that device"
	case "no_project_path":
		return "no path for project " + r.Project
	default:
		return r.Code
	}
}

// DeviceVerdict 是一台参与派发的设备的名字与它没通过的原因。
type DeviceVerdict struct {
	Name   string
	Reason Reason
}

// RequestedDeviceMessage 是 --device 点名的设备被派发规则拒绝时的话（桌面与账号模式共用）。
func RequestedDeviceMessage(agent string, d DeviceVerdict) string {
	return fmt.Sprintf("agent %s cannot run on the requested device — %s: %s", agent, d.Name, d.Reason.Text())
}

// NowhereToRunMessage 是没有任何设备通过派发规则时的话，逐台说出原因。
func NowhereToRunMessage(agent string, devices []DeviceVerdict) string {
	if len(devices) == 0 {
		return fmt.Sprintf("agent %s has no device to run on", agent)
	}
	reasons := make([]string, 0, len(devices))
	for _, d := range devices {
		name := d.Name
		if name == "" {
			name = "(unnamed device)"
		}
		reasons = append(reasons, name+": "+d.Reason.Text())
	}
	return fmt.Sprintf("agent %s cannot run anywhere right now — %s", agent, strings.Join(reasons, "; "))
}

// UnknownDeviceMessage 是 --device 不是任何参与派发的设备时的话（列出可选的）。
func UnknownDeviceMessage(ref, agent string, options []string) string {
	return fmt.Sprintf("--device %q is not a device agent %s can be dispatched to (one of: %s)", ref, agent, strings.Join(options, ", "))
}
