package llmurl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
	t.Run("Given an unversioned base URL, when a versioned path is joined, then the path is appended", func(t *testing.T) {
		u, err := Build("https://provider.example", "/v1/messages")
		require.NoError(t, err)
		assert.Equal(t, "https://provider.example/v1/messages", u.String())
	})

	t.Run("Given a base URL ending in v1, when a versioned path is joined, then the shared prefix appears once", func(t *testing.T) {
		u, err := Build("https://provider.example/v1/", "/v1/models")
		require.NoError(t, err)
		assert.Equal(t, "https://provider.example/v1/models", u.String())
	})

	t.Run("Given an OpenAI v1 base URL, when an unversioned endpoint is joined, then the base version is preserved", func(t *testing.T) {
		u, err := Build("https://provider.example/v1", "/chat/completions")
		require.NoError(t, err)
		assert.Equal(t, "https://provider.example/v1/chat/completions", u.String())
	})

	t.Run("Given an invalid base URL, when an endpoint is joined, then a diagnostic error is returned", func(t *testing.T) {
		_, err := Build("not-a-url", "/v1/messages")
		assert.ErrorContains(t, err, "invalid upstream URL")
	})
}

func TestEffectiveBaseURL(t *testing.T) {
	t.Run("Given a configured base URL, when it is resolved, then the configured value is used", func(t *testing.T) {
		assert.Equal(t, "https://proxy.example/v1", EffectiveBaseURL("openai-chat", "https://proxy.example/v1"))
	})

	t.Run("Given an anthropic provider with a blank base URL, when it is resolved, then the Anthropic default is used", func(t *testing.T) {
		assert.Equal(t, "https://api.anthropic.com", EffectiveBaseURL("anthropic", "  "))
	})

	t.Run("Given an OpenAI provider with an empty base URL, when it is resolved, then the OpenAI default is used", func(t *testing.T) {
		assert.Equal(t, "https://api.openai.com/v1", EffectiveBaseURL("openai-response", ""))
		assert.Equal(t, "https://api.openai.com/v1", EffectiveBaseURL("openai-chat", ""))
	})
}
