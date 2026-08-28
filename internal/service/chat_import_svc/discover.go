package chat_import_svc

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

// ListCandidates 扫描一台设备上的候选。
//
// 两条口径来自 spec「发现、去重与来源」:
//   - **任一后端失败不影响其余后端出结果**。某个后端的目录读不动、不存在、这台机器
//     上根本没装那个 CLI,都只让它自己那一档报出原因 —— 一档失败就整体报错的话,
//     装了三个 CLI 的机器上少装一个就什么都看不见。
//   - **库里已经有的候选照常列出但不可选**。藏起来会让用户以为扫描漏了。
func (s *chatImportSvc) ListCandidates(ctx context.Context, req *ListCandidatesRequest) (*ListCandidatesResponse, error) {
	if req == nil {
		req = &ListCandidatesRequest{}
	}
	sources, err := s.sources(ctx, req.DeviceID)
	if err != nil {
		return nil, failed(ctx, codeOf(err), err, zap.Int64("deviceId", req.DeviceID))
	}
	wanted := backendFilter(req.Backends)
	filter := transcriptimport.Filter{
		CwdPrefix:  req.CwdPrefix,
		TitleQuery: req.TitleQuery,
		Limit:      req.Limit,
	}
	if req.Since > 0 {
		filter.Since = time.UnixMilli(req.Since)
	}

	out := &ListCandidatesResponse{Candidates: []CandidateView{}, Issues: []BackendScanIssue{}}
	var all []transcriptimport.Candidate
	deviceIssue := false
	for _, src := range sources {
		if src == nil {
			continue
		}
		backend := src.Backend()
		if wanted != nil {
			if _, ok := wanted[backend]; !ok {
				continue
			}
		}
		got, scanErr := src.Scan(ctx, filter)
		if scanErr != nil {
			// 日志与 issue 一起只记一次,且只记后端、设备与失败原因 ——
			// 不记转录内容(spec「隐私」)。
			if status, isDevice := deviceScanStatus(scanErr); isDevice {
				// 设备级的一句话只说一遍:整台设备拨不通 / daemon 太旧时,三个
				// 后端会给出同一个答案,按后端重复三遍只是噪声。
				if !deviceIssue {
					logger.Ctx(ctx).Warn("chat_import_svc.ListCandidates: device cannot answer",
						zap.Int64("deviceId", req.DeviceID), zap.String("status", status), zap.Error(scanErr))
					out.Issues = append(out.Issues, BackendScanIssue{Status: status, Reason: scanErr.Error()})
					deviceIssue = true
				}
				continue
			}
			logger.Ctx(ctx).Warn("chat_import_svc.ListCandidates: backend scan failed",
				zap.String("backend", string(backend)), zap.Int64("deviceId", req.DeviceID), zap.Error(scanErr))
			out.Issues = append(out.Issues, BackendScanIssue{
				Backend: string(backend),
				Status:  ScanStatusUnavailable,
				Reason:  scanErr.Error(),
			})
			continue
		}
		all = append(all, got...)
	}

	ids := make([]string, 0, len(all))
	for _, c := range all {
		ids = append(ids, c.ProviderSessionID)
	}
	importedBy, err := s.importedSessionIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// 合并后按最后活动时间倒序:三个后端各自的顺序在这里失效,用户看到的是一条
	// 跨后端的时间线。
	sort.SliceStable(all, func(i, j int) bool { return all[i].EndedAt.After(all[j].EndedAt) })
	for _, c := range all {
		view := CandidateView{
			Backend:           string(c.Backend),
			ProviderSessionID: c.ProviderSessionID,
			Title:             c.Title,
			Cwd:               c.Cwd,
			StartedAt:         unixMilli(c.StartedAt),
			EndedAt:           unixMilli(c.EndedAt),
			Turns:             c.Turns,
			Origin:            string(c.Origin),
			Locator:           string(c.Locator),
		}
		if id, ok := importedBy[c.ProviderSessionID]; ok {
			view.Imported = true
			view.ImportedSessionID = id
		}
		out.Candidates = append(out.Candidates, view)
	}
	if req.Limit > 0 && len(out.Candidates) > req.Limit {
		out.Candidates = out.Candidates[:req.Limit]
	}
	return out, nil
}

// backendFilter 把请求里的后端白名单归一化;空表示不过滤。
func backendFilter(backends []string) map[agent_backend_entity.BackendType]struct{} {
	set := map[agent_backend_entity.BackendType]struct{}{}
	for _, b := range backends {
		if strings.TrimSpace(b) == "" {
			continue
		}
		set[agent_backend_entity.BackendType(b)] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
