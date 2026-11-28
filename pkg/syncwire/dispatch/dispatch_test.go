package dispatch_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
)

var seen = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)

// okDevice 是一台能通过全部检查的桌面端(claude-code 已装且已登录,有项目路径)。
func okDevice(fp string) dispatch.Device {
	return dispatch.Device{
		Fingerprint: fp, Kind: dispatch.KindDesktop, Online: true, AppRunning: true, Paired: true,
		Runtimes: map[string]dispatch.RuntimeFact{
			"claude-code": {Found: true, NativeLogin: dispatch.LoginLoggedIn},
		},
		ProjectPaths: map[string]bool{"p1": true},
	}
}

func baseInput(devs ...dispatch.Device) dispatch.Input {
	return dispatch.Input{
		Devices:    devs,
		Engine:     dispatch.EngineProfile{SyncID: "e1", Name: "main", Runtime: "claude-code", ModelSource: dispatch.ModelNative},
		ProjectID:  "p1",
		Constraint: dispatch.Constraint{Kind: dispatch.ConstraintAny},
	}
}

func reasonOf(t *testing.T, r dispatch.Result, fp string) dispatch.Reason {
	t.Helper()
	for _, d := range r.Devices {
		if d.Fingerprint == fp {
			return d.Reason
		}
	}
	t.Fatalf("device %s missing from result", fp)
	return dispatch.Reason{}
}

func TestEvaluate_Checks(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *dispatch.Input, d *dispatch.Device)
		want   dispatch.Reason
	}{
		{"offline", func(_ *dispatch.Input, d *dispatch.Device) { d.Online = false; d.LastSeen = seen },
			dispatch.Reason{Code: dispatch.ReasonOffline, LastSeen: seen}},
		{"app not running", func(_ *dispatch.Input, d *dispatch.Device) { d.AppRunning = false },
			dispatch.Reason{Code: dispatch.ReasonAppNotRunning}},
		{"not paired agentred", func(_ *dispatch.Input, d *dispatch.Device) {
			d.Kind = dispatch.KindAgentred
			d.RequiresPairing = true
			d.Paired = false
		},
			dispatch.Reason{Code: dispatch.ReasonNotPaired}},
		{"label missing", func(in *dispatch.Input, d *dispatch.Device) {
			in.Constraint = dispatch.Constraint{Kind: dispatch.ConstraintLabels, Labels: []string{"gpu", "fast"}}
			d.Labels = []string{"fast"}
		}, dispatch.Reason{Code: dispatch.ReasonLabelMissing, Labels: []string{"gpu"}}},
		{"not in device set", func(in *dispatch.Input, _ *dispatch.Device) {
			in.Constraint = dispatch.Constraint{Kind: dispatch.ConstraintDevices, Devices: []string{"other"}}
		}, dispatch.Reason{Code: dispatch.ReasonNotInDevices}},
		{"openclaw on agentred", func(in *dispatch.Input, d *dispatch.Device) {
			in.Engine.Runtime = "openclaw"
			d.Kind = dispatch.KindAgentred
		}, dispatch.Reason{Code: dispatch.ReasonRuntimeDesktopOnly, Runtime: "openclaw"}},
		{"builtin on agentred", func(in *dispatch.Input, d *dispatch.Device) {
			in.Engine.Runtime = "builtin"
			d.Kind = dispatch.KindAgentred
		}, dispatch.Reason{Code: dispatch.ReasonRuntimeDesktopOnly, Runtime: "builtin"}},
		{"runtime missing", func(_ *dispatch.Input, d *dispatch.Device) { d.Runtimes = nil },
			dispatch.Reason{Code: dispatch.ReasonRuntimeMissing, Runtime: "claude-code"}},
		{"runtime not found", func(_ *dispatch.Input, d *dispatch.Device) {
			d.Runtimes = map[string]dispatch.RuntimeFact{"claude-code": {Found: false}}
		}, dispatch.Reason{Code: dispatch.ReasonRuntimeMissing, Runtime: "claude-code"}},
		{"runtime too old", func(_ *dispatch.Input, d *dispatch.Device) {
			d.Runtimes = map[string]dispatch.RuntimeFact{"claude-code": {Found: true, BelowMinimum: true, NativeLogin: dispatch.LoginLoggedIn}}
		}, dispatch.Reason{Code: dispatch.ReasonRuntimeTooOld, Runtime: "claude-code"}},
		{"acp command unresolved", func(in *dispatch.Input, d *dispatch.Device) {
			in.Engine.Runtime = "acp"
			d.ACPResolvable = map[string]bool{"e1": false}
		}, dispatch.Reason{Code: dispatch.ReasonRuntimeMissing, Runtime: "acp"}},
		{"native logged out", func(_ *dispatch.Input, d *dispatch.Device) {
			d.Runtimes = map[string]dispatch.RuntimeFact{"claude-code": {Found: true, NativeLogin: dispatch.LoginLoggedOut}}
		}, dispatch.Reason{Code: dispatch.ReasonNotLoggedIn, Runtime: "claude-code"}},
		{"provider unreachable", func(in *dispatch.Input, _ *dispatch.Device) {
			in.Engine.ModelSource = dispatch.ModelProvider
			in.Engine.ProviderKey = "ollama"
			in.Engine.ProviderName = "Ollama"
			in.Providers = map[string]dispatch.ProviderReach{"ollama": {Devices: []string{"other"}}}
		}, dispatch.Reason{Code: dispatch.ReasonProviderUnreachable, Provider: "Ollama"}},
		{"hermes credential missing", func(in *dispatch.Input, _ *dispatch.Device) {
			in.Engine.Runtime = "hermes"
		}, dispatch.Reason{Code: dispatch.ReasonNoCredential, Engine: "main"}},
		{"openclaw credential missing", func(in *dispatch.Input, _ *dispatch.Device) {
			in.Engine.Runtime = "openclaw"
		}, dispatch.Reason{Code: dispatch.ReasonNoCredential, Engine: "main"}},
		{"project path missing", func(in *dispatch.Input, d *dispatch.Device) {
			in.ProjectName = "Proj"
			d.ProjectPaths = nil
		}, dispatch.Reason{Code: dispatch.ReasonNoProjectPath, Project: "Proj"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := okDevice("a")
			in := baseInput(d)
			tc.mutate(&in, &d)
			in.Devices = []dispatch.Device{d}
			got := dispatch.Evaluate(in)
			assert.False(t, got.Found)
			assert.Equal(t, tc.want, reasonOf(t, got, "a"))
			assert.False(t, got.Devices[0].Eligible)
		})
	}
}

func TestEvaluate_PassingVariants(t *testing.T) {
	t.Run("unknown native login counts as available", func(t *testing.T) {
		d := okDevice("a")
		d.Runtimes = map[string]dispatch.RuntimeFact{"claude-code": {Found: true, NativeLogin: dispatch.LoginUnknown}}
		got := dispatch.Evaluate(baseInput(d))
		assert.True(t, got.Found)
		assert.Equal(t, "a", got.Chosen)
	})
	t.Run("no project skips the path check", func(t *testing.T) {
		d := okDevice("a")
		d.ProjectPaths = nil
		in := baseInput(d)
		in.ProjectID = ""
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("provider any reach", func(t *testing.T) {
		d := okDevice("a")
		in := baseInput(d)
		in.Engine.ModelSource = dispatch.ModelProvider
		in.Engine.ProviderKey = "p"
		in.Providers = map[string]dispatch.ProviderReach{"p": {Any: true}}
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("provider reach lists device", func(t *testing.T) {
		d := okDevice("a")
		in := baseInput(d)
		in.Engine.ModelSource = dispatch.ModelProvider
		in.Engine.ProviderKey = "p"
		in.Providers = map[string]dispatch.ProviderReach{"p": {Devices: []string{"a"}}}
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("hermes with credential on agentred", func(t *testing.T) {
		d := okDevice("a")
		d.Kind = dispatch.KindAgentred
		d.Credentials = map[string]bool{"e1": true}
		in := baseInput(d)
		in.Engine.Runtime = "hermes"
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("acp resolvable", func(t *testing.T) {
		d := okDevice("a")
		d.ACPResolvable = map[string]bool{"e1": true}
		in := baseInput(d)
		in.Engine.Runtime = "acp"
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("labels constraint satisfied", func(t *testing.T) {
		d := okDevice("a")
		d.Labels = []string{"gpu"}
		in := baseInput(d)
		in.Constraint = dispatch.Constraint{Kind: dispatch.ConstraintLabels, Labels: []string{"gpu"}}
		assert.Equal(t, "a", dispatch.Evaluate(in).Chosen)
	})
	t.Run("agentred needs no app-running or pairing when not required", func(t *testing.T) {
		d := okDevice("a")
		d.Kind = dispatch.KindAgentred
		d.AppRunning = false
		d.Paired = false
		assert.Equal(t, "a", dispatch.Evaluate(baseInput(d)).Chosen)
	})
}

func TestEvaluate_FirstFailingCheckWins(t *testing.T) {
	d := okDevice("a")
	d.Online = false
	d.AppRunning = false
	d.Runtimes = nil
	got := dispatch.Evaluate(baseInput(d))
	assert.Equal(t, dispatch.ReasonOffline, reasonOf(t, got, "a").Code)
}

func TestEvaluate_ExcludesNonParticipatingKinds(t *testing.T) {
	cli, web, mobile := okDevice("cli"), okDevice("web"), okDevice("mobile")
	cli.Kind, web.Kind, mobile.Kind = "cli", "web", "mobile"
	in := baseInput(cli, web, mobile, okDevice("a"))
	in.AccountOrder = []string{"cli", "web", "mobile", "a"}
	in.ManualDevice = "cli"
	got := dispatch.Evaluate(in)
	assert.Equal(t, "a", got.Chosen)
	assert.Len(t, got.Devices, 1)
}

func TestEvaluate_Ordering(t *testing.T) {
	devs := func() []dispatch.Device { return []dispatch.Device{okDevice("a"), okDevice("b"), okDevice("c")} }
	order := func(r dispatch.Result) []string {
		var o []string
		for _, d := range r.Devices {
			o = append(o, d.Fingerprint)
		}
		return o
	}
	t.Run("account order", func(t *testing.T) {
		in := baseInput(devs()...)
		in.AccountOrder = []string{"c", "a", "b"}
		got := dispatch.Evaluate(in)
		assert.Equal(t, []string{"c", "a", "b"}, order(got))
		assert.Equal(t, "c", got.Chosen)
		assert.Equal(t, dispatch.ByAccountOrder, got.ChosenBy)
	})
	t.Run("devices missing from account order go last in input order", func(t *testing.T) {
		in := baseInput(devs()...)
		in.AccountOrder = []string{"b"}
		assert.Equal(t, []string{"b", "a", "c"}, order(dispatch.Evaluate(in)))
	})
	t.Run("local first lifts self ahead of account order", func(t *testing.T) {
		in := baseInput(devs()...)
		in.AccountOrder = []string{"c", "a", "b"}
		in.LocalFirst, in.SelfFingerprint = true, "b"
		got := dispatch.Evaluate(in)
		assert.Equal(t, []string{"b", "c", "a"}, order(got))
		assert.Equal(t, "b", got.Chosen)
	})
	t.Run("local first off keeps account order", func(t *testing.T) {
		in := baseInput(devs()...)
		in.AccountOrder = []string{"c", "a", "b"}
		in.SelfFingerprint = "b"
		assert.Equal(t, "c", dispatch.Evaluate(in).Chosen)
	})
	t.Run("project default beats account order and local first", func(t *testing.T) {
		in := baseInput(devs()...)
		in.AccountOrder = []string{"c", "a", "b"}
		in.LocalFirst, in.SelfFingerprint = true, "b"
		in.ProjectDefaultDevice = "a"
		got := dispatch.Evaluate(in)
		assert.Equal(t, []string{"a", "b", "c"}, order(got))
		assert.Equal(t, "a", got.Chosen)
		assert.Equal(t, dispatch.ByProjectDefault, got.ChosenBy)
	})
	t.Run("manual beats project default", func(t *testing.T) {
		in := baseInput(devs()...)
		in.ProjectDefaultDevice = "a"
		in.ManualDevice = "c"
		got := dispatch.Evaluate(in)
		assert.Equal(t, []string{"c", "a", "b"}, order(got))
		assert.Equal(t, "c", got.Chosen)
		assert.Equal(t, dispatch.ByManual, got.ChosenBy)
	})
	t.Run("failing manual falls back to automatic", func(t *testing.T) {
		ds := devs()
		ds[2].Online = false
		in := baseInput(ds...)
		in.ManualDevice = "c"
		in.ProjectDefaultDevice = "b"
		got := dispatch.Evaluate(in)
		assert.Equal(t, "b", got.Chosen)
		assert.Equal(t, dispatch.ByProjectDefault, got.ChosenBy)
		assert.Equal(t, dispatch.ReasonOffline, reasonOf(t, got, "c").Code)
	})
	t.Run("failing project default falls back to account order", func(t *testing.T) {
		ds := devs()
		ds[1].AppRunning = false
		in := baseInput(ds...)
		in.ProjectDefaultDevice = "b"
		in.AccountOrder = []string{"c", "a", "b"}
		got := dispatch.Evaluate(in)
		assert.Equal(t, "c", got.Chosen)
		assert.Equal(t, dispatch.ByAccountOrder, got.ChosenBy)
	})
	t.Run("manual naming unknown device is ignored", func(t *testing.T) {
		in := baseInput(devs()...)
		in.ManualDevice = "ghost"
		got := dispatch.Evaluate(in)
		assert.Equal(t, "a", got.Chosen)
		assert.Equal(t, dispatch.ByAccountOrder, got.ChosenBy)
	})
}

func TestEvaluate_AllFail(t *testing.T) {
	a, b := okDevice("a"), okDevice("b")
	a.Online, a.LastSeen = false, seen
	b.Runtimes = nil
	got := dispatch.Evaluate(baseInput(a, b))
	assert.False(t, got.Found)
	assert.Empty(t, got.Chosen)
	assert.Len(t, got.Devices, 2)
	assert.Equal(t, dispatch.ReasonOffline, reasonOf(t, got, "a").Code)
	assert.Equal(t, dispatch.ReasonRuntimeMissing, reasonOf(t, got, "b").Code)
}

func TestEvaluate_NoDevices(t *testing.T) {
	got := dispatch.Evaluate(baseInput())
	assert.False(t, got.Found)
	assert.Empty(t, got.Devices)
}

func TestEvaluate_EligibleDevicesCarryNoReason(t *testing.T) {
	got := dispatch.Evaluate(baseInput(okDevice("a")))
	assert.True(t, got.Devices[0].Eligible)
	assert.Equal(t, dispatch.Reason{}, got.Devices[0].Reason)
}
