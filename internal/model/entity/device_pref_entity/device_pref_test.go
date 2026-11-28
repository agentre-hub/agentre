package device_pref_entity

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

// Given 带空白、空串与重复的标签 When 写入 Then 存下去重、去空白、保持首次出现顺序的列表。
func TestDevicePref_SetLabels_NormalizesOnce(t *testing.T) {
	p := &DevicePref{}
	p.SetLabels([]string{" gpu ", "", "gpu", "build", "  "})
	assert.Equal(t, []string{"gpu", "build"}, p.Labels())
	assert.JSONEq(t, `["gpu","build"]`, p.LabelsJSON)
}

// Given 从未设过标签的行 When 读标签 Then 是空列表而不是 nil 或报错。
func TestDevicePref_Labels_EmptyAndCorrupt(t *testing.T) {
	assert.Empty(t, (&DevicePref{}).Labels())
	assert.Empty(t, (&DevicePref{LabelsJSON: "not json"}).Labels())
}

// Given 缺指纹的偏好行 When 校验 Then 拒绝；有指纹则通过。
func TestDevicePref_Check(t *testing.T) {
	ctx := context.Background()
	requireCode(t, (&DevicePref{}).Check(ctx), code.DevicePrefInvalidDevice)
	assert.NoError(t, (&DevicePref{DeviceFingerprint: "sha256:aa"}).Check(ctx))
}

// Given 运行时类型与设备 When 校验 CLI 路径覆盖 Then 只有带 CLI 可执行文件的运行时才允许，设备必填。
func TestCLIPathOverride_Check(t *testing.T) {
	ctx := context.Background()
	ok := &CLIPathOverride{RuntimeType: "claudecode", DeviceFingerprint: "sha256:aa", CLIPath: "/usr/bin/claude"}
	assert.NoError(t, ok.Check(ctx))
	requireCode(t, (&CLIPathOverride{RuntimeType: "hermes", DeviceFingerprint: "sha256:aa", CLIPath: "/x"}).Check(ctx), code.DeviceCLIRuntimeInvalid)
	requireCode(t, (&CLIPathOverride{RuntimeType: "", DeviceFingerprint: "sha256:aa"}).Check(ctx), code.DeviceCLIRuntimeInvalid)
	requireCode(t, (&CLIPathOverride{RuntimeType: "codex"}).Check(ctx), code.DevicePrefInvalidDevice)
}
