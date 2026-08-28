package chat_import_svc

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// defaultPreviewTurns 预览默认取前几轮。够看出"这条是不是我要的那条",又不至于
// 把整份转录解一遍(spec 决策 19:解全文推迟到预览,预览也只解前几轮)。
const defaultPreviewTurns = 3

// Preview 打开一条候选,给出元信息 + 缺口 + 前几轮的真实转录。
//
// 预览与导入**同一条生成路径**:同一个 dispatcher、同一个 accumulator、同一份
// 缺口说明块 —— 预览是回放的前 N 轮,不是另一条解析路径。区别只有一个:不落库。
func (s *chatImportSvc) Preview(ctx context.Context, req *PreviewRequest) (*PreviewResponse, error) {
	if req == nil || strings.TrimSpace(req.Locator) == "" || strings.TrimSpace(req.Backend) == "" {
		return nil, errInvalid(ctx)
	}
	backend := agent_backend_entity.BackendType(req.Backend)
	tr, err := s.open(ctx, req.DeviceID, backend, transcriptimport.Locator(req.Locator))
	if err != nil {
		return nil, err
	}
	defer func() { _ = tr.Close() }()

	meta := tr.Meta()
	limit := req.Turns
	if limit <= 0 {
		limit = defaultPreviewTurns
	}

	// 会话壳只为回放借位(handler 要往 TurnContext.Session 上写 context_window /
	// permission_mode),不落库。
	shell := &chat_entity.Session{}
	gaps := newGapNotifier(meta.Gaps)
	out := &PreviewResponse{Messages: []PreviewMessage{}}
	seq := 1
	replayErr := tr.Turns(ctx, func(t transcriptimport.Turn) error {
		if out.PreviewedTurns >= limit {
			return errPreviewEnough
		}
		pair, err := s.replayTurn(ctx, shell, req.Backend, t, seq, gaps)
		if err != nil {
			return err
		}
		userMsg, err := previewMessageOf(pair.user)
		if err != nil {
			return err
		}
		assistantMsg, err := previewMessageOf(pair.assistant)
		if err != nil {
			return err
		}
		out.Messages = append(out.Messages, userMsg, assistantMsg)
		seq += 2
		out.PreviewedTurns++
		return nil
	})
	if replayErr != nil && !errors.Is(replayErr, errPreviewEnough) {
		return nil, failed(ctx, code.ChatImportTranscriptReplayFailed, replayErr, zap.String("backend", req.Backend))
	}
	if out.PreviewedTurns == 0 {
		return nil, failed(ctx, code.ChatImportTranscriptEmpty, nil, zap.String("backend", req.Backend))
	}

	imported, err := s.importedSessionIDs(ctx, []string{meta.ProviderSessionID})
	if err != nil {
		return nil, err
	}
	out.Meta = s.metaView(ctx, meta)
	if id, ok := imported[meta.ProviderSessionID]; ok {
		out.Meta.Imported = true
		out.Meta.ImportedSessionID = id
	}
	// 元信息没给轮数时说不出还剩几轮 —— 报 -1 而不是 0,别让界面说"没有更多了"。
	out.RemainingTurns = -1
	if meta.Turns > 0 {
		out.RemainingTurns = max(meta.Turns-out.PreviewedTurns, 0)
	}
	return out, nil
}

func (s *chatImportSvc) metaView(ctx context.Context, meta transcriptimport.Meta) TranscriptMetaView {
	view := TranscriptMetaView{
		Backend:           string(meta.Backend),
		ProviderSessionID: meta.ProviderSessionID,
		Title:             meta.Title,
		Cwd:               meta.Cwd,
		Model:             meta.Model,
		Turns:             meta.Turns,
		ToolCalls:         meta.ToolCalls,
		Compactions:       meta.Compactions,
		StartedAt:         unixMilli(meta.StartedAt),
		EndedAt:           unixMilli(meta.EndedAt),
		Origin:            string(meta.Origin),
		Gaps:              []GapView{},
		CwdExists:         s.dirExists(meta.Cwd),
	}
	for _, g := range meta.Gaps {
		view.Gaps = append(view.Gaps, GapView{
			Kind:   string(g.Kind),
			Count:  g.Count,
			Detail: g.Detail,
			Text:   gapText(ctx, g.Kind),
		})
	}
	return view
}

// previewMessageOf 投影一条回放出来的消息。Blocks 走 chat_svc.ProjectBlocks ——
// 与 toChatMessage(线上读回路径)同一条投影,预览渲染的是 agentre 自己的消息与
// 卡片,而不是 blocks_json 原文。
func previewMessageOf(m *chat_entity.Message) (PreviewMessage, error) {
	bs, err := m.GetBlocks()
	if err != nil {
		return PreviewMessage{}, err
	}
	return PreviewMessage{
		Role:       m.Role,
		Seq:        m.Seq,
		Createtime: m.Createtime,
		Model:      m.Model,
		ErrorText:  m.ErrorText,
		Blocks:     chat_svc.ProjectBlocks(bs),
	}, nil
}
