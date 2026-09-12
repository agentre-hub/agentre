package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	llmrepomock "github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	remoterepomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestRemoteDeviceSvc_SyncProvider(t *testing.T) {
	Convey("SyncProvider", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		t.Cleanup(func() { llm_provider_repo.RegisterLLMProvider(nil) })

		providerRepo := llmrepomock.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(providerRepo)
		deviceRepo := remoterepomock.NewMockPairedAgentredRepo(ctrl)
		dial := svcmock.NewMockDaemonDialPort(ctrl)
		kc := svcmock.NewMockKeychainPort(ctrl)
		pool := svcmock.NewMockConnPool(ctrl)
		svc := remote_device_svc.New(deviceRepo, dial, kc, pool)

		provider := &llm_provider_entity.LLMProvider{
			ProviderKey:     "prov-1",
			Name:            "Anthropic Prod",
			Type:            string(llm_provider_entity.TypeAnthropic),
			BaseURL:         "https://api.anthropic.com",
			DefaultModelKey: "model-key-1",
			APIKey:          "sk-secret",
			Updatetime:      1716000500,
			Status:          consts.ACTIVE,
		}
		defaultModel := &llm_provider_model_entity.LLMProviderModel{
			ModelKey: "model-key-1",
			ModelID:  "claude-sonnet-4-6",
			Enabled:  llm_provider_model_entity.EnabledOn,
			Status:   consts.ACTIVE,
		}

		Convey("copies local provider metadata, API key and model catalog to remote llm.upsert", func() {
			lease := svcmock.NewMockLease(ctrl)
			providerRepo.EXPECT().FindByKey(gomock.Any(), "prov-1").Return(provider, nil)
			providerRepo.EXPECT().ListModels(gomock.Any(), int64(0)).Return([]*llm_provider_model_entity.LLMProviderModel{defaultModel}, nil)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().LLMUpsert(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, got *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
					So(got.ProviderKey, ShouldEqual, "prov-1")
					So(got.Name, ShouldEqual, "Anthropic Prod")
					So(got.Type, ShouldEqual, "anthropic")
					So(got.BaseUrl, ShouldEqual, "https://api.anthropic.com")
					// 默认模型的 ModelID，而不是 default_model_key。
					So(got.Model, ShouldEqual, "claude-sonnet-4-6")
					So(got.DefaultModelKey, ShouldEqual, "model-key-1")
					So(got.Models, ShouldHaveLength, 1)
					So(got.Models[0].ModelKey, ShouldEqual, "model-key-1")
					So(got.Models[0].ModelId, ShouldEqual, "claude-sonnet-4-6")
					So(got.ApiKey, ShouldEqual, "sk-secret")
					So(got.UpdatedAt, ShouldEqual, int64(1716000500))
					return &agentrewire.LLMUpsertResponse{Ok: true}, nil
				})
			lease.EXPECT().Release()

			err := svc.SyncProvider(context.Background(), 42, "prov-1")
			So(err, ShouldBeNil)

			cached := svc.ListDeviceProviders(42)
			So(cached, ShouldHaveLength, 1)
			So(cached[0].Key, ShouldEqual, "prov-1")
			So(cached[0].Name, ShouldEqual, "Anthropic Prod")
			So(cached[0].Type, ShouldEqual, "anthropic")
			So(cached[0].DefaultModelKey, ShouldEqual, "model-key-1")
			So(cached[0].Models, ShouldHaveLength, 1)
			So(cached[0].Models[0].ModelID, ShouldEqual, "claude-sonnet-4-6")
		})

		Convey("returns provider-not-found before dialing when local provider is missing", func() {
			providerRepo.EXPECT().FindByKey(gomock.Any(), "missing").Return(nil, nil)

			err := svc.SyncProvider(context.Background(), 42, "missing")
			So(err, ShouldNotBeNil)
		})

		Convey("sends an empty model when the provider has no default model", func() {
			noDefault := &llm_provider_entity.LLMProvider{
				ProviderKey: "prov-1",
				Name:        "Anthropic Prod",
				Type:        string(llm_provider_entity.TypeAnthropic),
				APIKey:      "sk-secret",
				Status:      consts.ACTIVE,
			}
			lease := svcmock.NewMockLease(ctrl)
			providerRepo.EXPECT().FindByKey(gomock.Any(), "prov-1").Return(noDefault, nil)
			// 无默认模型 → 目录照发、默认 model 留空。
			providerRepo.EXPECT().ListModels(gomock.Any(), int64(0)).Return(nil, nil)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().LLMUpsert(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, got *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
					So(got.Model, ShouldEqual, "")
					So(got.DefaultModelKey, ShouldEqual, "")
					return &agentrewire.LLMUpsertResponse{Ok: true}, nil
				})
			lease.EXPECT().Release()

			err := svc.SyncProvider(context.Background(), 42, "prov-1")
			So(err, ShouldBeNil)
		})

		Convey("sends an empty model when the default model is disabled", func() {
			disabled := &llm_provider_model_entity.LLMProviderModel{
				ModelKey: "model-key-1",
				ModelID:  "claude-sonnet-4-6",
				Enabled:  llm_provider_model_entity.EnabledOff,
				Status:   consts.ACTIVE,
			}
			lease := svcmock.NewMockLease(ctrl)
			providerRepo.EXPECT().FindByKey(gomock.Any(), "prov-1").Return(provider, nil)
			providerRepo.EXPECT().ListModels(gomock.Any(), int64(0)).Return([]*llm_provider_model_entity.LLMProviderModel{disabled}, nil)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().LLMUpsert(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, got *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
					So(got.Model, ShouldEqual, "")
					// 停用模型仍进目录（daemon 据此拒绝 fixed-model），Enabled=false。
					So(got.Models, ShouldHaveLength, 1)
					So(got.Models[0].Enabled, ShouldBeFalse)
					return &agentrewire.LLMUpsertResponse{Ok: true}, nil
				})
			lease.EXPECT().Release()

			err := svc.SyncProvider(context.Background(), 42, "prov-1")
			So(err, ShouldBeNil)
		})

		Convey("propagates an error when listing the model catalog fails", func() {
			providerRepo.EXPECT().FindByKey(gomock.Any(), "prov-1").Return(provider, nil)
			providerRepo.EXPECT().ListModels(gomock.Any(), int64(0)).Return(nil, errors.New("db boom"))

			err := svc.SyncProvider(context.Background(), 42, "prov-1")
			So(err, ShouldNotBeNil)
		})

		Convey("releases the lease and leaves cache untouched when remote upsert fails", func() {
			lease := svcmock.NewMockLease(ctrl)
			providerRepo.EXPECT().FindByKey(gomock.Any(), "prov-1").Return(provider, nil)
			providerRepo.EXPECT().ListModels(gomock.Any(), int64(0)).Return([]*llm_provider_model_entity.LLMProviderModel{defaultModel}, nil)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().LLMUpsert(gomock.Any(), gomock.Any()).Return(nil, errors.New("remote boom"))
			lease.EXPECT().Release()

			err := svc.SyncProvider(context.Background(), 42, "prov-1")
			So(err, ShouldNotBeNil)
			So(svc.ListDeviceProviders(42), ShouldBeNil)
		})
	})
}
