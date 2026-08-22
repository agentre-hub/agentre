package piagent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedTurn 是一轮最小的完整帧序列:开轮读状态 → prompt 被受理 → 收尾 → 终态元数据。
func scriptedTurn() []string {
	return []string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-1"}}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
	}
}

// Given 一个会话上刚跑完一轮, When 同一个会话上再跑一轮, Then 两轮由同一个 RPC 进程
// 服务 —— 没有第二次 spawn。
//
// pi 此前每轮起一个进程、轮末就关(进程归 Stream 所有,Client 只是工厂),所以它既进不了
// CLISessionPool,也在每轮开头付一次进程启动 + 扩展加载的代价。跨轮复用的前提就是这条:
// 进程活过 Stream,由会话拥有。
func TestSession_GivenACompletedTurn_WhenTheNextTurnRuns_ThenOneRPCProcessServesBoth(t *testing.T) {
	lines := append(scriptedTurn(), scriptedTurn()...)
	client, _, runner := newSingleProcessCaptureClient(strings.Join(append(lines, ""), "\n"))

	session, err := client.OpenSession(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	for _, prompt := range []string{"one", "two"} {
		stream, streamErr := session.Stream(context.Background(), prompt)
		require.NoError(t, streamErr, "第二轮起不来说明进程已经被上一轮关掉了")
		for stream.Next() {
		}
		require.NoError(t, stream.Err())
	}

	assert.Equal(t, 1, runner.starts, "跨轮复用的前提是只 spawn 一次")
}

// Given 一个会话已经被关掉, When 还想在它上面开一轮, Then 开不起来 —— 进程的寿命归
// 会话,关掉就是真的关掉了。
func TestSession_GivenAClosedSession_WhenStartingATurn_ThenItFails(t *testing.T) {
	client, _, _ := newSingleProcessCaptureClient(strings.Join(append(scriptedTurn(), ""), "\n"))

	session, err := client.OpenSession(context.Background())
	require.NoError(t, err)
	require.NoError(t, session.Close(context.Background()))

	_, err = session.Stream(context.Background(), "after close")

	require.Error(t, err)
}
