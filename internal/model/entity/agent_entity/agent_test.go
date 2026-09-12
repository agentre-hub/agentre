package agent_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

func TestAgentCheck(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		input   *Agent
		wantErr bool
	}{
		{"nil receiver", nil, true},
		{"empty name", &Agent{Name: "", DepartmentID: 1, AgentBackendID: 1}, true},
		{"invalid avatar color", &Agent{Name: "x", AvatarColor: "rainbow", DepartmentID: 1, AgentBackendID: 1}, true},
		{"non-system missing placement", &Agent{Name: "x", AgentBackendID: 1}, true},
		{"non-system with both department and parent", &Agent{Name: "x", DepartmentID: 1, ParentAgentID: 2, AgentBackendID: 1}, true},
		{"non-system without backend ok", &Agent{Name: "x", DepartmentID: 1}, false},
		{"non-system happy", &Agent{Name: "Eva", AvatarColor: "agent-2", DepartmentID: 1, AgentBackendID: 1, PromptJSON: "[]", SkillsJSON: "[]"}, false},
		{"non-system extended color happy", &Agent{Name: "Eva", AvatarColor: "agent-16", DepartmentID: 1, AgentBackendID: 1, PromptJSON: "[]", SkillsJSON: "[]"}, false},
		{"non-system parent agent happy", &Agent{Name: "Eva", AvatarColor: "agent-2", ParentAgentID: 1, AgentBackendID: 1, PromptJSON: "[]", SkillsJSON: "[]"}, false},
		{"system zero department ok", &Agent{Name: "CEO", SystemBadge: "DEFAULT", AvatarColor: "agent-1", PromptJSON: "[]", SkillsJSON: "[]"}, false},
		{"system with department rejected", &Agent{Name: "CEO", SystemBadge: "DEFAULT", DepartmentID: 1, PromptJSON: "[]", SkillsJSON: "[]"}, true},
		{"system with parent rejected", &Agent{Name: "CEO", SystemBadge: "DEFAULT", ParentAgentID: 1, PromptJSON: "[]", SkillsJSON: "[]"}, true},
		{"bad prompt json", &Agent{Name: "Eva", DepartmentID: 1, AgentBackendID: 1, PromptJSON: "{", SkillsJSON: "[]"}, true},
		// 技能授权由 AgentExecTarget 校验，SkillsJSON 旧列不属于 Agent 的领域约束；
		// 即便这一列存了坏 JSON，Agent 行本身也不因此判无效。
		{"bad skills json on legacy column does not fail Check", &Agent{Name: "Eva", DepartmentID: 1, AgentBackendID: 1, PromptJSON: "[]", SkillsJSON: "x"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Check(ctx)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAgentPromptRoundtrip(t *testing.T) {
	a := &Agent{}
	a.SetPrompt([]string{"line a", "line b"})
	got := a.GetPrompt()
	assert.Equal(t, []string{"line a", "line b"}, got)

	empty := &Agent{}
	assert.Equal(t, []string{}, empty.GetPrompt())
}

func TestAgentHelpers(t *testing.T) {
	assert.False(t, (*Agent)(nil).IsActive())
	assert.True(t, (&Agent{Status: 1}).IsActive())
	assert.True(t, (&Agent{SystemBadge: "DEFAULT"}).IsSystem())
	assert.False(t, (&Agent{}).IsSystem())
}

func TestAgentEnsureSyncID(t *testing.T) {
	t.Run("默认 CEO 使用固定同步身份", func(t *testing.T) {
		a := &Agent{SystemBadge: SystemBadgeDefault, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "random-id"}}
		a.EnsureSyncID()
		assert.Equal(t, DefaultAgentSyncID, a.SyncID)
	})

	t.Run("普通 Agent 仍生成独立同步身份", func(t *testing.T) {
		a := &Agent{}
		a.EnsureSyncID()
		assert.NotEmpty(t, a.SyncID)
		assert.NotEqual(t, DefaultAgentSyncID, a.SyncID)
	})
}

func TestAgent_PinnedField(t *testing.T) {
	assert.True(t, (&Agent{Pinned: true}).Pinned)
	assert.False(t, (&Agent{}).Pinned)
}

func TestAgentTools(t *testing.T) {
	t.Run("空串/坏 JSON 返回空列表", func(t *testing.T) {
		a := &Agent{}
		require.Equal(t, []AgentToolItem{}, a.GetTools())
		a.ToolsJSON = "{bad"
		require.Equal(t, []AgentToolItem{}, a.GetTools())
	})
	t.Run("SetTools/GetTools round-trip + ToolEnabled", func(t *testing.T) {
		a := &Agent{}
		a.SetTools([]AgentToolItem{{Key: "org", Enabled: true}})
		require.Equal(t, `[{"key":"org","enabled":true}]`, a.ToolsJSON)
		require.True(t, a.ToolEnabled("org"))
		require.False(t, a.ToolEnabled("other"))
		a.SetTools([]AgentToolItem{{Key: "org", Enabled: false}})
		require.False(t, a.ToolEnabled("org")) // 存在但已关闭
		a.SetTools(nil)
		require.Equal(t, `[]`, a.ToolsJSON)
		require.False(t, a.ToolEnabled("org"))
	})
	t.Run("Check 校验 ToolsJSON 必须是 JSON 数组", func(t *testing.T) {
		a := &Agent{Name: "x", DepartmentID: 1, AgentBackendID: 1, ToolsJSON: "{bad"}
		require.Error(t, a.Check(context.Background()))
	})
}
