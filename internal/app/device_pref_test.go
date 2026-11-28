package app

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
)

// 设备相关绑定的业务错误也要带着 `agentre-code:<码>` 过桥，前端据此分文案。
// 「只有这些设备」却没选设备 = LLMProviderReachInvalid（12064）。
func TestSetLLMProviderReach_CarriesTheBusinessCode(t *testing.T) {
	a := &App{ctx: context.Background()}
	ctrl := gomock.NewController(t)
	repo := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	repo.EXPECT().Find(gomock.Any(), int64(3)).Return(&llm_provider_entity.LLMProvider{ID: 3, Status: consts.ACTIVE}, nil)
	prev := llm_provider_repo.LLMProvider()
	llm_provider_repo.RegisterLLMProvider(repo)
	t.Cleanup(func() { llm_provider_repo.RegisterLLMProvider(prev) })

	_, err := a.SetLLMProviderReach(&llm_provider_svc.SetProviderReachRequest{ID: 3, Mode: llm_provider_entity.ReachDevices})
	if !bridgeErrorMatches(err, "agentre-code:12064 ") {
		t.Fatalf("expected the reach business code across the bridge, got %v", err)
	}
}
