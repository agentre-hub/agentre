package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/daemon/state"
)

// 与 login 同一条红线(见 TestLoginRefusesWhileDaemonIsRunningSoItCannotBeSilentlyReverted):
// 运行中的 daemon 会用内存里那份整文件覆写 state.json,所以此刻解除归属只是在磁盘上停留
// 几秒钟的假象。它尤其致命 —— 用户通常是先 unclaim 再 login,第一步就被回滚的话,第二步
// 会以「已归属」为由拒绝,或者登录成功却同样被覆盖。
func TestUnclaimRefusesWhileDaemonIsRunning(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountID = "account-1" })
	require.NoError(t, st.Save())

	cmd := newUnclaimCmdWithDeps(
		func() (string, error) { return dir, nil },
		func() bool { return true },
	)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agentred is running")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.True(t, got.IsClaimed(), "a refused unclaim must not report success it cannot keep")
}

func TestUnclaimRemovesClaimWhenNoDaemonIsRunning(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountID = "account-1" })
	require.NoError(t, st.Save())

	cmd := newUnclaimCmdWithDeps(
		func() (string, error) { return dir, nil },
		func() bool { return false },
	)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)

	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "removed")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.False(t, got.IsClaimed())
}
