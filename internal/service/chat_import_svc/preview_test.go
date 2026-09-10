package chat_import_svc

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// TestPreview_FirstTurnsOnly 预览是回放的前 N 轮加元信息,不是另一条解析路径:
// 取够就停(不把整份转录解完),并说清后面还有多少轮;缺口在元信息里带上本机文案。
func TestPreview_FirstTurnsOnly(t *testing.T) {
	m := withMocks(t, testCwd)
	tr := twoTurnTranscript()
	tr.meta.Turns = 42 // 磁盘上是一条 42 轮的长会话
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: tr})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)

	got, err := m.svc.Preview(context.Background(), &PreviewRequest{
		Backend: string(agent_backend_entity.TypeClaudeCode),
		Locator: "loc",
		Turns:   1,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, got.PreviewedTurns)
	assert.Equal(t, 41, got.RemainingTurns, "预览末尾要说清后面还有多少轮")
	require.Len(t, got.Messages, 2)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "assistant", got.Messages[1].Role)
	assert.Equal(t, ms(turn0Start), got.Messages[0].Createtime, "时间取磁盘值")
	require.NotEmpty(t, got.Messages[1].Blocks, "预览要拿到投影好的 ChatBlock,而不是原始 blocksJson")
	assert.Equal(t, "text", got.Messages[1].Blocks[0].Type)
	assert.Equal(t, "第一轮回答", got.Messages[1].Blocks[0].Text)

	assert.Equal(t, testCwd, got.Meta.Cwd)
	assert.True(t, got.Meta.CwdExists, "目录还在 → 续跑可用")
	assert.False(t, got.Meta.Imported)
	require.Len(t, got.Meta.Gaps, 1)
	assert.Equal(t, "thinking_unavailable", got.Meta.Gaps[0].Kind)
	assert.NotEmpty(t, got.Meta.Gaps[0].Text, "缺口要给出可读的说明,UI 才有话可说")
	assert.True(t, tr.closed)
}

// TestPreview_OpenFailureIsReported 文件已被删 / 内容损坏到解不出任何一轮时,
// 预览如实报原因(界面据此禁用导入按钮),不返回一份空转录。
func TestPreview_OpenFailureIsReported(t *testing.T) {
	m := withMocks(t)
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, openErr: errBoom})

	_, err := m.svc.Preview(context.Background(), &PreviewRequest{
		Backend: string(agent_backend_entity.TypeClaudeCode),
		Locator: "loc",
	})
	require.Error(t, err)
	assert.NotEmpty(t, strings.TrimSpace(err.Error()))
}
