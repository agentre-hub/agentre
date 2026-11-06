package chat_import_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// TestImport_ReadsTranscriptOutsideTx 读转录(磁盘解析 / 远端 RPC)与回放都不能占着
// 写事务:SQLite 单写者,事务里等 IO 会把其它会话的写入全部挡住。事务里只剩建会话
// 与写消息;消息的 SessionID 在写入时才填(会话号建行后才有)。
func TestImport_ReadsTranscriptOutsideTx(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	tr := twoTurnTranscript()
	turnsReads := 0
	tr.onTurns = func() {
		turnsReads++
		require.False(t, m.tx.inTx, "Turns 不得在写事务内执行")
	}
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: tr})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			require.True(t, m.tx.inTx)
			s.ID = 55
			return nil
		})
	var wrote []*chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			require.True(t, m.tx.inTx)
			wrote = append(wrote, msg)
			return nil
		})
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *chat_entity.Session) error {
			require.True(t, m.tx.inTx)
			return nil
		})

	var progress []int
	resp, err := m.svc.Import(context.Background(), importReq(), func(done, _ int) { progress = append(progress, done) })

	require.NoError(t, err)
	assert.Equal(t, 2, resp.ImportedTurns)
	assert.Equal(t, 1, turnsReads)
	require.Len(t, wrote, 4)
	for _, msg := range wrote {
		assert.Equal(t, int64(55), msg.SessionID, "消息必须挂到事务内建出的会话号上")
	}
	assert.Equal(t, []int{1, 2}, progress)
}

// TestImport_TurnsErrorNeverOpensTx 读转录失败时事务一次都没开过:库里不会有半截会话。
func TestImport_TurnsErrorNeverOpensTx(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	tr := twoTurnTranscript()
	tr.turnsErr = errBoom
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: tr})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)

	resp, err := m.svc.Import(context.Background(), importReq(), nil)

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, 0, m.tx.ran, "读取失败时不得开事务")
}
