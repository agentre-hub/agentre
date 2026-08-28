package server_svc_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// TestDeviceSerialization 锁定 Device 经 Wails 到达前端时字段为 camelCase：
// 该类型是 App.ServerListDevices 的返回形状，也是共享契约 AccountDeviceView
// 描述的那一份载荷，与同层 chat_svc / agent_backend_svc 的 json tag 约定一致。
func TestDeviceSerialization(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(server_svc.Device{
		ID:           7,
		Name:         "MacBook",
		Kind:         "desktop",
		Platform:     "darwin",
		Version:      "v0.1.0",
		Fingerprint:  "fp-1",
		LastSeenAt:   1234,
		Status:       1,
		Online:       true,
		IsThisDevice: true,
	})
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `"id":7`)
	assert.Contains(t, s, `"name":"MacBook"`)
	assert.Contains(t, s, `"kind":"desktop"`)
	assert.Contains(t, s, `"platform":"darwin"`)
	assert.Contains(t, s, `"version":"v0.1.0"`)
	assert.Contains(t, s, `"fingerprint":"fp-1"`)
	assert.Contains(t, s, `"lastSeenAt":1234`)
	assert.Contains(t, s, `"status":1`)
	assert.Contains(t, s, `"online":true`)
	assert.Contains(t, s, `"isThisDevice":true`)
}

// TestStartLoginResultSerialization 锁定 device-flow 元数据同样是 camelCase。
func TestStartLoginResultSerialization(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(server_svc.StartLoginResult{
		DeviceCode:              "device-abc",
		UserCode:                "ABCD-1234",
		VerificationURI:         "https://example.com/device",
		VerificationURIComplete: "https://example.com/device?user_code=ABCD-1234",
		Interval:                5,
		ExpiresIn:               600,
	})
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `"deviceCode":"device-abc"`)
	assert.Contains(t, s, `"userCode":"ABCD-1234"`)
	assert.Contains(t, s, `"verificationURI":"https://example.com/device"`)
	assert.Contains(t, s, `"verificationURIComplete":"https://example.com/device?user_code=ABCD-1234"`)
	assert.Contains(t, s, `"interval":5`)
	assert.Contains(t, s, `"expiresIn":600`)
}
