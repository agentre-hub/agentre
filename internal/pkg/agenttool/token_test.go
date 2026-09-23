package agenttool

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TokenSigner 是内置工具 server 与 agrctl 会话级 token 共用的签发/校验方式：
// 同一实例签的 token 能解回 (agent, session)，别的实例、被篡改的 token 一律验不过。
func TestTokenSigner(t *testing.T) {
	t.Run("同一实例签发 → 解回绑定的 (agent, session)，且确定性", func(t *testing.T) {
		s := NewTokenSigner()
		tok := s.MintToken(7, 42)
		assert.Equal(t, tok, s.MintToken(7, 42), "同一 (agent, session) 必须返回相同 token")
		ref, ok := s.Lookup(tok)
		assert.True(t, ok)
		assert.Equal(t, Ref{AgentID: 7, SessionID: 42}, ref)
	})
	t.Run("另一个实例签的 token 验不过", func(t *testing.T) {
		_, ok := NewTokenSigner().Lookup(NewTokenSigner().MintToken(7, 42))
		assert.False(t, ok)
	})
	t.Run("改了载荷的 token 验不过", func(t *testing.T) {
		s := NewTokenSigner()
		tok := s.MintToken(7, 42)
		_, sig, _ := strings.Cut(tok, ".")
		forged := s.MintToken(7, 43)
		payload, _, _ := strings.Cut(forged, ".")
		_, ok := s.Lookup(payload + "." + sig)
		assert.False(t, ok)
	})
	t.Run("格式非法 → !ok", func(t *testing.T) {
		s := NewTokenSigner()
		for _, tok := range []string{"", "no-dot", "!!!.???"} {
			_, ok := s.Lookup(tok)
			assert.False(t, ok, tok)
		}
	})
}
