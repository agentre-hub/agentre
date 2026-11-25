package ctlcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 设备管理（仅账号模式）：list / get / update / delete / upgrade device。配对新的 agentred 仍在
// 那台机器上运行 `agentred login`，不在 agrctl 里做。

const (
	deviceListUsage = `agrctl list devices [-o json]

Lists the account's devices: name, kind, online state (a cli device shows when it was last used),
version, and a * on the device this agrctl is logged in as. Needs agrctl login.`
	deviceGetUsage = `agrctl get device <device> [-o json]

Prints one device as JSON, including the agent backends bound to it. <device> is an id or a name.
Needs agrctl login.`
	deviceUpdateUsage = `agrctl update device <device> --name <name>

Gives the device an account-level name. Needs agrctl login and the devices:write scope.`
	deviceDeleteUsage = `agrctl delete device <device>

Revokes the device. Deleting the device this agrctl is logged in as is the same as 'agrctl logout'.
Needs agrctl login and the devices:write scope.`
	deviceUpgradeUsage = `agrctl upgrade device <device>

Asks an agentred device to upgrade itself. Only agentred devices can be upgraded. Needs agrctl login
and the devices:write scope.`
)

// deviceUsageText 是 help 里关于设备管理的全部说明。
const deviceUsageText = deviceListUsage + "\n\n" + deviceGetUsage + "\n\n" + deviceUpdateUsage + "\n\n" +
	deviceDeleteUsage + "\n\n" + deviceUpgradeUsage

var deviceNameFlag = &flagDef{name: "name", value: "<name>", usage: "account-level device name", notEmpty: true}

func isDeviceResource(word string) bool { return word == "device" || word == "devices" }

// deviceVerb 报告这条命令是不是设备管理；rest 是动词之后的参数。
func deviceVerb(verb string, rest []string) bool {
	if len(rest) == 0 || !isDeviceResource(rest[0]) {
		return false
	}
	switch verb {
	case "list", "get", "update", "delete", "upgrade":
		return true
	}
	return false
}

func deviceHelp(verb string) string {
	switch verb {
	case "get":
		return deviceGetUsage
	case "update":
		return deviceUpdateUsage
	case "delete":
		return deviceDeleteUsage
	case "upgrade":
		return deviceUpgradeUsage
	}
	return deviceListUsage
}

var errDevicesNeedLogin = errors.New("device management needs an account login — run 'agrctl login'")

// runDevice 执行一条设备管理命令；args 是资源名（device / devices）之后的参数。
func runDevice(verb string, args []string, s *sys) error {
	if wantsHelp(args) {
		_, _ = fmt.Fprintln(s.stdout, deviceHelp(verb))
		return nil
	}
	var defs []*flagDef
	switch verb {
	case "list", "get":
		defs = outputFlagNames
	case "update":
		defs = []*flagDef{deviceNameFlag}
	}
	p, err := parseArgs(args, defs)
	if err != nil {
		return err
	}
	switch {
	case verb == "list" && len(p.positional) > 0:
		return usageErrorf("unexpected argument %q", p.positional[0])
	case verb != "list" && len(p.positional) != 1:
		return usageErrorf("%s device needs exactly one <device>", verb)
	}
	asJSON := false
	if verb == "list" || verb == "get" {
		if asJSON, err = wantJSON(p); err != nil {
			return err
		}
	}
	name := ""
	if verb == "update" {
		if !p.has("name") {
			return usageErrorf("update device: --name is required")
		}
		if name = strings.TrimSpace(last(p, "name")); name == "" {
			return emptyValue("name")
		}
	}

	x, err := selectExecutor(s, "", "")
	var unavailable *defaultDesktopUnavailable
	switch {
	case errors.As(err, &unavailable):
		return errDevicesNeedLogin
	case err != nil:
		return err
	case x.kind != execAccount:
		return errDevicesNeedLogin
	}
	if verb != "list" && verb != "get" {
		if err := x.refuseExternal(s, verb+" device"); err != nil {
			return err
		}
	}
	ctx := context.Background()
	devices, err := ctlaccount.ListDevices(ctx, x.acct)
	if err != nil {
		return err
	}
	if verb == "list" {
		return listDevices(s, devices, asJSON)
	}
	d, err := ctlaccount.LocateDevice(devices, p.positional[0])
	if err != nil {
		return accountErr(err)
	}
	switch verb {
	case "get":
		return getDevice(s, x, d, args)
	case "update":
		body, _ := json.Marshal(map[string]string{"display_name": name})
		if _, err := x.acct.PatchRaw(ctx, fmt.Sprintf("/v1/devices/%d", d.ID), body); err != nil {
			return serverErr(err)
		}
		_, _ = fmt.Fprintf(s.stdout, "device %s renamed to %s\n", d.Label(), name)
		return nil
	case "delete":
		return deleteDevice(s, x, d)
	default:
		return upgradeDevice(ctx, s, x, d)
	}
}

type deviceView struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Platform   string   `json:"platform,omitempty"`
	Version    string   `json:"version,omitempty"`
	Online     bool     `json:"online"`
	LastSeenAt int64    `json:"lastSeenAt,omitempty"`
	Current    bool     `json:"current"`
	Backends   []string `json:"backends,omitempty"`
}

func viewOfDevice(d ctlaccount.Device) deviceView {
	return deviceView{ID: d.ID, Name: d.Label(), Kind: d.Kind, Platform: d.Platform, Version: d.Version,
		Online: d.Online, LastSeenAt: d.LastSeenAt, Current: d.IsThisDevice}
}

// deviceStatus：cli 设备不连中继，没有在线这个概念，只显示最近使用时间。
func deviceStatus(d ctlaccount.Device) string {
	switch {
	case d.Kind == "cli" && d.LastSeenAt == 0:
		return "never used"
	case d.Kind == "cli":
		return "last used " + time.UnixMilli(d.LastSeenAt).Local().Format("2006-01-02 15:04")
	case d.Online:
		return "online"
	default:
		return "offline"
	}
}

func listDevices(s *sys, devices []ctlaccount.Device, asJSON bool) error {
	if asJSON {
		views := make([]deviceView, 0, len(devices))
		for _, d := range devices {
			views = append(views, viewOfDevice(d))
		}
		return printJSON(s.stdout, views)
	}
	rows := make([][]string, 0, len(devices))
	for _, d := range devices {
		cur := ""
		if d.IsThisDevice {
			cur = "*"
		}
		rows = append(rows, []string{itoa(d.ID), d.Label(), d.Kind, deviceStatus(d), d.Version, cur})
	}
	return printTable(s.stdout, []string{"ID", "NAME", "KIND", "STATUS", "VERSION", "CURRENT"}, rows)
}

// getDevice 打印一台设备，并列出绑定在它上面的后端。
func getDevice(s *sys, x *executor, d ctlaccount.Device, args []string) error {
	v := viewOfDevice(d)
	items, err := newCatalog(x, args).list(agentrewire.CtlKind_CTL_KIND_BACKEND)
	if err != nil {
		return err
	}
	for _, it := range items {
		if b := it.GetBackend(); b != nil && d.Fingerprint != "" && b.GetDeviceFingerprint() == d.Fingerprint {
			v.Backends = append(v.Backends, b.GetName())
		}
	}
	return printJSON(s.stdout, v)
}

func deleteDevice(s *sys, x *executor, d ctlaccount.Device) error {
	if isHuman(s) {
		_, _ = fmt.Fprintf(s.stderr, "Delete device %s? [y/N] ", d.Label())
		line, _ := bufio.NewReader(s.stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fmt.Errorf("delete of device %s canceled", d.Label())
		}
	}
	if d.IsThisDevice {
		return runLogout(nil, s) // 删除当前设备就是退出登录：吊销并删掉本地凭据
	}
	if err := x.acct.Revoke(context.Background(), d.ID); err != nil {
		return serverErr(err)
	}
	_, _ = fmt.Fprintf(s.stdout, "deleted device %s\n", d.Label())
	return nil
}

func upgradeDevice(ctx context.Context, s *sys, x *executor, d ctlaccount.Device) error {
	if d.Kind != "agentred" {
		return fmt.Errorf("only an agentred device can be upgraded — %s is a %s device", d.Label(), d.Kind)
	}
	body, _ := json.Marshal(map[string]int64{"device_id": d.ID})
	raw, err := x.acct.PostRaw(ctx, "/v1/devices/upgrade", body)
	if err != nil {
		return serverErr(err)
	}
	var out struct {
		Accepted      bool   `json:"accepted"`
		Message       string `json:"message"`
		TargetVersion string `json:"target_version"`
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return fmt.Errorf("decode the upgrade response: %w", err)
	}
	msg := out.Message
	if msg == "" && out.Accepted {
		msg = "upgrade of " + d.Label() + " started"
	}
	if !out.Accepted {
		if msg == "" {
			msg = "device " + d.Label() + " did not accept the upgrade"
		}
		return errors.New(msg)
	}
	_, _ = fmt.Fprintln(s.stdout, msg)
	return nil
}
