package remotepool

import (
	"errors"
	"fmt"
)

// ErrInvalidDevice 表示 backend 记的 DeviceID 解析不出本机配对行 ID:空 DeviceID
// (本机档)或指纹在本机配对表里查不到。宿主把它翻成 AgentBackendInvalidDevice。
var ErrInvalidDevice = errors.New("remotepool: backend device is not paired locally")

// DialError 包住池借用失败的原始 err,宿主据此记日志并翻成 RemoteRunnerDialFailed。
type DialError struct {
	DeviceID int64
	Err      error
}

func (e *DialError) Error() string {
	return fmt.Sprintf("remotepool: borrow device %d: %v", e.DeviceID, e.Err)
}

func (e *DialError) Unwrap() error { return e.Err }
