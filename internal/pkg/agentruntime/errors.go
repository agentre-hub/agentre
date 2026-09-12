package agentruntime

import "errors"

// ErrNoActiveTurn 来自 Steer / Abort / SetPermissionMode 等控制接口:对应
// sessionID 没有 in-flight turn 时返回。chat_svc 据此翻译为 ChatSteerNoActive /
// ChatStopNoActive 给前端。
var ErrNoActiveTurn = errors.New("agentruntime: no active turn for session")

// ErrSteerNotFound CancelSteer 收到非空 queuedID 但 pending 队列里找不到
// (AI 已消费或 ID 从未入队)。chat_svc 翻译为 ChatCancelNotFound。
var ErrSteerNotFound = errors.New("agentruntime: queued steer entry not found")

// ErrAborted RunResult.StopErr 在用户主动 Abort 时填这个,让 chat_svc 区分
// "正常 Done" / "用户中止" / "真错误"三态。当前由 remote runner 使用。
var ErrAborted = errors.New("agentruntime: chat aborted by user")

// ErrUnsupported runtime 不支持的能力(对应 capability bool=false)。
var ErrUnsupported = errors.New("agentruntime: capability unsupported by this runtime")

// ErrSessionNotFound 表示 runtime 请求恢复的 provider 原生 Session 已不存在。
// chat_svc 据此清空 provider_session_id，且当前轮失败而不是静默创建替代 Session。
var ErrSessionNotFound = errors.New("agentruntime: provider session no longer exists")

// ErrWaiterNotFound 表示 SubmitToolPermission / SubmitAnswer 按 requestID
// 找不到阻塞中的 waiter —— 已被回答过一次(take-and-delete 已命中)、或从未
// 在这个 runner 上登记过。daemon 侧的 handler 把它当幂等成功处理而不是报错
// 给客户端(R8):断连重连后客户端无法判断上一次提交是否已经送达,报错只会
// 让它误报给用户。
var ErrWaiterNotFound = errors.New("agentruntime: no waiting request for requestID")

// ErrBackgroundTaskUnknown 来自 BackgroundTaskResolver.ResolveBackgroundTask:这个
// runner 认不出该 tool_use id 对应的后台任务 —— 子进程从没报过它(不是后台任务)、
// 或那个子进程已经不在(evict / 重开)。与「反查到了但值为空」区分开:调用方据此
// 退回持久化的 subagent_state overlay,而不是拿一个空标识去下发 stop。
var ErrBackgroundTaskUnknown = errors.New("agentruntime: unknown background task for tool_use id")
