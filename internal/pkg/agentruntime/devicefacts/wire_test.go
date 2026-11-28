package devicefacts_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given agentred 采到一份情况,When 经 device.facts 交给直连的桌面端,
// Then 桌面端解回来的与 agentred 上报给 server 的是同一份(本机项目路径除外:agentred 没有)。
func TestWire_GivenAReport_WhenSentOverDeviceFacts_ThenTheDesktopReadsTheSameFacts(t *testing.T) {
	report := syncwire.DeviceFactsReport{
		CollectedAt: 1700000000000,
		Runtimes: []syncwire.RuntimeFact{
			{Runtime: "codex", Found: true, Version: "0.46.0", Path: "/bin/codex", NativeLogin: syncwire.NativeLoginLoggedOut},
			{Runtime: "antigravity", Found: true, Version: "1.4", MinimumVersion: "1.5", BelowMinimum: true},
		},
		ACPEngines:     []syncwire.ACPEngineFact{{EngineSyncID: "a", Resolvable: true}},
		ServiceEngines: []syncwire.ServiceEngineFact{{EngineSyncID: "o", Runtime: "openclaw", CredentialPresent: true}},
		SkillPacks:     []syncwire.RuntimeSkillPacks{{Runtime: "claudecode", PackIDs: []string{"x@y"}}},
		LocalPaths:     []syncwire.LocalPathItem{},
	}

	raw, err := proto.Marshal(devicefacts.ToWire(report))
	assert.NoError(t, err)
	var decoded agentrewire.DeviceFactsResponse
	assert.NoError(t, proto.Unmarshal(raw, &decoded))

	assert.Equal(t, report, devicefacts.FromWire(&decoded))
}

// 空情况两头都是空切片,不是 nil:server / 界面据此区分「没有」与「没报」不靠 nil。
func TestWire_GivenAnEmptyResponse_ThenListsAreEmptyNotNil(t *testing.T) {
	got := devicefacts.FromWire(&agentrewire.DeviceFactsResponse{})
	assert.NotNil(t, got.Runtimes)
	assert.NotNil(t, got.ACPEngines)
	assert.NotNil(t, got.ServiceEngines)
	assert.NotNil(t, got.SkillPacks)
	assert.NotNil(t, got.LocalPaths)
}
