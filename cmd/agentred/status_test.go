package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrintStatus_ShowsDatabasePathAndSize 验证 CLI 的可观察契约：远端盒子上的
// transcript 是永久落盘的档案，库文件的**位置与体量**必须在 daemon 状态
// 查询里看得见,用户才能自行判断何时该清理。/local/status 早就交出了 dbPath / dbSizeBytes,
// 但 `agentred status` 一个字都不印 —— 对用户而言那等于不可见。
func TestPrintStatus_ShowsDatabasePathAndSize(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":              float64(4321),
		"listenURLs":       []any{"wss://192.168.1.9:7456"},
		"pairedPeers":      []any{map[string]any{}},
		"activeSessions":   float64(0),
		"llmProviderCount": float64(2),
		"dbPath":           "/var/lib/agentred/agentred.db",
		"dbSizeBytes":      float64(3_221_225_472),
	})

	out := buf.String()
	assert.Contains(t, out, "/var/lib/agentred/agentred.db", "库文件路径必须印出来")
	assert.Contains(t, out, "3.0 GB", "体量要印成人读得懂的量级,3221225472 这串数字判断不了该不该清理")
}

func TestGivenStatusWithVersionWhenPrintingThenShowsDaemonBuildIdentity(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":              float64(1),
		"version":          "v1.2.3 (abcdef1)",
		"listenURLs":       []any{},
		"pairedPeers":      []any{},
		"activeSessions":   float64(0),
		"llmProviderCount": float64(0),
	})

	assert.Contains(t, buf.String(), "Version: v1.2.3 (abcdef1)\n")
}

func TestGivenOlderStatusWithoutVersionWhenPrintingThenStillRenders(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":              float64(1),
		"listenURLs":       []any{},
		"pairedPeers":      []any{},
		"activeSessions":   float64(0),
		"llmProviderCount": float64(0),
	})

	assert.Contains(t, buf.String(), "Daemon running, pid 1\n")
	assert.NotContains(t, buf.String(), "Version:")
}

func TestGivenDaemonConnectionStateWhenPrintingThenShowsRelayAndClientConnections(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":                   float64(1),
		"listenURLs":            []any{},
		"pairedPeers":           []any{},
		"activeSessions":        float64(0),
		"llmProviderCount":      float64(0),
		"relayConnected":        true,
		"clientConnectionCount": float64(2),
	})

	assert.Contains(t, buf.String(), "Relay: connected\n")
	assert.Contains(t, buf.String(), "Client connections: 2\n")
}

func TestGivenZeroConnectionStateWhenPrintingThenNeverShowsLegacyUnknownCopy(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":              float64(1),
		"listenURLs":       []any{},
		"pairedPeers":      []any{},
		"activeSessions":   float64(0),
		"llmProviderCount": float64(0),
	})

	assert.Contains(t, buf.String(), "Relay: disconnected\n")
	assert.Contains(t, buf.String(), "Client connections: 0\n")
	assert.NotContains(t, buf.String(), "unknown")
}

// TestPrintStatus_OmitsDatabaseLineWhenDaemonDoesNotReportIt 应答里取不出 dbPath 时
// 不能印一行空路径的 "Database:" —— 那会让人以为库文件丢了。
func TestPrintStatus_OmitsDatabaseLineWhenDaemonDoesNotReportIt(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":              float64(1),
		"listenURLs":       []any{},
		"pairedPeers":      []any{},
		"activeSessions":   float64(0),
		"llmProviderCount": float64(0),
	})

	assert.NotContains(t, buf.String(), "Database")
}

// TestHumanBytes_RendersEachMagnitude 体量渲染的边界:不足 1 KiB 按字节原样印,进位后保留
// 一位小数,最大量级封顶在 TB 而不是继续乘下去。
func TestHumanBytes_RendersEachMagnitude(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{2048 * 1024 * 1024 * 1024 * 1024, "2048.0 TB"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanBytes(c.in), "humanBytes(%d)", c.in)
	}
}

// TestPrintStatus_PairedPeersAreNotAccountDevices 钉住这一行数的到底是什么。
//
// 它数的是 state.json 里的 pairedPeers —— 走配对码那条流、同网段直连过来的桌面端。
// 而控制台「设备」页数的是账号授权设备（走设备码那条流的 devices 行），两者是不同的
// 集合：用户先配对再登录，CLI 印 1、控制台可能印 3，共用「devices」这一个词就只能
// 让人以为其中一边错了。
func TestPrintStatus_PairedPeersAreNotAccountDevices(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, map[string]any{
		"pid":         float64(4321),
		"listenURLs":  []any{"wss://192.168.1.9:7456"},
		"pairedPeers": []any{map[string]any{}, map[string]any{}},
	})

	out := buf.String()
	assert.Contains(t, out, "Paired peers: 2", "数的是配对到本机的对端，不是账号设备")
	assert.NotContains(t, out, "Paired devices",
		"「devices」在控制台已经指账号授权设备，这里不能再用它指另一个集合")
}
