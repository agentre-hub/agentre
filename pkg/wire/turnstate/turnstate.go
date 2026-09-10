// Package turnstate 回答关于「一轮执行怎么收的场」的问题。
//
// 它住在 pkg/wire 这个共享 module 里,而不是桌面仓的 internal/ 下,因为**两个仓库都要
// 用同一句话**:agentred 据它决定把会话行落成 failed 还是 idle,agentre-server 的账号
// 镜像据它决定那一行过线之后是什么状态,浏览器据它决定要不要在转录里画一张错误卡。
// 三处分头写的话,同一轮在列表里和在转录里会给出两种说法。
//
// 它不在 agentrewire/ 里:那个目录由 buf 生成,`clean: true` 会在每次生成前把它清空,
// 手写文件放进去会在下一次 `pnpm run proto:generate` 时消失(同 guard/ 分家的理由)。
//
// 本包刻意只收**标量**而不是某个帧类型:线上那一帧在三处各有各的形状(桌面端 internal
// 的 JSON 结构体、生成的 protobuf 消息、TS 的 DTO),收标量才能让三者共用同一个判据。
package turnstate

// AbortedCode 是「用户自己按了停止」这个 sentinel 的 RPC 错误码。
//
// 它与桌面端 internal wire 的 ErrCodeAborted 是同一个值 —— 那一侧现在从这里取,
// 不再各写一份字面量。
const AbortedCode = -32013

// IsFailure 判定这一轮是不是**故障**收场。
//
// 用户自己按的停止不算:中断在线上同样带停止文案(agentruntime.ErrAborted 的 Error()),
// 只有错误码分得开两者 —— 不认这一格的话,每点一次「停止」都会在列表里留下一条失败的
// 会话、在转录里留下一张红卡。
//
// 判据取**文案非空**而不是错误码非零:错误码 0 的含义是「没有 sentinel」,不是「没出
// 错」—— 真正的启动失败(CLI 直接 exit 1)正落在这一档,它有文案、没有 sentinel。
func IsFailure(stopErrMsg string, stopErrCode int) bool {
	if stopErrMsg == "" {
		return false
	}
	return stopErrCode != AbortedCode
}
