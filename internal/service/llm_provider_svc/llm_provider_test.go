package llm_provider_svc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/google/uuid"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-ai/agentre/internal/pkg/code"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

type fakeDoer struct {
	last   *http.Request
	status int
	body   string
	err    error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(f.body)),
		Header:     make(http.Header),
	}, nil
}

func (f *fakeDoer) respond(status int, body string) {
	f.status = status
	f.body = body
}

func setupSvcTest(t *testing.T) (
	context.Context,
	*mock_llm_provider_repo.MockLLMProviderRepo,
	*fakeDoer,
	*llmProviderSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRepo := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	llm_provider_repo.RegisterLLMProvider(mockRepo)

	doer := &fakeDoer{}
	svc := &llmProviderSvc{http: doer, now: func() int64 { return 1234567890 }}
	return context.Background(), mockRepo, doer, svc
}

func assertCode(t *testing.T, err error, want int) {
	t.Helper()
	assert.Error(t, err)
	var httpErr *httputils.Error
	assert.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func TestCreateProvider(t *testing.T) {
	convey.Convey("Create LLM provider", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("带 Models + 默认模型创建：原子写 Provider+Models+默认，Provider 启用", func() {
			var gotKey string
			mockRepo.EXPECT().FindByName(gomock.Any(), "production").Return(nil, nil)
			mockRepo.EXPECT().CreateWithModels(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{}), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel, defaultKey string) error {
					gotKey = defaultKey
					p.ID = 7
					assert.Equal(t, llm_provider_entity.EnabledOn, p.Enabled)
					assert.Len(t, models, 2)
					for _, m := range models {
						_, parseErr := uuid.Parse(m.ModelKey)
						assert.NoError(t, parseErr, "ModelKey should be a valid UUID")
					}
					// 默认 key 必须等于默认模型（ModelID=claude-sonnet-4-6）的 key
					assert.Equal(t, "claude-sonnet-4-6", models[0].ModelID)
					assert.Equal(t, models[0].ModelKey, defaultKey)
					return nil
				})

			resp, err := svc.Create(ctx, &CreateProviderRequest{
				Type:   "anthropic",
				Name:   "production",
				APIKey: "test-ant-key-1234",
				Models: []*ModelInput{
					{ModelID: "claude-sonnet-4-6", Name: "Sonnet"},
					{ModelID: "claude-opus-4-7"},
				},
				DefaultModelID: "claude-sonnet-4-6",
			})
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, int64(7), resp.Item.ID)
			assert.True(t, resp.Item.Enabled)
			assert.Equal(t, gotKey, resp.Item.DefaultModelKey)
			assert.True(t, resp.Item.HasAPIKey)
			// 掩码 key 不暴露明文
			assert.NotContains(t, resp.Item.MaskedAPIKey, "test-ant")
		})

		convey.Convey("无默认模型时 Provider 以停用态创建", func() {
			mockRepo.EXPECT().FindByName(gomock.Any(), "offline").Return(nil, nil)
			mockRepo.EXPECT().CreateWithModels(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{}), gomock.Any(), "").
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, _ []*llm_provider_model_entity.LLMProviderModel, _ string) error {
					p.ID = 8
					assert.Equal(t, llm_provider_entity.EnabledOff, p.Enabled)
					return nil
				})
			resp, err := svc.Create(ctx, &CreateProviderRequest{Type: "openai-chat", Name: "offline"})
			assert.NoError(t, err)
			assert.False(t, resp.Item.Enabled)
		})

		convey.Convey("DefaultModelID 不在 Models 中 → 参数错误（不落库）", func() {
			_, err := svc.Create(ctx, &CreateProviderRequest{
				Type:           "openai-chat",
				Name:           "x",
				Models:         []*ModelInput{{ModelID: "a"}},
				DefaultModelID: "b",
			})
			assertCode(t, err, code.InvalidParameter)
		})

		convey.Convey("名称重复返回错误", func() {
			mockRepo.EXPECT().FindByName(gomock.Any(), "dup").Return(&llm_provider_entity.LLMProvider{ID: 1, Name: "dup"}, nil)
			_, err := svc.Create(ctx, &CreateProviderRequest{Type: "openai-chat", Name: "dup", APIKey: "k"})
			assertCode(t, err, code.LLMProviderNameDuplicated)
		})

		convey.Convey("不支持的类型被拒绝", func() {
			_, err := svc.Create(ctx, &CreateProviderRequest{Type: "google", Name: "x"})
			assertCode(t, err, code.LLMProviderInvalidType)
		})
	})
}

func TestListProviders(t *testing.T) {
	convey.Convey("List providers", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("返回全部供应商并按 provider 计数填充 ModelCount", func() {
			mockRepo.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
				{ID: 1, ProviderKey: "pk-1", Name: "one", Status: consts.ACTIVE},
				{ID: 2, ProviderKey: "pk-2", Name: "two", Status: consts.ACTIVE},
			}, nil)
			mockRepo.EXPECT().CountModelsByProvider(gomock.Any(), []int64{1, 2}).
				Return(map[int64]int64{1: 3, 2: 0}, nil).
				Times(1)

			resp, err := svc.List(ctx, &ListProvidersRequest{})
			assert.NoError(t, err)
			assert.Len(t, resp.Items, 2)
			assert.Equal(t, int64(3), resp.Items[0].ModelCount)
			assert.Equal(t, int64(0), resp.Items[1].ModelCount)
		})

		convey.Convey("计数查询失败时透传错误", func() {
			mockRepo.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
				{ID: 1, ProviderKey: "pk-1", Name: "one", Status: consts.ACTIVE},
			}, nil)
			mockRepo.EXPECT().CountModelsByProvider(gomock.Any(), []int64{1}).
				Return(nil, errors.New("count boom"))

			_, err := svc.List(ctx, &ListProvidersRequest{})
			assert.EqualError(t, err, "count boom")
		})
	})
}

func TestUpdateProvider(t *testing.T) {
	convey.Convey("Update LLM provider", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("APIKey 为空时保留原值", func() {
			existing := &llm_provider_entity.LLMProvider{
				ID: 3, Type: "openai-chat", Name: "old", APIKey: "old-key", Status: 1,
			}
			mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(existing, nil)
			mockRepo.EXPECT().FindByName(gomock.Any(), "new").Return(nil, nil)
			mockRepo.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
					assert.Equal(t, "old-key", p.APIKey)
					assert.Equal(t, "new", p.Name)
					return nil
				})
			resp, err := svc.Update(ctx, &UpdateProviderRequest{ID: 3, Name: "new"})
			assert.NoError(t, err)
			assert.Equal(t, "new", resp.Item.Name)
		})

		convey.Convey("供应商不存在", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Update(ctx, &UpdateProviderRequest{ID: 99, Name: "x"})
			assertCode(t, err, code.LLMProviderNotFound)
		})
	})
}

func TestSetProviderEnabled(t *testing.T) {
	convey.Convey("SetProviderEnabled", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("已有启用默认模型 → 启用成功", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
					assert.Equal(t, llm_provider_entity.EnabledOn, p.Enabled)
					return nil
				})
			resp, err := svc.SetProviderEnabled(ctx, &SetProviderEnabledRequest{ID: 1, Enabled: true})
			assert.NoError(t, err)
			assert.True(t, resp.Item.Enabled)
		})

		convey.Convey("未设默认模型 → 拒绝启用", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Enabled: llm_provider_entity.EnabledOff, Status: 1,
			}, nil)
			_, err := svc.SetProviderEnabled(ctx, &SetProviderEnabledRequest{ID: 1, Enabled: true})
			assertCode(t, err, code.LLMProviderNoEnabledDefault)
		})

		convey.Convey("默认模型已停用 → 拒绝启用", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOff, Status: 1,
			}, nil)
			_, err := svc.SetProviderEnabled(ctx, &SetProviderEnabledRequest{ID: 1, Enabled: true})
			assertCode(t, err, code.LLMProviderNoEnabledDefault)
		})

		convey.Convey("被引用的供应商允许停用（不做引用检查）", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
					assert.Equal(t, llm_provider_entity.EnabledOff, p.Enabled)
					return nil
				})
			resp, err := svc.SetProviderEnabled(ctx, &SetProviderEnabledRequest{ID: 1, Enabled: false})
			assert.NoError(t, err)
			assert.False(t, resp.Item.Enabled)
		})
	})
}

func TestDeleteProvider(t *testing.T) {
	convey.Convey("Delete LLM provider", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("无引用 → 删除 Provider 及其全部 Models", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountProviderReferences(gomock.Any(), "pk").Return(llm_provider_repo.ProviderRefCounts{}, nil)
			mockRepo.EXPECT().DeleteWithModels(gomock.Any(), int64(1)).Return(nil)
			_, err := svc.Delete(ctx, &DeleteProviderRequest{ID: 1})
			assert.NoError(t, err)
		})

		convey.Convey("被 Backend 引用 + 未确认 → 要求二次确认，不删", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountProviderReferences(gomock.Any(), "pk").Return(llm_provider_repo.ProviderRefCounts{
				Backends: 1,
			}, nil)
			_, err := svc.Delete(ctx, &DeleteProviderRequest{ID: 1})
			assertCode(t, err, code.LLMProviderReferenced)
		})

		convey.Convey("被 Backend / 会话 / 路由引用 + 已确认 → 照删，引用不阻止删除", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountProviderReferences(gomock.Any(), "pk").Return(llm_provider_repo.ProviderRefCounts{
				Backends: 1, Sessions: 2, Routes: 3,
			}, nil)
			mockRepo.EXPECT().DeleteWithModels(gomock.Any(), int64(1)).Return(nil)
			_, err := svc.Delete(ctx, &DeleteProviderRequest{ID: 1, ConfirmReference: true})
			assert.NoError(t, err)
		})

		convey.Convey("供应商不存在", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Delete(ctx, &DeleteProviderRequest{ID: 99})
			assertCode(t, err, code.LLMProviderNotFound)
		})
	})
}

func TestListModelsPersisted(t *testing.T) {
	convey.Convey("ListModels（持久化）", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("返回已持久化模型并标记 isDefault / enabled，不含凭证", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().ListModels(gomock.Any(), int64(1)).Return([]*llm_provider_model_entity.LLMProviderModel{
				{ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: 1},
				{ID: 2, ProviderID: 1, ModelKey: "mk2", ModelID: "claude-opus-4-7", Enabled: llm_provider_model_entity.EnabledOff, Status: 1},
			}, nil)

			resp, err := svc.ListModels(ctx, &ListModelsRequest{ID: 1})
			assert.NoError(t, err)
			assert.Len(t, resp.Items, 2)
			assert.Equal(t, "pk", resp.Items[0].ProviderKey)
			assert.True(t, resp.Items[0].IsDefault)
			assert.False(t, resp.Items[1].IsDefault)
			assert.False(t, resp.Items[1].Enabled)
		})

		convey.Convey("供应商不存在", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.ListModels(ctx, &ListModelsRequest{ID: 99})
			assertCode(t, err, code.LLMProviderNotFound)
		})
	})
}

func TestImportModels(t *testing.T) {
	convey.Convey("ImportModels", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("新模型导入 + 已存在模型保留原 key 且不覆盖非空元数据", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().ListModels(gomock.Any(), int64(1)).Return([]*llm_provider_model_entity.LLMProviderModel{
				{ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", Name: "user-name", ContextWindow: 200000, Enabled: llm_provider_model_entity.EnabledOn, Status: 1},
			}, nil)
			mockRepo.EXPECT().ImportModels(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, updates, inserts []*llm_provider_model_entity.LLMProviderModel) error {
					// 已存在行的 name 非空 → 无补齐；只有 1 条新模型进 inserts
					assert.Len(t, updates, 0)
					assert.Len(t, inserts, 1)
					assert.Equal(t, "claude-opus-4-7", inserts[0].ModelID)
					_, parseErr := uuid.Parse(inserts[0].ModelKey)
					assert.NoError(t, parseErr)
					assert.Equal(t, llm_provider_model_entity.EnabledOn, inserts[0].Enabled)
					return nil
				})

			resp, err := svc.ImportModels(ctx, &ImportModelsRequest{
				ProviderID: 1,
				Models: []*ModelInput{
					// 已存在：新 name 不能覆盖用户维护的 name；不改 key
					{ModelID: "claude-sonnet-4-6", Name: "upstream-name"},
					// 新发现
					{ModelID: "claude-opus-4-7", Name: "Opus"},
				},
			})
			assert.NoError(t, err)
			assert.Equal(t, 1, resp.Imported)
			assert.Equal(t, 0, resp.Updated)
			assert.Len(t, resp.Items, 2)
			assert.Equal(t, "mk1", resp.Items[0].ModelKey)
			assert.Equal(t, "user-name", resp.Items[0].Name)
		})

		convey.Convey("本地字段为空时用提交值补齐，与新增同走一次原子调用", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().ListModels(gomock.Any(), int64(1)).Return([]*llm_provider_model_entity.LLMProviderModel{
				{ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: 1},
			}, nil)
			mockRepo.EXPECT().ImportModels(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, updates, inserts []*llm_provider_model_entity.LLMProviderModel) error {
					assert.Len(t, updates, 1)
					assert.Len(t, inserts, 0)
					// 稳定 key 保留，仅补齐空字段
					assert.Equal(t, "mk1", updates[0].ModelKey)
					assert.Equal(t, "Sonnet", updates[0].Name)
					assert.Equal(t, 128000, updates[0].ContextWindow)
					return nil
				})

			resp, err := svc.ImportModels(ctx, &ImportModelsRequest{
				ProviderID: 1,
				Models: []*ModelInput{
					{ModelID: "claude-sonnet-4-6", Name: "Sonnet", ContextWindow: 128000},
				},
			})
			assert.NoError(t, err)
			assert.Equal(t, 0, resp.Imported)
			assert.Equal(t, 1, resp.Updated)
		})

		convey.Convey("批量插入失败时已存在行补齐不残留：补齐+新增同一次原子调用整体回滚", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().ListModels(gomock.Any(), int64(1)).Return([]*llm_provider_model_entity.LLMProviderModel{
				// 空 name/context_window → 需要补齐
				{ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: 1},
			}, nil)
			// 回归：只允许一次 ImportModels 原子调用；若实现仍分别 UpdateModel + BatchImportModels，
			// 该 expectation 之外的任何调用都会使 mock 失败 → RED。
			mockRepo.EXPECT().ImportModels(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, updates, inserts []*llm_provider_model_entity.LLMProviderModel) error {
					assert.Len(t, updates, 1)
					assert.Len(t, inserts, 1)
					return errors.New("dup model_id")
				})

			resp, err := svc.ImportModels(ctx, &ImportModelsRequest{
				ProviderID: 1,
				Models: []*ModelInput{
					{ModelID: "claude-sonnet-4-6", Name: "Sonnet"},
					{ModelID: "claude-opus-4-7", Name: "Opus"},
				},
			})
			assert.Nil(t, resp)
			assert.EqualError(t, err, "dup model_id")
		})
	})
}

func TestUpdateModel(t *testing.T) {
	convey.Convey("UpdateModel", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("未引用模型改 ModelID → 直接更新", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "a", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{}, nil)
			mockRepo.EXPECT().UpdateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
				DoAndReturn(func(_ context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
					assert.Equal(t, "b", m.ModelID)
					assert.Equal(t, "mk1", m.ModelKey)
					return nil
				})
			resp, err := svc.UpdateModel(ctx, &UpdateModelRequest{ID: 1, ModelID: "b"})
			assert.NoError(t, err)
			assert.Equal(t, "b", resp.Item.ModelID)
		})

		convey.Convey("被引用模型改 ModelID 未确认 → 拒绝", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "a", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{Backends: 1}, nil)
			_, err := svc.UpdateModel(ctx, &UpdateModelRequest{ID: 1, ModelID: "b"})
			assertCode(t, err, code.LLMProviderModelConfirmRequired)
		})

		convey.Convey("被引用模型改 ModelID 已确认 → 更新", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", ModelID: "a", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{Sessions: 2}, nil)
			mockRepo.EXPECT().UpdateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
				DoAndReturn(func(_ context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
					assert.Equal(t, "b", m.ModelID)
					return nil
				})
			_, err := svc.UpdateModel(ctx, &UpdateModelRequest{ID: 1, ModelID: "b", ConfirmReference: true})
			assert.NoError(t, err)
		})
	})
}

func TestSetModelDefault(t *testing.T) {
	convey.Convey("SetModelDefault", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("设默认成功并顺带启用 Provider", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "old", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk2").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk2", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
				DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
					assert.Equal(t, "mk2", p.DefaultModelKey)
					assert.Equal(t, llm_provider_entity.EnabledOn, p.Enabled)
					return nil
				})
			resp, err := svc.SetModelDefault(ctx, &SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk2"})
			assert.NoError(t, err)
			assert.Equal(t, "mk2", resp.Item.DefaultModelKey)
			assert.True(t, resp.Item.Enabled)
		})

		convey.Convey("模型不属于该供应商 → 拒绝", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 2, ModelKey: "mk9", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			_, err := svc.SetModelDefault(ctx, &SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelNotOwned)
		})

		convey.Convey("模型已停用 → 拒绝", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk9", Enabled: llm_provider_model_entity.EnabledOff, Status: 1,
			}, nil)
			_, err := svc.SetModelDefault(ctx, &SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelDisabled)
		})

		convey.Convey("模型不存在 → 拒绝", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(nil, nil)
			_, err := svc.SetModelDefault(ctx, &SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelNotFound)
		})
	})
}

func TestSetModelEnabled(t *testing.T) {
	convey.Convey("SetModelEnabled", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("停用非默认模型 → 成功", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "other", Status: 1,
			}, nil)
			mockRepo.EXPECT().UpdateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
				DoAndReturn(func(_ context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
					assert.Equal(t, llm_provider_model_entity.EnabledOff, m.Enabled)
					return nil
				})
			resp, err := svc.SetModelEnabled(ctx, &SetModelEnabledRequest{ID: 1, Enabled: false})
			assert.NoError(t, err)
			assert.False(t, resp.Item.Enabled)
		})

		convey.Convey("停用默认模型 → 拒绝", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			_, err := svc.SetModelEnabled(ctx, &SetModelEnabledRequest{ID: 1, Enabled: false})
			assertCode(t, err, code.LLMProviderModelIsDefault)
		})

		convey.Convey("重新启用模型 → 成功", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOff, Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "other", Status: 1,
			}, nil)
			mockRepo.EXPECT().UpdateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
				DoAndReturn(func(_ context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
					assert.Equal(t, llm_provider_model_entity.EnabledOn, m.Enabled)
					return nil
				})
			resp, err := svc.SetModelEnabled(ctx, &SetModelEnabledRequest{ID: 1, Enabled: true})
			assert.NoError(t, err)
			assert.True(t, resp.Item.Enabled)
		})
	})
}

func TestDeleteModel(t *testing.T) {
	convey.Convey("DeleteModel", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("无引用且非默认 → 删除", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "other", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{}, nil)
			mockRepo.EXPECT().DeleteModel(gomock.Any(), int64(1)).Return(nil)
			_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1})
			assert.NoError(t, err)
		})

		convey.Convey("默认模型 → 拒绝", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1})
			assertCode(t, err, code.LLMProviderModelIsDefault)
		})

		convey.Convey("被引用 + 未确认 → 要求二次确认，不删", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "other", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{Routes: 1}, nil)
			_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1})
			assertCode(t, err, code.LLMProviderModelReferenced)
		})

		convey.Convey("被引用 + 已确认 → 照删，引用不阻止删除", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "other", Status: 1,
			}, nil)
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk1").Return(llm_provider_repo.ModelRefCounts{
				Backends: 1, Sessions: 1, Routes: 1,
			}, nil)
			mockRepo.EXPECT().DeleteModel(gomock.Any(), int64(1)).Return(nil)
			_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1, ConfirmReference: true})
			assert.NoError(t, err)
		})

		convey.Convey("默认模型 + 已确认 → 仍拒绝，二次确认放不开这条", func() {
			mockRepo.EXPECT().FindModel(gomock.Any(), int64(1)).Return(&llm_provider_model_entity.LLMProviderModel{
				ID: 1, ProviderID: 1, ModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			_, err := svc.DeleteModel(ctx, &DeleteModelRequest{ID: 1, ConfirmReference: true})
			assertCode(t, err, code.LLMProviderModelIsDefault)
		})
	})
}
