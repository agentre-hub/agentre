package exec_target_svc_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSessionCwd_Exported(t *testing.T) {
	t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(nil) })
	exec_target_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
		return "/from/resolver", nil
	})
	cwd, err := exec_target_svc.ResolveSessionCwd(context.Background(),
		&chat_entity.Session{ID: 1, AgentID: 7},
		&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
	)
	require.NoError(t, err)
	assert.Equal(t, "/from/resolver", cwd)
}

// TestResolveSessionCwd_PrefersStoredCwd 钉死导入进来的会话怎么找回它的工作目录
// (spec「续跑」:工作目录取磁盘转录里记录的 cwd)。
//
// 导入把磁盘转录记的 cwd 落在 chat_sessions.cwd 上,解析必须先认这一列 ——
// 而且**本地与远端两条路都要**:
//   - 本地:project.Path / AgentCwd 兜底给出的是「这个 agent 的默认目录」,而
//     claude 的 --resume 按 cwd 定位 project 目录,换个目录那条 provider session
//     id 根本不存在;
//   - 远端:自由会话那一支原本返回空串、把兜底权下放给远端 runtime(落到远端机器的
//     <AppDataDir>/agents/<id>),而机器轴导进来的会话正是这一支 —— 不认这一列的话
//     从别的机器导进来的会话永远续不上。
func TestResolveSessionCwd_PrefersStoredCwd(t *testing.T) {
	t.Run("本地档 → 存下的 cwd 压过 CwdResolver", func(t *testing.T) {
		t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(nil) })
		exec_target_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/from/resolver", nil
		})
		cwd, err := exec_target_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 1, AgentID: 7, Cwd: "/disk/transcript/cwd"},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
		)
		require.NoError(t, err)
		assert.Equal(t, "/disk/transcript/cwd", cwd)
	})

	t.Run("远端档的自由会话 → 存下的 cwd 压过「交给远端 runtime 兜底」的空串", func(t *testing.T) {
		cwd, err := exec_target_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 2, AgentID: 7, ProjectID: 0, Cwd: "/box/repo"},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:device-4"},
		)
		require.NoError(t, err)
		assert.Equal(t, "/box/repo", cwd)
	})

	t.Run("没存 cwd → 沿用原来的两条路", func(t *testing.T) {
		t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(nil) })
		exec_target_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/from/resolver", nil
		})
		cwd, err := exec_target_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 3, AgentID: 7},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
		)
		require.NoError(t, err)
		assert.Equal(t, "/from/resolver", cwd)

		cwd, err = exec_target_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 4, AgentID: 7, ProjectID: 0},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:device-4"},
		)
		require.NoError(t, err)
		assert.Empty(t, cwd, "远端自由会话仍把兜底权下放给远端 runtime")
	})
}
