package data_svc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

// TestExport_ExportedAtReadsNowAsMilliseconds 固定 dataSvc.now 的单位契约。
// now 与全库时间列同为毫秒 epoch;按秒解读会把它放大一千倍,
// 导出包上的 exportedAt 会写成公元 58000 年的日期。
func TestExport_ExportedAtReadsNowAsMilliseconds(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	providers := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil)

	original := llm_provider_repo.LLMProvider()
	t.Cleanup(func() { llm_provider_repo.RegisterLLMProvider(original) })
	llm_provider_repo.RegisterLLMProvider(providers)

	at := time.Date(2026, 8, 26, 10, 30, 0, 0, time.UTC)
	s := &dataSvc{now: func() int64 { return at.UnixMilli() }}

	res, err := s.Export(context.Background(), &ExportRequest{
		Scopes: []string{string(ScopeLLMProviders)},
	})

	assert.NoError(t, err)
	assert.NotNil(t, res)

	var bundle BundleV1
	assert.NoError(t, json.Unmarshal(res.JSON, &bundle))
	// 比较时刻本身而非字符串: time.UnixMilli 返回本地时区,
	// 断言格式化结果会把测试绑死在跑测机器的时区上。
	parsed, perr := time.Parse(time.RFC3339, bundle.ExportedAt)
	assert.NoError(t, perr)
	assert.True(t, parsed.Equal(at), "exportedAt = %s, want %s", bundle.ExportedAt, at.Format(time.RFC3339))
}
