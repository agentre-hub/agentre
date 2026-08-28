package llm_provider_svc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

func TestResolveTarget(t *testing.T) {
	convey.Convey("ResolveTarget", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("空 ModelKey 解析当前启用的默认模型，返回执行配置", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Type: "anthropic", APIKey: "sk-clear", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", ContextWindow: 200000, MaxOutput: 64000, Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)

			got, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk"})
			assert.NoError(t, err)
			assert.Equal(t, "pk", got.ProviderKey)
			assert.Equal(t, "mk1", got.ModelKey)
			assert.Equal(t, "anthropic", got.ProviderType)
			assert.Equal(t, "claude-sonnet-4-6", got.ModelID)
			assert.Equal(t, 200000, got.ContextWindow)
			assert.Equal(t, 64000, got.MaxOutput)
			// 执行侧契约携带明文 key；BaseURL 默认值由消费侧填充
			assert.Equal(t, "sk-clear", got.APIKey)
			assert.True(t, got.HasAPIKey)
		})

		convey.Convey("具体 ModelKey 只解析该启用且归属的模型", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Type: "openai-chat", APIKey: "sk", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk2").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk2", ModelID: "gpt-5", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)

			got, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk2"})
			assert.NoError(t, err)
			assert.Equal(t, "mk2", got.ModelKey)
			assert.Equal(t, "gpt-5", got.ModelID)
		})

		convey.Convey("Provider 不存在", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(nil, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk1"})
			assertCode(t, err, code.LLMProviderNotFound)
		})

		convey.Convey("Provider 已停用（provider-default）", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk"})
			assertCode(t, err, code.LLMProviderDisabled)
		})

		convey.Convey("Provider 已停用（fixed-model）也严格阻止", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOff, Status: 1,
			}, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk1"})
			assertCode(t, err, code.LLMProviderDisabled)
		})

		convey.Convey("Provider 存在但未配置默认模型", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOn, Status: 1,
			}, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk"})
			assertCode(t, err, code.LLMProviderDefaultModelInvalid)
		})

		convey.Convey("默认模型已停用", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", Enabled: llm_provider_model_entity.EnabledOff, Status: 1,
			}, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk"})
			assertCode(t, err, code.LLMProviderDefaultModelInvalid)
		})

		convey.Convey("fixed-model 模型不存在", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(nil, nil)
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelNotFound)
		})

		convey.Convey("fixed-model 归属错误", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 2, ModelKey: "mk9", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			// 该子测试的 provider 夹具 ID=1，归属判定用 ID != ProviderID(2) → NotOwned。
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelNotOwned)
		})

		convey.Convey("fixed-model 模型已停用", func() {
			mockRepo.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
				ID: 1, ProviderKey: "pk", Enabled: llm_provider_entity.EnabledOn, Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk9").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk9", Enabled: llm_provider_model_entity.EnabledOff, Status: 1,
			}, nil)
			// 该子测试的 provider 夹具 ID=1，与模型 ProviderID=1 匹配（归属通过）→ 命中停用错误。
			_, err := svc.ResolveTarget(ctx, ModelTarget{ProviderKey: "pk", ModelKey: "mk9"})
			assertCode(t, err, code.LLMProviderModelDisabled)
		})
	})
}

func TestTestConnection(t *testing.T) {
	convey.Convey("TestConnection（同一真实调用能力）", t, func() {
		ctx, mockRepo, doer, svc := setupSvcTest(t)

		convey.Convey("已保存 Provider、空 ModelKey → 测试当前默认模型", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Type: "anthropic", APIKey: "test-ant-key", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			doer.respond(200, `{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`)

			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{ID: 1})
			assert.NoError(t, err)
			assert.True(t, resp.OK)
			assert.Equal(t, "test-ant-key", doer.last.Header.Get("x-api-key"))
			var payload struct {
				Model string `json:"model"`
			}
			assert.NoError(t, json.NewDecoder(doer.last.Body).Decode(&payload))
			assert.Equal(t, "claude-sonnet-4-6", payload.Model)
		})

		convey.Convey("已保存 Provider、具体 ModelKey → 测试该子模型", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Type: "openai-chat", APIKey: "test-openai-key", BaseURL: "http://localhost:11434/v1", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk2").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk2", ModelID: "llama3.2", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			doer.respond(200, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)

			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{ID: 1, ModelKey: "mk2"})
			assert.NoError(t, err)
			assert.True(t, resp.OK)
			var payload struct {
				Model string `json:"model"`
			}
			assert.NoError(t, json.NewDecoder(doer.last.Body).Decode(&payload))
			assert.Equal(t, "llama3.2", payload.Model)
		})

		convey.Convey("已保存 Provider 未配置默认模型 → OK=false", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Type: "openai-chat", APIKey: "test-key", Status: 1,
			}, nil)
			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{ID: 1})
			assert.NoError(t, err)
			assert.False(t, resp.OK)
			assert.Contains(t, resp.Message, "默认模型")
		})

		convey.Convey("草稿配置按 ModelID 测试", func() {
			doer.respond(200, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{
				UseDraft: true,
				Type:     "openai-chat",
				APIKey:   "test-draft-key",
				BaseURL:  "http://localhost:11434/v1",
				ModelID:  "llama3.2",
			})
			assert.NoError(t, err)
			assert.True(t, resp.OK)
			var payload struct {
				Model string `json:"model"`
			}
			assert.NoError(t, json.NewDecoder(doer.last.Body).Decode(&payload))
			assert.Equal(t, "llama3.2", payload.Model)
		})

		convey.Convey("草稿配置未指定 ModelID → OK=false", func() {
			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{UseDraft: true, Type: "openai-chat", APIKey: "k"})
			assert.NoError(t, err)
			assert.False(t, resp.OK)
		})

		convey.Convey("openai-response 草稿按 ModelID 走 /responses", func() {
			doer.respond(200, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`)
			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{
				UseDraft: true,
				Type:     "openai-response",
				APIKey:   "test-response-key",
				BaseURL:  "https://api.openai.com/v1",
				ModelID:  "gpt-5-codex",
			})
			assert.NoError(t, err)
			assert.True(t, resp.OK)
			assert.True(t, strings.HasSuffix(doer.last.URL.Path, "/responses"))
		})

		convey.Convey("上游失败 → OK=false 并携带原因", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Type: "openai-chat", APIKey: "bad", DefaultModelKey: "mk1", Status: 1,
			}, nil)
			mockRepo.EXPECT().FindModelByKey(gomock.Any(), "mk1").Return(&llm_provider_model_entity.LLMProviderModel{
				ProviderID: 1, ModelKey: "mk1", ModelID: "gpt-4o", Enabled: llm_provider_model_entity.EnabledOn, Status: 1,
			}, nil)
			doer.err = errors.New("dial tcp: i/o timeout")
			resp, err := svc.TestConnection(ctx, &TestConnectionRequest{ID: 1})
			assert.NoError(t, err)
			assert.False(t, resp.OK)
			assert.Contains(t, resp.Message, "i/o timeout")
		})
	})
}

func TestProviderModelRefCounts(t *testing.T) {
	convey.Convey("引用影响计数", t, func() {
		ctx, mockRepo, _, svc := setupSvcTest(t)

		convey.Convey("Provider 引用计数透传三路", func() {
			mockRepo.EXPECT().CountProviderReferences(gomock.Any(), "pk").Return(llm_provider_repo.ProviderRefCounts{
				Backends: 1, Sessions: 2, Routes: 3,
			}, nil)
			resp, err := svc.ProviderRefCounts(ctx, &ProviderRefCountsRequest{ProviderKey: "pk"})
			assert.NoError(t, err)
			assert.Equal(t, int64(1), resp.Counts.Backends)
			assert.Equal(t, int64(2), resp.Counts.Sessions)
			assert.Equal(t, int64(3), resp.Counts.Routes)
		})

		convey.Convey("Model 引用计数透传三路", func() {
			mockRepo.EXPECT().CountModelReferences(gomock.Any(), "mk").Return(llm_provider_repo.ModelRefCounts{
				Backends: 0, Sessions: 1, Routes: 0,
			}, nil)
			resp, err := svc.ModelRefCounts(ctx, &ModelRefCountsRequest{ModelKey: "mk"})
			assert.NoError(t, err)
			assert.Equal(t, int64(1), resp.Counts.Sessions)
		})
	})
}

func TestPreviewModelsAnthropic(t *testing.T) {
	convey.Convey("PreviewModels 作为发现建议（瞬时，不落库）", t, func() {
		ctx, mockRepo, doer, svc := setupSvcTest(t)

		convey.Convey("命中 cago 目录回填元数据，未命中只带 id + vendor", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(&llm_provider_entity.LLMProvider{
				ID: 1, Type: "anthropic", APIKey: "test-ant-key", Status: 1,
			}, nil)
			doer.respond(200, `{"data":[{"id":"claude-opus-4-7"},{"id":"unknown-model"}]}`)

			resp, err := svc.PreviewModels(ctx, &PreviewModelsRequest{ID: 1, Type: "anthropic"})
			assert.NoError(t, err)
			assert.Len(t, resp.Items, 2)
			assert.Equal(t, "claude-opus-4-7", resp.Items[0].ID)
			assert.True(t, resp.Items[0].KnownInCago)
			assert.Equal(t, "anthropic", resp.Items[0].Vendor)
			assert.False(t, resp.Items[1].KnownInCago)
			assert.Equal(t, "anthropic", resp.Items[1].Vendor)
			assert.Equal(t, "test-ant-key", doer.last.Header.Get("x-api-key"))
		})
	})
}

func TestPreviewModelsDraftEditKeepsSavedAPIKey(t *testing.T) {
	t.Run("Given an edited provider and an empty draft key, when models are fetched, then the draft URL and saved key are used", func(t *testing.T) {
		ctx, mockRepo, doer, svc := setupSvcTest(t)
		mockRepo.EXPECT().Find(gomock.Any(), int64(23)).Return(&llm_provider_entity.LLMProvider{
			ID:      23,
			Type:    "anthropic",
			APIKey:  "test-saved-key",
			BaseURL: "https://old.example/v1",
			Status:  1,
		}, nil)
		doer.respond(200, `{"data":[{"id":"glm-test-model"}]}`)

		var req PreviewModelsRequest
		assert.NoError(t, json.Unmarshal([]byte(`{
			"id": 23,
			"type": "anthropic",
			"apiKey": "",
			"baseUrl": "https://new.example/v1"
		}`), &req))
		resp, err := svc.PreviewModels(ctx, &req)

		assert.NoError(t, err)
		assert.Len(t, resp.Items, 1)
		assert.Equal(t, "https://new.example/v1/models", doer.last.URL.String())
		assert.Equal(t, "test-saved-key", doer.last.Header.Get("x-api-key"))
	})
}

func TestAnthropicCustomBaseURLKeepsSingleV1Prefix(t *testing.T) {
	t.Run("Given a base URL ending in /v1, when models are previewed, then /v1 is not duplicated", func(t *testing.T) {
		ctx, mockRepo, doer, svc := setupSvcTest(t)
		mockRepo.EXPECT().Find(gomock.Any(), int64(21)).Return(&llm_provider_entity.LLMProvider{ //nolint:gosec // credential-shaped API key is a test fixture.
			ID:      21,
			Type:    "anthropic",
			APIKey:  "test-anthropic-key",
			BaseURL: "https://glm.example/v1",
			Status:  1,
		}, nil)
		doer.respond(200, `{"data":[{"id":"glm-test-model"}]}`)

		resp, err := svc.PreviewModels(ctx, &PreviewModelsRequest{ID: 21, Type: "anthropic"})

		assert.NoError(t, err)
		assert.Len(t, resp.Items, 1)
		assert.Equal(t, "https://glm.example/v1/models", doer.last.URL.String())
	})
}

func TestPreviewModelsUpstreamError(t *testing.T) {
	ctx, mockRepo, doer, svc := setupSvcTest(t)
	mockRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(&llm_provider_entity.LLMProvider{
		ID: 2, Type: "openai-chat", APIKey: "bad", Status: 1,
	}, nil)
	doer.respond(401, `{"error":"invalid api key"}`)
	_, err := svc.PreviewModels(ctx, &PreviewModelsRequest{ID: 2, Type: "openai-chat"})
	assert.Error(t, err)
}

func TestLookupModel(t *testing.T) {
	ctx, _, _, svc := setupSvcTest(t)
	resp, err := svc.LookupModel(ctx, &LookupModelRequest{ID: "definitely-not-a-real-model"})
	assert.NoError(t, err)
	assert.False(t, resp.Known)

	resp2, err := svc.LookupModel(ctx, &LookupModelRequest{ID: ""})
	assert.NoError(t, err)
	assert.False(t, resp2.Known)
}

var _ context.Context // keep context import used when helper set is trimmed
