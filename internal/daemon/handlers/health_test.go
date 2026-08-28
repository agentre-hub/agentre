package handlers_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// stubDBStat 是一份固定的库落盘事实,替代真库让本包的单测不碰磁盘。
type stubDBStat struct{ stat handlers.DBStat }

func (s stubDBStat) DBStat() handlers.DBStat { return s.stat }

// TestHealth_Ping_ReportsDatabaseSizeButNotItsPath 覆盖规格「磁盘增长」那一句在 LAN
// 侧的一半:探活应答带上 daemon 库的体量,让桌面端能说出这台盒子上的档案有多大。
//
// **路径不出这台机器**。绝对路径通常带着宿主机的 OS 用户名(/Users/<name>/… 或
// /home/<name>/…),而这条应答发给每一个已配对的对端。规格只要求「路径与体量在 daemon
// 状态查询里可见」,而那份查询是本机 IPC(/local/status 与 `agentred status`),那里
// 两样都在;LAN 这一侧留体量就够了。
//
// 没有注入库统计口时不编造零值 —— 那会在界面上显示成「库是空的」。
func TestHealth_Ping_ReportsDatabaseSizeButNotItsPath(t *testing.T) {
	Convey("Ping 交出库体量", t, func() {
		h := handlers.NewHealthHandlers("inst", state.NewDefault("inst"),
			stubDBStat{stat: handlers.DBStat{Path: "/Users/someone/Library/agentred.db", SizeBytes: 4096}})
		res, err := h.Ping(context.Background())
		So(err, ShouldBeNil)
		So(res.DBSizeBytes, ShouldEqual, int64(4096))

		Convey("应答里没有任何字段带着库文件的绝对路径", func() {
			raw, marshalErr := json.Marshal(res)
			So(marshalErr, ShouldBeNil)
			So(string(raw), ShouldNotContainSubstring, "someone")
			So(string(raw), ShouldNotContainSubstring, "agentred.db")
		})

		Convey("没有库统计口时体量留空", func() {
			bare := handlers.NewHealthHandlers("inst", state.NewDefault("inst"), nil)
			bareRes, bareErr := bare.Ping(context.Background())
			So(bareErr, ShouldBeNil)
			So(bareRes.DBSizeBytes, ShouldEqual, int64(0))
		})
	})
}

func TestHealthHandlers_Ping(t *testing.T) {
	Convey("Ping returns instanceUUID + serverTimeMs", t, func() {
		h := handlers.NewHealthHandlers("inst-uuid-fixed", state.NewDefault("inst-uuid-fixed"), nil)
		res, err := h.Ping(context.Background())
		So(err, ShouldBeNil)
		So(res.InstanceUUID, ShouldEqual, "inst-uuid-fixed")
		So(res.ServerTimeMs, ShouldBeGreaterThan, int64(0))
	})
}

func TestHealth_Ping_IncludesProviders(t *testing.T) {
	Convey("Ping includes known providers sorted by key", t, func() {
		Convey("two providers — returned sorted by key", func() {
			st := state.NewDefault("test-instance")
			st.Mutate(func(s *state.State) {
				s.LLMProviders["zzz-key"] = state.LLMProviderMeta{Name: "Provider Z", Type: "openai"}
				s.LLMProviders["aaa-key"] = state.LLMProviderMeta{Name: "Provider A", Type: "anthropic"}
			})
			h := handlers.NewHealthHandlers("test-instance", st, nil)
			res, err := h.Ping(context.Background())
			So(err, ShouldBeNil)
			So(res.Providers, ShouldHaveLength, 2)
			assert.Equal(t, wire.ProviderSummary{Key: "aaa-key", Name: "Provider A", Type: "anthropic"}, res.Providers[0])
			assert.Equal(t, wire.ProviderSummary{Key: "zzz-key", Name: "Provider Z", Type: "openai"}, res.Providers[1])
		})

		Convey("zero providers — Providers is nil/empty, no panic", func() {
			st := state.NewDefault("test-instance")
			h := handlers.NewHealthHandlers("test-instance", st, nil)
			res, err := h.Ping(context.Background())
			So(err, ShouldBeNil)
			So(res.Providers, ShouldBeEmpty)
		})
	})
}

// TestHealth_Ping_AdvertisesModelTargetCapabilityAndCatalog 钉死决策 11 的目录契约：
// health.ping 公布 llm-model-target-v1 能力位 + 非敏感 Provider/Model 摘要（稳定 key /
// 实际 model id / 启用态），APIKey 与 BaseURL 绝不出现在应答里。
func TestHealth_Ping_AdvertisesModelTargetCapabilityAndCatalog(t *testing.T) {
	Convey("Ping 公布能力位 + 非敏感目录", t, func() {
		st := state.NewDefault("test-instance")
		st.Mutate(func(s *state.State) {
			s.LLMProviders["prov-1"] = state.LLMProviderMeta{ //nolint:gosec // credential-shaped API key is a test fixture.
				Name:            "Anthropic Prod",
				Type:            "anthropic",
				APIKey:          "sk-ant-secret",
				BaseURL:         "https://api.anthropic.com",
				DefaultModelKey: "model-1",
				Models: []state.LLMModelMeta{
					{ModelKey: "model-1", ModelID: "claude-sonnet-4-6", Enabled: true},
					{ModelKey: "model-2", ModelID: "claude-opus-4-5", Enabled: false},
				},
			}
		})
		h := handlers.NewHealthHandlers("test-instance", st, nil)
		res, err := h.Ping(context.Background())
		So(err, ShouldBeNil)
		So(res.Capabilities, ShouldContain, wire.CapLLMModelTargetV1)
		So(res.Providers, ShouldHaveLength, 1)
		p := res.Providers[0]
		So(p.Key, ShouldEqual, "prov-1")
		So(p.DefaultModelKey, ShouldEqual, "model-1")
		So(p.Models, ShouldHaveLength, 2)
		So(p.Models[0].ModelID, ShouldEqual, "claude-sonnet-4-6")
		So(p.Models[0].Enabled, ShouldBeTrue)
		So(p.Models[1].Enabled, ShouldBeFalse)

		raw, marshalErr := json.Marshal(res)
		So(marshalErr, ShouldBeNil)
		So(string(raw), ShouldNotContainSubstring, "sk-ant-secret")
		So(string(raw), ShouldNotContainSubstring, "api.anthropic.com")
	})
}
