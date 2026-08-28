package protowire

import (
	"time"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// transcriptimport.go 是 transcriptimport.* 方法族的唯一编解码边界:daemon 侧
// handler 用它编,host 侧远端读取器用它解,两端因此不会各解各的。
//
// 时间一律 unix 毫秒,**零值仍是零值**:磁盘上没有时间就是没有,拿 1970 冒充会让
// 「最后活动时间」排序与「取磁盘时间而不是导入时间」两条口径同时失守。

func transcriptMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func transcriptTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// ── Scan ────────────────────────────────────────────────────────────────────

func TranscriptScanParamsToProto(params wire.ScanParams) *agentrewire.TranscriptImportScanRequest {
	return &agentrewire.TranscriptImportScanRequest{
		Backends: params.Backends,
		Filter: &agentrewire.TranscriptImportFilter{
			CwdPrefix:  params.Filter.CwdPrefix,
			Since:      transcriptMillis(params.Filter.Since),
			TitleQuery: params.Filter.TitleQuery,
			Limit:      int32(params.Filter.Limit),
		},
	}
}

func TranscriptScanParamsFromProto(request *agentrewire.TranscriptImportScanRequest) wire.ScanParams {
	filter := request.GetFilter()
	return wire.ScanParams{
		Backends: request.GetBackends(),
		Filter: pkgimport.Filter{
			CwdPrefix:  filter.GetCwdPrefix(),
			Since:      transcriptTime(filter.GetSince()),
			TitleQuery: filter.GetTitleQuery(),
			Limit:      int(filter.GetLimit()),
		},
	}
}

func TranscriptScanResultToProto(result wire.ScanResult) *agentrewire.TranscriptImportScanResponse {
	out := &agentrewire.TranscriptImportScanResponse{}
	for _, backend := range result.Backends {
		entry := &agentrewire.TranscriptImportBackendResult{
			Backend: backend.Backend, Status: backend.Status, Reason: backend.Reason,
		}
		for _, c := range backend.Candidates {
			entry.Candidates = append(entry.Candidates, &agentrewire.TranscriptImportCandidate{
				Backend: string(c.Backend), ProviderSessionId: c.ProviderSessionID, Title: c.Title,
				Cwd: c.Cwd, StartedAt: transcriptMillis(c.StartedAt), EndedAt: transcriptMillis(c.EndedAt),
				Turns: int32(c.Turns), Origin: string(c.Origin), Locator: string(c.Locator),
			})
		}
		out.Backends = append(out.Backends, entry)
	}
	return out
}

func TranscriptScanResultFromProto(response *agentrewire.TranscriptImportScanResponse) wire.ScanResult {
	out := wire.ScanResult{}
	for _, entry := range response.GetBackends() {
		backend := wire.BackendScan{
			Backend: entry.GetBackend(), Status: entry.GetStatus(), Reason: entry.GetReason(),
		}
		for _, c := range entry.GetCandidates() {
			backend.Candidates = append(backend.Candidates, pkgimport.Candidate{
				Backend:           agent_backend_entity.BackendType(c.GetBackend()),
				ProviderSessionID: c.GetProviderSessionId(),
				Title:             c.GetTitle(),
				Cwd:               c.GetCwd(),
				StartedAt:         transcriptTime(c.GetStartedAt()),
				EndedAt:           transcriptTime(c.GetEndedAt()),
				Turns:             int(c.GetTurns()),
				Origin:            pkgimport.Origin(c.GetOrigin()),
				Locator:           pkgimport.Locator(c.GetLocator()),
			})
		}
		out.Backends = append(out.Backends, backend)
	}
	return out
}

// ── Open ────────────────────────────────────────────────────────────────────

func TranscriptOpenParamsToProto(params wire.OpenParams) *agentrewire.TranscriptImportOpenRequest {
	return &agentrewire.TranscriptImportOpenRequest{Backend: params.Backend, Locator: params.Locator}
}

func TranscriptOpenParamsFromProto(request *agentrewire.TranscriptImportOpenRequest) wire.OpenParams {
	return wire.OpenParams{Backend: request.GetBackend(), Locator: request.GetLocator()}
}

func TranscriptOpenResultToProto(result wire.OpenResult) *agentrewire.TranscriptImportOpenResponse {
	meta := result.Meta
	out := &agentrewire.TranscriptImportMeta{
		Backend: string(meta.Backend), ProviderSessionId: meta.ProviderSessionID, Title: meta.Title,
		Cwd: meta.Cwd, Model: meta.Model, Turns: int32(meta.Turns), ToolCalls: int32(meta.ToolCalls),
		Compactions: int32(meta.Compactions), StartedAt: transcriptMillis(meta.StartedAt),
		EndedAt: transcriptMillis(meta.EndedAt), Origin: string(meta.Origin),
	}
	for _, gap := range meta.Gaps {
		out.Gaps = append(out.Gaps, &agentrewire.TranscriptImportGap{
			Kind: string(gap.Kind), Count: int32(gap.Count), Detail: gap.Detail,
		})
	}
	return &agentrewire.TranscriptImportOpenResponse{Meta: out}
}

func TranscriptOpenResultFromProto(response *agentrewire.TranscriptImportOpenResponse) wire.OpenResult {
	meta := response.GetMeta()
	out := pkgimport.Meta{
		Backend:           agent_backend_entity.BackendType(meta.GetBackend()),
		ProviderSessionID: meta.GetProviderSessionId(),
		Title:             meta.GetTitle(),
		Cwd:               meta.GetCwd(),
		Model:             meta.GetModel(),
		Turns:             int(meta.GetTurns()),
		ToolCalls:         int(meta.GetToolCalls()),
		Compactions:       int(meta.GetCompactions()),
		StartedAt:         transcriptTime(meta.GetStartedAt()),
		EndedAt:           transcriptTime(meta.GetEndedAt()),
		Origin:            pkgimport.Origin(meta.GetOrigin()),
	}
	for _, gap := range meta.GetGaps() {
		out.Gaps = append(out.Gaps, pkgimport.Gap{
			Kind: pkgimport.GapKind(gap.GetKind()), Count: int(gap.GetCount()), Detail: gap.GetDetail(),
		})
	}
	return wire.OpenResult{Meta: out}
}

// ── Turns ───────────────────────────────────────────────────────────────────

func TranscriptTurnsParamsToProto(params wire.TurnsParams) *agentrewire.TranscriptImportTurnsRequest {
	return &agentrewire.TranscriptImportTurnsRequest{
		Backend: params.Backend, Locator: params.Locator,
		StartIndex: int32(params.StartIndex), MaxTurns: int32(params.MaxTurns),
	}
}

func TranscriptTurnsParamsFromProto(request *agentrewire.TranscriptImportTurnsRequest) wire.TurnsParams {
	return wire.TurnsParams{
		Backend: request.GetBackend(), Locator: request.GetLocator(),
		StartIndex: int(request.GetStartIndex()), MaxTurns: int(request.GetMaxTurns()),
	}
}

// TranscriptTurnsResultToProto 编一页轮次。事件走 marshalEvent 那份**逐字段**的
// sealed event 编解码(见 event.go 顶部那段:JSON 中转会静默丢字段),
// RuntimeEventNotification 在这里纯粹当信封用,session_id / seq 不参与。
func TranscriptTurnsResultToProto(result wire.TurnsResult) (*agentrewire.TranscriptImportTurnsResponse, error) {
	out := &agentrewire.TranscriptImportTurnsResponse{
		NextIndex: int32(result.NextIndex), HasMore: result.HasMore,
	}
	for _, turn := range result.Turns {
		entry := &agentrewire.TranscriptImportTurn{
			Index: int32(turn.Index), UserText: turn.UserText, Usage: usageToProto(turn.Usage),
			Model: turn.Model, StartedAt: transcriptMillis(turn.StartedAt),
			EndedAt: transcriptMillis(turn.EndedAt), ForkAnchor: turn.ForkAnchor, ErrorText: turn.ErrorText,
		}
		for _, img := range turn.UserImages {
			entry.UserImages = append(entry.UserImages, &agentrewire.TranscriptImportImage{
				MediaType: img.MediaType, Url: img.Source.URL, Inline: img.Source.Inline,
			})
		}
		for _, ev := range turn.Events {
			envelope := &agentrewire.RuntimeEventNotification{}
			if err := marshalEvent(envelope, ev); err != nil {
				return nil, err
			}
			entry.Events = append(entry.Events, envelope)
		}
		out.Turns = append(out.Turns, entry)
	}
	return out, nil
}

func TranscriptTurnsResultFromProto(response *agentrewire.TranscriptImportTurnsResponse) (wire.TurnsResult, error) {
	out := wire.TurnsResult{
		Turns:     make([]pkgimport.Turn, 0, len(response.GetTurns())),
		NextIndex: int(response.GetNextIndex()),
		HasMore:   response.GetHasMore(),
	}
	for _, entry := range response.GetTurns() {
		turn := pkgimport.Turn{
			Index: int(entry.GetIndex()), UserText: entry.GetUserText(), Usage: usageFromProto(entry.GetUsage()),
			Model: entry.GetModel(), StartedAt: transcriptTime(entry.GetStartedAt()),
			EndedAt: transcriptTime(entry.GetEndedAt()), ForkAnchor: entry.GetForkAnchor(),
			ErrorText: entry.GetErrorText(),
		}
		for _, img := range entry.GetUserImages() {
			turn.UserImages = append(turn.UserImages, blocks.ImageBlock{
				MediaType: img.GetMediaType(),
				Source:    blocks.BlobSource{URL: img.GetUrl(), Inline: img.GetInline()},
			})
		}
		for _, envelope := range entry.GetEvents() {
			ev, err := unmarshalEvent(envelope)
			if err != nil {
				return wire.TurnsResult{}, err
			}
			turn.Events = append(turn.Events, ev)
		}
		out.Turns = append(out.Turns, turn)
	}
	return out, nil
}

// ── Execute ─────────────────────────────────────────────────────────────────

func TranscriptExecuteParamsToProto(params wire.ExecuteParams) *agentrewire.TranscriptImportExecuteRequest {
	return &agentrewire.TranscriptImportExecuteRequest{
		Backend: params.Backend, Locator: params.Locator, SessionId: params.SessionID,
		AgentId: params.AgentID, AgentSyncId: params.AgentSyncID,
		PeerFingerprint: params.PeerFingerprint,
	}
}

func TranscriptExecuteParamsFromProto(request *agentrewire.TranscriptImportExecuteRequest) wire.ExecuteParams {
	return wire.ExecuteParams{
		Backend: request.GetBackend(), Locator: request.GetLocator(), SessionID: request.GetSessionId(),
		AgentID: request.GetAgentId(), AgentSyncID: request.GetAgentSyncId(),
		PeerFingerprint: request.GetPeerFingerprint(),
	}
}

func TranscriptExecuteResultToProto(result wire.ExecuteResult) *agentrewire.TranscriptImportExecuteResponse {
	return &agentrewire.TranscriptImportExecuteResponse{
		SessionId: result.SessionID, ProviderSessionId: result.ProviderSessionID,
		Cwd: result.Cwd, Title: result.Title, Turns: int32(result.Turns),
		AlreadyImported: result.AlreadyImported,
	}
}

func TranscriptExecuteResultFromProto(response *agentrewire.TranscriptImportExecuteResponse) wire.ExecuteResult {
	return wire.ExecuteResult{
		SessionID: response.GetSessionId(), ProviderSessionID: response.GetProviderSessionId(),
		Cwd: response.GetCwd(), Title: response.GetTitle(), Turns: int(response.GetTurns()),
		AlreadyImported: response.GetAlreadyImported(),
	}
}
