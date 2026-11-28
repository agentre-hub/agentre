package llm_provider_svc

import (
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

// Given 系统钥匙串写不进去 When 保存（新建 / 编辑）供应商 Then 报「无法写入系统钥匙串」，
// 不把仓储的内部错误原样抛给前端，也没有任何退回明文的路径。
func TestSaveProvider_GivenKeychainUnavailable_ReportsKeychainError(t *testing.T) {
	failure := fmt.Errorf("%w: locked", llm_provider_repo.ErrKeychainUnavailable)

	t.Run("create", func(t *testing.T) {
		ctx, mockRepo, _, svc := setupSvcTest(t)
		mockRepo.EXPECT().FindByName(gomock.Any(), "acme").Return(nil, nil)
		mockRepo.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(failure)

		_, err := svc.Create(ctx, &CreateProviderRequest{Type: "openai-chat", Name: "acme", APIKey: "sk"})

		assertCode(t, err, code.LLMProviderKeychainUnavailable)
	})

	t.Run("update", func(t *testing.T) {
		ctx, mockRepo, _, svc := setupSvcTest(t)
		mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&llm_provider_entity.LLMProvider{ID: 3, Type: "openai-chat", Name: "old", Status: 1}, nil)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(failure)

		_, err := svc.Update(ctx, &UpdateProviderRequest{ID: 3, Name: "old", APIKey: "sk"})

		assertCode(t, err, code.LLMProviderKeychainUnavailable)
	})
}
