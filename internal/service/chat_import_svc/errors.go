package chat_import_svc

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// errInvalidDevice 是设备 id 本身不合法(负数)。
var errInvalidDevice = errors.New("chat_import_svc: invalid device id")

// failed 把一个内部错误翻成带原因的用户可见错误,并记一条日志。
//
// 隐私红线(spec「隐私」):日志只记数量、耗时、失败原因与不透明标识 —— 后端类型、
// 定位符、会话 id 可以记,消息正文 / 工具入参 / 文件内容一概不记。
func failed(ctx context.Context, c int, cause error, fields ...zap.Field) error {
	if cause != nil {
		fields = append(fields, zap.Error(cause))
	}
	logger.Ctx(ctx).Error("chat_import_svc: operation failed", append([]zap.Field{zap.Int("code", c)}, fields...)...)
	return i18n.NewError(ctx, c)
}

// errInvalid 是几个入口共用的参数校验(定位符 / 后端 / agent 缺一不可)。
func errInvalid(ctx context.Context) error { return i18n.NewError(ctx, code.InvalidParameter) }

// unixMilli 把磁盘时间戳转成毫秒;零值仍是 0(调用方据此回退,不用导入时刻冒充)。
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// codeOf 把远端三态如实翻成各自的话,而不是笼统的"操作失败":
// 「升级 agentred」「这台机器此刻连不上」「这台机器上没有这个后端的档案」是三条
// 不同的出路,混成一句用户就无从下手(spec「远端」)。
func codeOf(err error) int {
	if c, ok := remoteCodeOf(err); ok {
		return c
	}
	if errors.Is(err, errInvalidDevice) {
		return code.InvalidParameter
	}
	return code.ChatImportBackendUnavailable
}

// remoteCodeOf 认出远端三态的哨兵;不是远端错误时返回 false,由调用方按自己那条
// 路的默认码兜底。
func remoteCodeOf(err error) (int, bool) {
	switch {
	case errors.Is(err, errDeviceOffline):
		return code.ChatImportDeviceOffline, true
	case errors.Is(err, errBackendUnavailable):
		return code.ChatImportBackendUnavailable, true
	}
	return 0, false
}

// deviceScanStatus 回答「这个扫描错误是整台设备的答案吗」。是的话给出三态里的
// 那一档,发现聚合据此只记一条设备级 issue。
func deviceScanStatus(err error) (string, bool) {
	if errors.Is(err, errDeviceOffline) {
		return ScanStatusUnavailable, true
	}
	return "", false
}

// errEmptyTranscript 是「一轮都解不出来」的内部哨兵:它必须让事务回滚(否则会留下
// 一条空会话),所以走 error 而不是提前 return。
var errEmptyTranscript = errors.New("chat_import_svc: transcript yielded no turn")

// errPreviewEnough 是预览取够前 N 轮之后主动收工的哨兵 —— 契约里 yield 返回非 nil
// 就立刻停止回放,不必把整份转录解完。
var errPreviewEnough = errors.New("chat_import_svc: preview turn budget reached")
