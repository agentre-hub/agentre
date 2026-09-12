package llm_provider_model_entity

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
)

func TestLLMProviderModelCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("nil receiver rejects", func(t *testing.T) {
		var m *LLMProviderModel
		assert.Error(t, m.Check(ctx))
	})

	t.Run("empty model_key rejects", func(t *testing.T) {
		m := &LLMProviderModel{ModelID: "claude-sonnet-4-6"}
		assert.Error(t, m.Check(ctx))
	})

	t.Run("empty model_id rejects", func(t *testing.T) {
		m := &LLMProviderModel{ModelKey: "mk-1"}
		assert.Error(t, m.Check(ctx))
	})

	t.Run("valid model passes", func(t *testing.T) {
		m := &LLMProviderModel{ModelKey: "mk-1", ModelID: "claude-sonnet-4-6"}
		assert.NoError(t, m.Check(ctx))
	})
}

func TestLLMProviderModelDisplayName(t *testing.T) {
	t.Run("name wins when set", func(t *testing.T) {
		m := &LLMProviderModel{ModelID: "claude-sonnet-4-6", Name: "Sonnet 4.6"}
		assert.Equal(t, "Sonnet 4.6", m.DisplayName())
	})

	t.Run("empty name falls back to model_id", func(t *testing.T) {
		m := &LLMProviderModel{ModelID: "claude-sonnet-4-6"}
		assert.Equal(t, "claude-sonnet-4-6", m.DisplayName())
	})

	t.Run("nil receiver is empty", func(t *testing.T) {
		assert.Equal(t, "", (*LLMProviderModel)(nil).DisplayName())
	})
}

func TestLLMProviderModelState(t *testing.T) {
	t.Run("IsActive only when status ACTIVE", func(t *testing.T) {
		assert.True(t, (&LLMProviderModel{Status: consts.ACTIVE}).IsActive())
		assert.False(t, (&LLMProviderModel{Status: consts.DELETE}).IsActive())
		assert.False(t, (*LLMProviderModel)(nil).IsActive())
	})

	t.Run("IsEnabled reflects independent enabled flag", func(t *testing.T) {
		assert.True(t, (&LLMProviderModel{Enabled: EnabledOn}).IsEnabled())
		assert.False(t, (&LLMProviderModel{Enabled: EnabledOff}).IsEnabled())
		assert.False(t, (*LLMProviderModel)(nil).IsEnabled())
	})

	t.Run("TableName binds llm_provider_models", func(t *testing.T) {
		assert.Equal(t, "llm_provider_models", (&LLMProviderModel{}).TableName())
	})
}
