package llm_provider_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// registerConfigChangeSpy 装配 config:changed 的替身 emitter，用来断言「这五类资源经
// Wails、orgtool、ctl 任一路径写入成功后发出带资源类型的 config:changed（失败不发）」
// （docs/specs/2026-09-22-agrctl-resource-management.md「Real-time refresh」）。
func registerConfigChangeSpy(t *testing.T) *[][]string {
	t.Helper()
	got := &[][]string{}
	sync_svc.SetConfigChangeEmitter(func(kinds []string) {
		*got = append(*got, kinds)
	})
	t.Cleanup(func() { sync_svc.SetConfigChangeEmitter(nil) })
	return got
}

func TestCreateProvider_EmitsConfigChanged(t *testing.T) {
	ctx, mockRepo, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	mockRepo.EXPECT().FindByName(gomock.Any(), "acme").Return(nil, nil)
	mockRepo.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &CreateProviderRequest{Type: "openai-chat", Name: "acme"})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindLLMProvider}}, *got)
}

func TestCreateProvider_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mockRepo, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	mockRepo.EXPECT().FindByName(gomock.Any(), "acme").Return(nil, nil)
	mockRepo.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("db down"))

	_, err := svc.Create(ctx, &CreateProviderRequest{Type: "openai-chat", Name: "acme"})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUpdateProvider_EmitsConfigChanged(t *testing.T) {
	ctx, mockRepo, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	existing := &llm_provider_entity.LLMProvider{ID: 3, Type: "openai-chat", Name: "old", Status: 1}
	mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(existing, nil)
	mockRepo.EXPECT().FindByName(gomock.Any(), "new").Return(nil, nil)
	mockRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &UpdateProviderRequest{ID: 3, Name: "new"})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindLLMProvider}}, *got)
}

func TestDeleteProvider_EmitsConfigChanged(t *testing.T) {
	ctx, mockRepo, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
		ID: 1, ProviderKey: "pk", Status: 1,
	}, nil)
	mockRepo.EXPECT().CountProviderReferences(gomock.Any(), "pk").Return(llm_provider_repo.ProviderRefCounts{}, nil)
	mockRepo.EXPECT().DeleteWithModels(gomock.Any(), int64(1)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteProviderRequest{ID: 1})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindLLMProvider}}, *got)
}

// configChangeCase drives one write method twice against fresh mocks: once where
// the terminal repo write succeeds (must emit exactly [KindLLMProvider]) and once
// where it fails (must not emit at all). This is the "every exported write method"
// enumeration the spec's Real-time refresh section requires — SetProviderEnabled
// and every model-level method (ImportModels/UpdateModel/SetModelDefault/
// SetModelEnabled/DeleteModel) are provider sub-resources (design decision 9), so
// they all report KindLLMProvider, same as Create/Update/Delete above.
type configChangeCase struct {
	name          string
	arrangeOK     func(m *mock_llm_provider_repo.MockLLMProviderRepo)
	arrangeFailOK func(m *mock_llm_provider_repo.MockLLMProviderRepo)
	call          func(ctx context.Context, svc *llmProviderSvc) error
}

func TestLLMProviderSvc_EveryWriteMethod_EmitsConfigChangedOnlyOnSuccess(t *testing.T) {
	cases := []configChangeCase{
		{
			name: "SetProviderEnabled",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "mk1", Status: 1,
				}, nil)
				m.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
					ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "mk1", Status: 1,
				}, nil)
				m.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
					ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.SetProviderEnabled(ctx, &SetProviderEnabledRequest{ID: 1, Enabled: true})
				return err
			},
		},
		{
			name: "ImportModels",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{ID: 1, ProviderKey: "pk", Status: 1}, nil)
				m.EXPECT().ListModels(gomock.Any(), int64(1)).Return(nil, nil)
				m.EXPECT().ImportModels(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{ID: 1, ProviderKey: "pk", Status: 1}, nil)
				m.EXPECT().ListModels(gomock.Any(), int64(1)).Return(nil, nil)
				m.EXPECT().ImportModels(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.ImportModels(ctx, &ImportModelsRequest{
					ProviderID: 1,
					Models:     []*ModelInput{{ModelID: "new-model"}},
				})
				return err
			},
		},
		{
			name: "UpdateModel",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "a", Status: 1,
				}, nil)
				m.EXPECT().UpdateModel(gomock.Any(), gomock.Any()).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "a", Status: 1,
				}, nil)
				m.EXPECT().UpdateModel(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.UpdateModel(ctx, &UpdateModelRequest{ID: 1, Name: "renamed"})
				return err
			},
		},
		{
			name: "SetModelDefault",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "old", Status: 1,
				}, nil)
				m.EXPECT().FindModelByKey(gomock.Any(), "mk2").Return(&llm_provider_model_entity.LLMProviderModel{
					ProviderID: 1, ModelKey: "mk2", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "old", Status: 1,
				}, nil)
				m.EXPECT().FindModelByKey(gomock.Any(), "mk2").Return(&llm_provider_model_entity.LLMProviderModel{
					ProviderID: 1, ModelKey: "mk2", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.SetModelDefault(ctx, &SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk2"})
				return err
			},
		},
		{
			name: "SetModelEnabled",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, DefaultModelKey: "other", Status: 1,
				}, nil)
				m.EXPECT().UpdateModel(gomock.Any(), gomock.Any()).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
				}, nil)
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, DefaultModelKey: "other", Status: 1,
				}, nil)
				m.EXPECT().UpdateModel(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.SetModelEnabled(ctx, &SetModelEnabledRequest{ID: 1, Enabled: false})
				return err
			},
		},
		{
			name: "DeleteModel",
			arrangeOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
				}, nil)
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, DefaultModelKey: "other", Status: 1,
				}, nil)
				m.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{}, nil)
				m.EXPECT().DeleteModel(gomock.Any(), int64(1)).Return(nil)
			},
			arrangeFailOK: func(m *mock_llm_provider_repo.MockLLMProviderRepo) {
				m.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
					ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
				}, nil)
				m.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
					ID: 1, DefaultModelKey: "other", Status: 1,
				}, nil)
				m.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{}, nil)
				m.EXPECT().DeleteModel(gomock.Any(), int64(1)).Return(errors.New("db down"))
			},
			call: func(ctx context.Context, svc *llmProviderSvc) error {
				_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/success emits KindLLMProvider", func(t *testing.T) {
			ctx, mockRepo, _, svc := setupSvcTest(t)
			got := registerConfigChangeSpy(t)
			tc.arrangeOK(mockRepo)

			err := tc.call(ctx, svc)

			assert.NoError(t, err)
			assert.Equal(t, [][]string{{syncwire.KindLLMProvider}}, *got)
		})
		t.Run(tc.name+"/repo failure emits nothing", func(t *testing.T) {
			ctx, mockRepo, _, svc := setupSvcTest(t)
			got := registerConfigChangeSpy(t)
			tc.arrangeFailOK(mockRepo)

			err := tc.call(ctx, svc)

			assert.Error(t, err)
			assert.Empty(t, *got)
		})
	}
}
