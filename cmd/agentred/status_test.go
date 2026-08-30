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

// TestPrintStatus_OmitsDatabaseLineWhenDaemonDoesNotReportIt 老 daemon 的状态应答里没有
// dbPath,此时不能印一行空路径的 "Database:" —— 那会让人以为库文件丢了。
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
