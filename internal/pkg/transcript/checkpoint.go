package transcript

import "github.com/agentre-hub/agentre/internal/pkg/agentruntime"

// ShouldCheckpointAfter 回答「这一帧之后要不要把在途那一轮的正文 checkpoint 一次」。
//
// 在途那一轮抗崩溃靠的就是它,不另立 WAL(决策 5):宿主在轮中消失时,checkpoint 过的
// 块留在库里,没 checkpoint 的尾巴丢失。选中的这几种事件是「一段内容已经定稿」的那些
// 时刻 —— 工具往返收口、待决策的提出与作答、计划更新;逐 token 的增量不在其中,按它们
// checkpoint 等于把 checkpoint 变成第二条流式写路径。
//
// 两个宿主共用这一份:桌面端(chat_svc)与 agentred 在同一帧上落同一次 checkpoint,
// 否则「换台机器跑,崩溃后看得见的东西不一样」就是这一处漏同步的直接后果。
func ShouldCheckpointAfter(ev agentruntime.Event) bool {
	switch ev.(type) {
	case agentruntime.ToolResult,
		agentruntime.UserAskRequest,
		agentruntime.UserAskResolved,
		agentruntime.ToolPermissionRequest,
		agentruntime.ToolPermissionResolved,
		agentruntime.PlanUpdated:
		return true
	default:
		return false
	}
}
