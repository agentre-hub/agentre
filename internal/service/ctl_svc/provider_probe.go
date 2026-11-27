package ctl_svc

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
)

// 供应商的测试连接与模型发现（agrctl test provider / discover models 的桌面模式）：都是读，
// 不改任何资源，所以会话 token 也能调；导入模型是写，走既有的 /ctl/v1/resources 写路径
//（审批规则不变）。

// ProbeResult 是一次测试连接的结果。上游失败是 OK=false 的结果，不是 HTTP 错误；消息不含密钥。
type ProbeResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// DiscoveredModel 是上游列出的一个模型。
type DiscoveredModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

// ProviderProbes 是对已保存供应商发起的上游探测。
type ProviderProbes interface {
	TestProvider(ctx context.Context, providerID int64, modelKey string) (ProbeResult, error)
	DiscoverModels(ctx context.Context, providerID int64) ([]DiscoveredModel, error)
}

// liveProbes 把探测接到 llm_provider_svc 上（每次调用现取服务单例）。
type liveProbes struct{ providers func() ProviderService }

func (p liveProbes) TestProvider(ctx context.Context, providerID int64, modelKey string) (ProbeResult, error) {
	resp, err := p.providers().TestConnection(ctx, &llm_provider_svc.TestConnectionRequest{ID: providerID, ModelKey: modelKey})
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{OK: resp.OK, Message: resp.Message}, nil
}

func (p liveProbes) DiscoverModels(ctx context.Context, providerID int64) ([]DiscoveredModel, error) {
	resp, err := p.providers().PreviewModels(ctx, &llm_provider_svc.PreviewModelsRequest{ID: providerID})
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveredModel, 0, len(resp.Items))
	for _, m := range resp.Items {
		if m == nil {
			continue
		}
		// 上游列表只给 id；名字沿用 id（与 agentred 的发现结果同一形状）。
		out = append(out, DiscoveredModel{ModelID: m.ID, Name: m.ID})
	}
	return out, nil
}

type providerProbeRequest struct {
	ProviderID int64  `json:"providerId"`
	ModelKey   string `json:"modelKey"`
}

// decodeProbe 校验 POST 与请求体并取出探测端口；已写出错误响应时 ok=false。
func (h *ctlHandler) decodeProbe(w http.ResponseWriter, r *http.Request) (providerProbeRequest, ProviderProbes, bool) {
	var req providerProbeRequest
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "provider probes require POST")
		return req, nil, false
	}
	if h.resources.Probes == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return req, nil, false
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProviderID <= 0 {
		writeErr(w, http.StatusBadRequest, "providerId is required")
		return req, nil, false
	}
	return req, h.resources.Probes, true
}

func (h *ctlHandler) serveProviderTest(w http.ResponseWriter, r *http.Request) {
	req, probes, ok := h.decodeProbe(w, r)
	if !ok {
		return
	}
	res, err := probes.TestProvider(r.Context(), req.ProviderID, req.ModelKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *ctlHandler) serveProviderDiscover(w http.ResponseWriter, r *http.Request) {
	req, probes, ok := h.decodeProbe(w, r)
	if !ok {
		return
	}
	models, err := probes.DiscoverModels(r.Context(), req.ProviderID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []DiscoveredModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
