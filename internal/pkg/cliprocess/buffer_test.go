package cliprocess

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given 一个活了很多轮的 CLI 进程一直往 stderr 写, When 诊断缓冲区收下这些字节,
// Then 它只保留最近的一段固定长度尾巴。
//
// 常驻 app-server / RPC 进程可以活几个小时,把它整个生命周期的 stderr 留在内存里
// 就是一个无界增长,还让后来的 ExitError 更可能把很久以前的凭据形状原样带出来。
// pkg/claudecode(64KB)与 pkg/codex(64KB 尾巴)各自都限过,只有这个公共的没限 ——
// 而 pkg/piagent 用的正是它。
func TestLockedBuffer_GivenMoreBytesThanTheCap_WhenReading_ThenOnlyTheRecentTailRemains(t *testing.T) {
	var buf LockedBuffer

	_, err := buf.Write([]byte(strings.Repeat("x", 80*1024) + "diagnostic-tail"))
	require.NoError(t, err)

	assert.LessOrEqual(t, len(buf.String()), MaxDiagnosticBytes)
	assert.True(t, strings.HasSuffix(buf.String(), "diagnostic-tail"), "保留的必须是最近的尾巴")
}

// 边界:分多次写累计超过上限时,同样只留尾巴 —— 真实进程是一行一行写的。
func TestLockedBuffer_GivenManySmallWritesPastTheCap_WhenReading_ThenOnlyTheRecentTailRemains(t *testing.T) {
	var buf LockedBuffer

	for i := 0; i < 100; i++ {
		_, err := buf.Write([]byte(strings.Repeat("y", 1024)))
		require.NoError(t, err)
	}
	_, err := buf.Write([]byte("last-line"))
	require.NoError(t, err)

	assert.LessOrEqual(t, len(buf.String()), MaxDiagnosticBytes)
	assert.True(t, strings.HasSuffix(buf.String(), "last-line"))
}
