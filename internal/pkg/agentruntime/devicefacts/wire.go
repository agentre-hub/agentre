package devicefacts

import (
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ToWire 把一份情况折成 device.facts 的应答。本机项目路径不上线:agentred 没有它,
// 桌面端的那份另走 server 上报。
func ToWire(report syncwire.DeviceFactsReport) *agentrewire.DeviceFactsResponse {
	out := &agentrewire.DeviceFactsResponse{CollectedAtMs: report.CollectedAt}
	for _, rt := range report.Runtimes {
		out.Runtimes = append(out.Runtimes, &agentrewire.DeviceRuntimeFact{
			Runtime: rt.Runtime, Found: rt.Found, Version: rt.Version, Path: rt.Path,
			MinimumVersion: rt.MinimumVersion, BelowMinimum: rt.BelowMinimum, NativeLogin: rt.NativeLogin,
		})
	}
	for _, e := range report.ACPEngines {
		out.AcpEngines = append(out.AcpEngines, &agentrewire.DeviceACPEngineFact{EngineSyncId: e.EngineSyncID, Resolvable: e.Resolvable})
	}
	for _, e := range report.ServiceEngines {
		out.ServiceEngines = append(out.ServiceEngines, &agentrewire.DeviceServiceEngineFact{
			EngineSyncId: e.EngineSyncID, Runtime: e.Runtime, CredentialPresent: e.CredentialPresent,
			LastConnection: e.LastConnection, LastConnectionAtMs: e.LastConnectionAt,
		})
	}
	for _, s := range report.SkillPacks {
		out.SkillPacks = append(out.SkillPacks, &agentrewire.DeviceSkillPacks{Runtime: s.Runtime, PackIds: append([]string(nil), s.PackIDs...)})
	}
	return out
}

// FromWire 是 ToWire 的反方向;列表一律是非 nil 的切片。
func FromWire(resp *agentrewire.DeviceFactsResponse) syncwire.DeviceFactsReport {
	out := syncwire.DeviceFactsReport{
		CollectedAt:    resp.GetCollectedAtMs(),
		Runtimes:       make([]syncwire.RuntimeFact, 0, len(resp.GetRuntimes())),
		ACPEngines:     make([]syncwire.ACPEngineFact, 0, len(resp.GetAcpEngines())),
		ServiceEngines: make([]syncwire.ServiceEngineFact, 0, len(resp.GetServiceEngines())),
		SkillPacks:     make([]syncwire.RuntimeSkillPacks, 0, len(resp.GetSkillPacks())),
		LocalPaths:     []syncwire.LocalPathItem{},
	}
	for _, rt := range resp.GetRuntimes() {
		out.Runtimes = append(out.Runtimes, syncwire.RuntimeFact{
			Runtime: rt.GetRuntime(), Found: rt.GetFound(), Version: rt.GetVersion(), Path: rt.GetPath(),
			MinimumVersion: rt.GetMinimumVersion(), BelowMinimum: rt.GetBelowMinimum(), NativeLogin: rt.GetNativeLogin(),
		})
	}
	for _, e := range resp.GetAcpEngines() {
		out.ACPEngines = append(out.ACPEngines, syncwire.ACPEngineFact{EngineSyncID: e.GetEngineSyncId(), Resolvable: e.GetResolvable()})
	}
	for _, e := range resp.GetServiceEngines() {
		out.ServiceEngines = append(out.ServiceEngines, syncwire.ServiceEngineFact{
			EngineSyncID: e.GetEngineSyncId(), Runtime: e.GetRuntime(), CredentialPresent: e.GetCredentialPresent(),
			LastConnection: e.GetLastConnection(), LastConnectionAt: e.GetLastConnectionAtMs(),
		})
	}
	for _, s := range resp.GetSkillPacks() {
		out.SkillPacks = append(out.SkillPacks, syncwire.RuntimeSkillPacks{Runtime: s.GetRuntime(), PackIDs: append([]string(nil), s.GetPackIds()...)})
	}
	return out
}
