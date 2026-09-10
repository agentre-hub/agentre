package rpcerror_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// TestDomainCodes_GivenReleasedPeers_WhenTheyBranchOnTheNumber_ThenTheValuesNeverMove
// 钉死每一个领域错误码的数值。
//
// 这些码是**过线的稳定协议值**:对端按数字分支,不按 message 文本。已发布的
// agentred、桌面端与 agentre-server 各自认这些数字,改一个就是协议破坏 ——
// 所以期望值在这里一个一个写死,而不是从常量本身推导。
func TestDomainCodes_GivenReleasedPeers_WhenTheyBranchOnTheNumber_ThenTheValuesNeverMove(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		got  int32
		want int32
	}{
		{"CodeRuntimeNoActiveTurn", rpcerror.CodeRuntimeNoActiveTurn, -32010},
		{"CodeRuntimeSteerNotFound", rpcerror.CodeRuntimeSteerNotFound, -32011},
		{"CodeRuntimeUnsupported", rpcerror.CodeRuntimeUnsupported, -32012},
		{"CodeRuntimeAborted", rpcerror.CodeRuntimeAborted, -32013},
		{"CodeRuntimeSessionNotFound", rpcerror.CodeRuntimeSessionNotFound, -32014},
		{"CodeRuntimePeerExecutionUnavailable", rpcerror.CodeRuntimePeerExecutionUnavailable, -32015},

		{"CodeRemoteFSPathRefused", rpcerror.CodeRemoteFSPathRefused, -32030},
		{"CodeRemoteFSPermDenied", rpcerror.CodeRemoteFSPermDenied, -32031},
		{"CodeRemoteFSNotFound", rpcerror.CodeRemoteFSNotFound, -32032},
		{"CodeRemoteFSNotDir", rpcerror.CodeRemoteFSNotDir, -32033},
		{"CodeRemoteFSMkdirExists", rpcerror.CodeRemoteFSMkdirExists, -32034},
		{"CodeRemoteFSInvalidName", rpcerror.CodeRemoteFSInvalidName, -32035},

		{"CodeWorkspaceFSPathRefused", rpcerror.CodeWorkspaceFSPathRefused, -32040},
		{"CodeWorkspaceFSBaselineRequired", rpcerror.CodeWorkspaceFSBaselineRequired, -32041},
		{"CodeWorkspaceFSNoCwd", rpcerror.CodeWorkspaceFSNoCwd, -32042},
		{"CodeWorkspaceFSNotFound", rpcerror.CodeWorkspaceFSNotFound, -32043},

		{"CodeProjectNotSynced", rpcerror.CodeProjectNotSynced, -32050},
		{"CodeProjectInvalidPath", rpcerror.CodeProjectInvalidPath, -32051},
		{"CodeProjectPathNotFound", rpcerror.CodeProjectPathNotFound, -32052},
	} {
		require.Equalf(t, c.want, c.got, "%s 是过线的稳定值,改它就是协议破坏", c.name)
	}
}
