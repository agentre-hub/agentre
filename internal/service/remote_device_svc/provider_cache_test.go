package remote_device_svc_test

import (
	"encoding/json"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

func TestRemoteDeviceSvc_ProviderCache_RecordAndList(t *testing.T) {
	Convey("ProviderCache", t, func() {
		_, _, _, _, svc := setupSvc(t)

		Convey("List returns nil before any Record", func() {
			So(svc.ListDeviceProviders(7), ShouldBeNil)
		})

		Convey("Record then List returns the same providers", func() {
			input := []remote_device_svc.ProviderSummary{
				{Key: "k-1", Name: "main", Type: "anthropic"},
				{Key: "k-2", Name: "backup", Type: "openai"},
			}
			svc.RecordDeviceProviders(7, input)

			got := svc.ListDeviceProviders(7)
			So(got, ShouldHaveLength, 2)
			So(got[0].Key, ShouldEqual, "k-1")
			So(got[1].Key, ShouldEqual, "k-2")
		})

		Convey("List for unknown deviceID returns nil", func() {
			svc.RecordDeviceProviders(7, []remote_device_svc.ProviderSummary{
				{Key: "k-1", Name: "main", Type: "anthropic"},
			})
			So(svc.ListDeviceProviders(99), ShouldBeNil)
		})

		Convey("Second Record for same deviceID overwrites", func() {
			svc.RecordDeviceProviders(7, []remote_device_svc.ProviderSummary{
				{Key: "k-1", Name: "old", Type: "anthropic"},
			})
			svc.RecordDeviceProviders(7, []remote_device_svc.ProviderSummary{
				{Key: "k-2", Name: "new", Type: "openai"},
			})
			got := svc.ListDeviceProviders(7)
			So(got, ShouldHaveLength, 1)
			So(got[0].Key, ShouldEqual, "k-2")
		})

		Convey("Record with empty list is distinguishable from never recorded", func() {
			svc.RecordDeviceProviders(7, []remote_device_svc.ProviderSummary{})

			got := svc.ListDeviceProviders(7)
			So(got, ShouldNotBeNil)
			So(got, ShouldHaveLength, 0)
		})

		Convey("List returns a defensive copy (mutation doesn't affect cache)", func() {
			svc.RecordDeviceProviders(7, []remote_device_svc.ProviderSummary{
				{Key: "k-1", Name: "main", Type: "anthropic"},
			})
			got := svc.ListDeviceProviders(7)
			got[0].Key = "mutated"
			// Second call returns original
			got2 := svc.ListDeviceProviders(7)
			So(got2[0].Key, ShouldEqual, "k-1")
		})

		Convey("concurrent Record+List is race-free (smoke)", func() {
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(2)
				devID := int64(i % 3)
				go func() {
					defer wg.Done()
					svc.RecordDeviceProviders(devID, []remote_device_svc.ProviderSummary{
						{Key: "k", Name: "n", Type: "t"},
					})
				}()
				go func() {
					defer wg.Done()
					_ = svc.ListDeviceProviders(devID)
				}()
			}
			wg.Wait()
		})
	})
}

func TestProviderSummary_JSONContract(t *testing.T) {
	Convey("ProviderSummary uses lower-camel JSON fields for Wails/frontend", t, func() {
		raw, err := json.Marshal(remote_device_svc.ProviderSummary{
			Key:  "k-1",
			Name: "Provider",
			Type: "anthropic",
		})

		So(err, ShouldBeNil)
		So(string(raw), ShouldEqual, `{"key":"k-1","name":"Provider","type":"anthropic"}`)
	})
}

// TestRemoteDeviceSvc_CapabilityCache 钉死决策 11 的能力位缓存：watcher 把 daemon
// 公布的 llm-model-target-v1 能力位记进缓存，SupportsLLMModelTarget 据此放行
// fixed-model；未探过 / 不公布 → false（保守禁用）。
func TestRemoteDeviceSvc_CapabilityCache(t *testing.T) {
	Convey("CapabilityCache", t, func() {
		_, _, _, _, svc := setupSvc(t)

		Convey("unprobed device reports false", func() {
			So(svc.SupportsLLMModelTarget(7), ShouldBeFalse)
		})

		Convey("advertised llm-model-target-v1 reports true", func() {
			svc.RecordDeviceCapabilities(7, []string{"llm-model-target-v1"})
			So(svc.SupportsLLMModelTarget(7), ShouldBeTrue)
		})

		Convey("capability absent reports false", func() {
			svc.RecordDeviceCapabilities(7, []string{"something-else"})
			So(svc.SupportsLLMModelTarget(7), ShouldBeFalse)
		})

		Convey("per-device isolation", func() {
			svc.RecordDeviceCapabilities(7, []string{"llm-model-target-v1"})
			So(svc.SupportsLLMModelTarget(7), ShouldBeTrue)
			So(svc.SupportsLLMModelTarget(8), ShouldBeFalse)
		})
	})
}

// TestRemoteDeviceSvc_ProviderSummary_Models 钉死非敏感目录含模型摘要：ProviderSummary
// 的 lowerCamel JSON 形状带 defaultModelKey + models[]，且绝不包含 API key / baseURL。
func TestRemoteDeviceSvc_ProviderSummary_Models(t *testing.T) {
	Convey("ProviderSummary carries non-sensitive model summaries", t, func() {
		raw, err := json.Marshal(remote_device_svc.ProviderSummary{
			Key:             "k-1",
			Name:            "Provider",
			Type:            "anthropic",
			DefaultModelKey: "m-1",
			Models: []remote_device_svc.ModelSummary{
				{Key: "m-1", ModelID: "claude-sonnet-4-6", Enabled: true},
			},
		})
		So(err, ShouldBeNil)
		So(string(raw), ShouldContainSubstring, `"defaultModelKey":"m-1"`)
		So(string(raw), ShouldContainSubstring, `"modelId":"claude-sonnet-4-6"`)
		So(string(raw), ShouldNotContainSubstring, "apiKey")
		So(string(raw), ShouldNotContainSubstring, "baseURL")
	})
}
