package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// TestBoardJoinNotice_GivenTheBoardJoinsForTheFirstTime_IsPendingOnce 规格「首次
// 上行的后果要说在前面」：两台机器各自积累的历史任务，首次同步后会合并出现在同一
// 个账号下，而且不可逆——所以桌面端在首次把看板并入同步时给一次说明，不是静默合并。
//
// 说完之后它永远不再出现：第二轮同步、第三轮同步都不再有待说明。
func TestBoardJoinNotice_GivenTheBoardJoinsForTheFirstTime_IsPendingOnce(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.state.unowned[syncwire.KindLabel] = []syncstate_repo.ClaimedRow{{SyncID: "label-1"}}

	pending, err := h.svc.BoardJoinNoticePending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "还没并入同步组之前没有什么要说的")

	require.NoError(t, h.svc.claimUnowned(ctx, 7))

	pending, err = h.svc.BoardJoinNoticePending(ctx)
	require.NoError(t, err)
	assert.True(t, pending, "看板首次并入同步组，这一次要把合并的后果说在前面")

	st, err := h.svc.Status(ctx)
	require.NoError(t, err)
	assert.True(t, st.BoardJoinNoticePending, "状态里带着它，界面据此弹一次说明")

	require.NoError(t, h.svc.AcknowledgeBoardJoinNotice(ctx))

	pending, err = h.svc.BoardJoinNoticePending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "说过一次就不再说")

	// 之后每一轮同步都照常跑认领（第一轮之后是空集），但说明不会再出现。
	h.state.unowned[syncwire.KindIssue] = []syncstate_repo.ClaimedRow{{SyncID: "issue-1"}}
	require.NoError(t, h.svc.claimUnowned(ctx, 7))
	pending, err = h.svc.BoardJoinNoticePending(ctx)
	require.NoError(t, err)
	assert.False(t, pending, "一次性说明不因后续认领复活")
}

// TestBoardJoinNotice_GivenOnlyNonBoardKindsAreClaimed_StaysSilent 认领项目 / Agent
// 与看板无关：那条说明说的是「任务会合并」，没有任务被并进来时它不该冒出来。
func TestBoardJoinNotice_GivenOnlyNonBoardKindsAreClaimed_StaysSilent(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.state.unowned[syncwire.KindProject] = []syncstate_repo.ClaimedRow{{SyncID: "proj-1"}}

	require.NoError(t, h.svc.claimUnowned(ctx, 7))

	pending, err := h.svc.BoardJoinNoticePending(ctx)
	require.NoError(t, err)
	assert.False(t, pending)
}
