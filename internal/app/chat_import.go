package app

import (
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/agentre-hub/agentre/internal/service/chat_import_svc"
)

/*
导入本地会话的三个绑定。错误**带着业务码过桥**（见 coded_error.go）：对话框要把
「这台机器没有这个后端的档案 / 转录打不开 / 选中的 Agent 后端不对 / 远端还没支持」
分开说，它们的出路各不相同，折成同一句「导入失败」等于什么都没说。
*/

// ChatImportProgressEvent 是按轮计的导入进度事件名。前端订阅它渲染进度条；
// 导入本身是一次同步调用，进度经事件推送而不是返回值（同 update:progress）。
const ChatImportProgressEvent = "chat-import:progress"

// ListImportCandidates 扫描某台设备上可导入的本地会话。单个后端失败只在
// Issues 里报出它自己那一档，其余照常返回。
func (a *App) ListImportCandidates(req *chat_import_svc.ListCandidatesRequest) (*chat_import_svc.ListCandidatesResponse, error) {
	resp, err := chat_import_svc.Default().ListCandidates(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}

// PreviewImportTranscript 打开一条候选，返回元信息、缺口声明与前几轮真实转录。
func (a *App) PreviewImportTranscript(req *chat_import_svc.PreviewRequest) (*chat_import_svc.PreviewResponse, error) {
	resp, err := chat_import_svc.Default().Preview(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}

// CancelLocalSessionImport 停掉一笔还在写库的导入。RequestID 与 ImportLocalSession
// 那一笔一致；未知 id 只答 Canceled=false（导入刚返回、取消慢半拍是常态）。
func (a *App) CancelLocalSessionImport(req *chat_import_svc.CancelImportRequest) (*chat_import_svc.CancelImportResponse, error) {
	resp, err := chat_import_svc.Default().Cancel(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}

// ImportLocalSession 把一条候选落成一条 agentre 会话；进度经
// ChatImportProgressEvent 按轮推送。取消走 CancelLocalSessionImport —— 这一笔是
// 同步调用，中断只能从外面另敲一下。
func (a *App) ImportLocalSession(req *chat_import_svc.ImportRequest) (*chat_import_svc.ImportResponse, error) {
	ctx := a.ctx
	onProgress := func(done, total int) {
		wailsruntime.EventsEmit(ctx, ChatImportProgressEvent, map[string]int{
			"done":  done,
			"total": total,
		})
	}
	resp, err := chat_import_svc.Default().Import(ctx, req, onProgress)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}
