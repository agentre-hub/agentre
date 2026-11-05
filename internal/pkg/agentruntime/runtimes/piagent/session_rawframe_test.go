package piagent

import (
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestPiRawFrameEnabledFollowsDebugLevelOfCurrentLogger(t *testing.T) {
	// Given the logger is replaced when the Debug Logging switch flips,
	// When the gate is asked, Then it reflects the logger installed at that moment.
	old := logger.Default()
	t.Cleanup(func() { logger.SetLogger(old) })
	newLogger := func(level zapcore.Level) *zap.Logger {
		enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
		return zap.New(zapcore.NewCore(enc, zapcore.AddSync(discardWriter{}), level))
	}

	logger.SetLogger(newLogger(zapcore.InfoLevel))
	assert.False(t, piRawFrameEnabled())

	logger.SetLogger(newLogger(zapcore.DebugLevel))
	assert.True(t, piRawFrameEnabled())
}

func TestPiProgressToolOnlyWantsSubagentTools(t *testing.T) {
	assert.True(t, piProgressTool("subagent"))
	assert.True(t, piProgressTool("Run_SubAgent"))
	assert.False(t, piProgressTool("bash"))
	assert.False(t, piProgressTool(""))
}
