package wire

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 反向隧道也载 agrctl 的 /ctl/*（桌面端拥有的 agentred 会话）。agrctl 读的是 ctl 契约：
// 失败是非 2xx + {"error": …}；给它一个 200 的 JSON-RPC 信封，它会解成一个空的成功响应
// ——list 打出零条、写入「成功」而什么都没变。
func TestMCPTunnelUnavailableResponse_GivenCtlPathThenCtlShapedFailure(t *testing.T) {
	resp := MCPTunnelUnavailableResponse("/ctl/v1/resources", []byte(`{"list":{"kind":"CTL_KIND_AGENT"}}`))
	assert.Equal(t, http.StatusBadGateway, resp.Status)
	var body map[string]any
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	assert.NotContains(t, body, "jsonrpc")
	assert.NotEmpty(t, body["error"])
}

func TestMCPTunnelUnavailableResponse_GivenMCPPathThen200JSONRPCError(t *testing.T) {
	resp := MCPTunnelUnavailableResponse("/mcp/org/", []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call"}`))
	assert.Equal(t, http.StatusOK, resp.Status)
	var body struct {
		ID    int `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	assert.Equal(t, 7, body.ID)
	assert.Equal(t, mcpTunnelErrorCode, body.Error.Code)
}
