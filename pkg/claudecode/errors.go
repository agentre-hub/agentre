package claudecode

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// Sentinel errors —— 上层（agentruntime / chat_svc）用 errors.Is 判断。
var (
	ErrBinaryNotFound  = errors.New("claudecode: claude binary not found in PATH or configured CLIPath")
	ErrSessionNotFound = errors.New("claudecode: provider session no longer exists")
	// ErrAPIError 标记 CLI 合成的 API 错误消息帧（isApiErrorMessage:true）。上层用
	// errors.Is 把「turn 被 API 错误终止」这一类与普通子进程退出错误区分开。
	ErrAPIError = errors.New("claudecode: api error")
	// ErrInterruptPending 标记 control_request（interrupt / stop_task）帧已写入
	// CLI stdin、但 ack 在 interruptAckBound 内未到达。帧已写、请求在途,CLI 处理到
	// 后会自然收尾 —— 上层（claudecode Runtime.Abort）把它视为「中断已下发」而不是
	// 「失败」,不得因此杀子进程 / 逐出缓存。
	ErrInterruptPending = errors.New("claudecode: interrupt pending, ack not received within bound")
)

// APIError 承载 CLI isApiErrorMessage 帧的错误文本与顶层 error 分类码。
//
// CLI 在一次 API 调用不可恢复地中断（连接中途断开 / 服务端 5xx 等）时，会把提示文案
// 塞进一个 model:"<synthetic>" 的合成 assistant 帧，并在帧顶层打 isApiErrorMessage:true
// + error:"<code>"。这**不是**模型正文——agentre 把它翻成 EventError，让上层落
// error_text / 渲染独立 ErrorCard，而不是当 EventTextDelta 拼进正文 block（见 sess-2153：
// "API Error: Connection closed mid-response. The response above may be incomplete."
// 曾被直接续在上一段真实输出后面）。
type APIError struct {
	Text string // 面向用户的提示，如 "API Error: Connection closed mid-response. ..."
	Code string // CLI 顶层 error 分类码，如 "server_error"
}

func (e *APIError) Error() string { return e.Text }

// Is 让 errors.Is(err, ErrAPIError) 命中，供上层分类而不依赖文案匹配。
func (e *APIError) Is(target error) bool { return target == ErrAPIError }

// ProcessExitError 子进程非 0 退出时的结构化错误，便于上层（agent_backend_svc/prober）
// 用 errors.As 精确拿到 exit code 和 stderr 文本。
//
// 注意：当 stderr 命中 "Conversation not found" 这类 sentinel 串时仍优先返回
// fmt.Errorf("%w: ...", ErrSessionNotFound, ...) —— 上层先 errors.Is，再 errors.As。
type ProcessExitError struct {
	Code   int
	Stderr string
}

func (e *ProcessExitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("claudecode: exit %d", e.Code)
	}
	return fmt.Sprintf("claudecode: exit %d: %s", e.Code, e.Stderr)
}

// classifyStderr 把 stderr 文本 + exit code 映射到 sentinel error。未识别且 exit
// 非 0 时返回 *ProcessExitError 让上层结构化解析。
func classifyStderr(stderr string, exitCode int) error {
	low := strings.ToLower(stderr)
	// 真实 Claude Code CLI（resume 失效）输出："No conversation found with session ID: <id>"
	// 历史变体保留兜底：旧版本 / SDK 直连可能写 "Conversation not found ..."。
	if strings.Contains(low, "no conversation found") ||
		strings.Contains(low, "conversation not found") ||
		strings.Contains(low, "no resumable conversation") {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, strings.TrimSpace(stderr))
	}
	if exitCode == 0 {
		return nil
	}
	return &ProcessExitError{Code: exitCode, Stderr: strings.TrimSpace(stderr)}
}

// classifyExecErr 区分启动失败（二进制找不到）与运行时失败。
// PATH 查找失败返回 *exec.Error；绝对路径不存在则返回 *fs.PathError。
func classifyExecErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		if strings.Contains(ee.Err.Error(), "not found") {
			return fmt.Errorf("%w: %s", ErrBinaryNotFound, ee.Name)
		}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrBinaryNotFound, pe.Path)
	}
	return err
}
