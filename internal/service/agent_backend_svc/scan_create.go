package agent_backend_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/utils/httputils"

	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// defaultNameForType 按 CLI 后端类型返回自动扫描时的默认名称。
func defaultNameForType(backendType string) string {
	switch backendType {
	case "claudecode":
		return "Claude Code CLI"
	case "codex":
		return "Codex CLI"
	case "piagent":
		return "Pi Agent CLI"
	default:
		return backendType + " CLI"
	}
}

// isNameDuplicated 判断 err 是否为 i18n 名称重复错误(AgentBackendNameDuplicated)。
func isNameDuplicated(err error) bool {
	var httpErr *httputils.Error
	if errors.As(err, &httpErr) {
		return httpErr.Code == int(code.AgentBackendNameDuplicated)
	}
	return false
}

// setLocalCLIOverlay 把扫描命中的绝对路径写进本机那一行 CLI 覆盖（DeviceID 留空 =
// 这台机器自己的指纹）。远端设备服务未装配时静默跳过——那只出现在窄单测组合里，
// bootstrap 在任何公开写入之前都已经把它装好。
func (s *agentBackendSvc) setLocalCLIOverlay(ctx context.Context, backendSyncID, cliPath string) error {
	if backendSyncID == "" || remote_device_svc.Default() == nil {
		return nil
	}
	_, err := s.SetCLIOverlay(ctx, &SetCLIOverlayRequest{BackendSyncID: backendSyncID, CLIPath: cliPath})
	return err
}

// ScanAndCreateAgentBackends 扫描系统 PATH 中的 Claude Code / Codex / Pi Agent CLI，
// 命中时自动创建对应的 Agent 后端配置。
func (s *agentBackendSvc) ScanAndCreateAgentBackends(ctx context.Context, _ *ScanAndCreateAgentBackendsRequest) (*ScanAndCreateAgentBackendsResponse, error) {
	results := cliprober.ScanAllCLIs()
	items := make([]*ScanResultItem, 0, len(results))
	for _, r := range results {
		item := &ScanResultItem{
			Type:    r.BackendType,
			Name:    defaultNameForType(r.BackendType),
			CLIPath: r.Path,
			Found:   r.Found,
		}
		if !r.Found {
			item.Error = "binary not found in system PATH"
			items = append(items, item)
			continue
		}
		// 决策 25:撞名判据把墓碑一并计入,而不是只看 ACTIVE 行(Create 内部
		// FindByName 的判据)。不这么做,「扫描建 → 被删 → 再扫描建」每轮都会留一条
		// 新墓碑(Problem 19:Claude Code CLI / Codex CLI / Pi Agent CLI 各 47 条
		// 同名墓碑,createtime 完全相同),把决策 24 的回收持续抵消掉。
		if exists, err := agent_backend_repo.AgentBackend().ExistsByName(ctx, item.Name); err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		} else if exists {
			item.Skipped = true
			item.Error = "name already exists"
			items = append(items, item)
			continue
		}
		resp, err := s.Create(ctx, &CreateBackendRequest{
			Type: r.BackendType,
			Name: item.Name,
		})
		if err != nil {
			if isNameDuplicated(err) {
				item.Skipped = true
				item.Error = "name already exists"
			} else {
				item.Error = err.Error()
			}
		} else {
			item.Created = true
			item.BackendID = resp.Item.ID
			// 扫出来的路径落本机那一行覆盖：路径不随身份一起写（见
			// CreateBackendRequest 的注释），这一条路没有编辑器替它调那个端口，
			// 所以自己调。远端设备服务没装配时（窄单测组合）跳过，与它此前的
			// 容忍一致。
			if err := s.setLocalCLIOverlay(ctx, resp.Item.SyncID, r.Path); err != nil {
				item.Error = err.Error()
			}
		}
		items = append(items, item)
	}
	return &ScanAndCreateAgentBackendsResponse{Results: items}, nil
}
