package chat_entity

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
)

func TestMessage_Check(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		m    *Message
		ok   bool
	}{
		{"valid user", &Message{SessionID: 1, Role: "user", BlocksJSON: "[]", Seq: 1}, true},
		{"valid assistant", &Message{SessionID: 1, Role: "assistant", BlocksJSON: "[]", Seq: 2}, true},
		{"missing session", &Message{Role: "user", BlocksJSON: "[]"}, false},
		{"bad role", &Message{SessionID: 1, Role: "tool", BlocksJSON: "[]"}, false},
		{"bad blocks json", &Message{SessionID: 1, Role: "user", BlocksJSON: "{"}, false},
		{"text length not gated by entity", &Message{SessionID: 1, Role: "user", BlocksJSON: "[]"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.m.Check(ctx)
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestMessage_BlocksRoundTrip(t *testing.T) {
	m := &Message{SessionID: 1, Role: "assistant"}
	err := m.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: "hello"}, &blocks.TextBlock{Text: "world"}})
	assert.NoError(t, err)
	assert.True(t, strings.Contains(m.BlocksJSON, "hello"))
	assert.True(t, strings.Contains(m.BlocksJSON, "world"))

	got, err := m.GetBlocks()
	assert.NoError(t, err)
	assert.Len(t, got, 2)
	tb, ok := got[0].(blocks.TextBlock)
	assert.True(t, ok)
	assert.Equal(t, "hello", tb.Text)
}

func TestMessage_BlocksDecodeMalformed(t *testing.T) {
	m := &Message{BlocksJSON: "not-json"}
	_, err := m.GetBlocks()
	assert.Error(t, err)
}

// TestMessage_DeviceFingerprintFieldTag 验证 DeviceFingerprint 字段在实体上可读写，
// 且 gorm tag 配置了正确的列名 device_fingerprint（决策 16：这一格存的是目标机器的
// 规范设备指纹，不是任何数字主键）。空串 = 本地；非空 = 远端设备。
func TestMessage_DeviceFingerprintFieldTag(t *testing.T) {
	// 空串（本地 backend）
	local := &Message{ID: 1, SessionID: 1, Role: "user", BlocksJSON: "[]", DeviceFingerprint: ""}
	assert.Equal(t, "", local.DeviceFingerprint)

	// 非空（远端 backend，值是那台机器的设备指纹）
	remote := &Message{ID: 2, SessionID: 1, Role: "assistant", BlocksJSON: "[]", DeviceFingerprint: "sha256:remote"}
	assert.Equal(t, "sha256:remote", remote.DeviceFingerprint)

	// gorm tag 携带正确列名
	const wantColumn = "device_fingerprint"
	var m Message
	tp := reflect.TypeOf(m)
	f, ok := tp.FieldByName("DeviceFingerprint")
	assert.True(t, ok, "DeviceFingerprint field must exist on Message struct")
	assert.Contains(t, f.Tag.Get("gorm"), wantColumn, "gorm tag must reference column device_fingerprint")
}
