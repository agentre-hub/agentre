//go:build windows

package clipboardimg

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                      = windows.NewLazySystemDLL("user32.dll")
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard           = user32.NewProc("OpenClipboard")
	procCloseClipboard          = user32.NewProc("CloseClipboard")
	procEmptyClipboard          = user32.NewProc("EmptyClipboard")
	procSetClipboardData        = user32.NewProc("SetClipboardData")
	procRegisterClipboardFormat = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc             = kernel32.NewProc("GlobalAlloc")
	procGlobalFree              = kernel32.NewProc("GlobalFree")
	procGlobalLock              = kernel32.NewProc("GlobalLock")
	procGlobalUnlock            = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory           = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfDIBV5      = 17
	gmemMoveable = 0x0002
	// 别的进程正拿着剪贴板时 OpenClipboard 会失败，短暂重试几次。
	openAttempts = 10
	openBackoff  = 20 * time.Millisecond
)

// writePNG 同时放注册格式 "PNG"（浏览器、Office、聊天工具读它，保留透明）与
// CF_DIBV5（画图等只认位图的应用读它）。
func writePNG(png []byte) error {
	dib, err := PNGToDIBV5(png)
	if err != nil {
		return err
	}
	pngFormat, err := registerFormat("PNG")
	if err != nil {
		return err
	}

	// 剪贴板的打开 / 关闭绑定在调用线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := openClipboard(); err != nil {
		return err
	}
	defer func() { _, _, _ = procCloseClipboard.Call() }() // 关闭失败无可补救

	if r, _, callErr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("clipboardimg: EmptyClipboard: %w", callErr)
	}
	if err := setData(pngFormat, png); err != nil {
		return err
	}
	return setData(cfDIBV5, dib)
}

func registerFormat(name string) (uintptr, error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("clipboardimg: format name: %w", err)
	}
	format, _, callErr := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(ptr))) //nolint:gosec // G103: Win32 调用要传 UTF-16 指针
	if format == 0 {
		return 0, fmt.Errorf("clipboardimg: RegisterClipboardFormat(%s): %w", name, callErr)
	}
	return format, nil
}

func openClipboard() error {
	var lastErr error
	for range openAttempts {
		r, _, callErr := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = callErr
		time.Sleep(openBackoff)
	}
	return fmt.Errorf("clipboardimg: OpenClipboard: %w", lastErr)
}

// setData 把 data 拷进一块可移动全局内存交给剪贴板。SetClipboardData 成功后这块内存
// 归系统所有，不能再释放；失败时由这里释放。
func setData(format uintptr, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("clipboardimg: empty clipboard payload")
	}
	handle, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if handle == 0 {
		return fmt.Errorf("clipboardimg: GlobalAlloc: %w", callErr)
	}
	locked, _, callErr := procGlobalLock.Call(handle)
	if locked == 0 {
		_, _, _ = procGlobalFree.Call(handle) // 已在失败路径上
		return fmt.Errorf("clipboardimg: GlobalLock: %w", callErr)
	}
	// RtlMoveMemory 而不是把 uintptr 转回 unsafe.Pointer：后者 go vet 会报。
	_, _, _ = procRtlMoveMemory.Call(locked, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data))) //nolint:gosec // G103: 拷进 GlobalLock 给的内存
	// 解锁计数归零时 GlobalUnlock 返回 0，不是错误。
	_, _, _ = procGlobalUnlock.Call(handle)

	if r, _, callErr := procSetClipboardData.Call(format, handle); r == 0 {
		_, _, _ = procGlobalFree.Call(handle) // 已在失败路径上
		return fmt.Errorf("clipboardimg: SetClipboardData(%d): %w", format, callErr)
	}
	return nil
}
