package remote_device_svc

import (
	"strconv"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// toView projects entity → DTO; computes Online via current wall clock and
// folds in the process-local probe results (R18 的 daemon 版本探测)。
//
// 投影只有这一处:Add / List / Get / Refresh 交给前端的都是同一张视图,漏掉哪一条
// 路径,配对设备面板就会在那条路径上把说明抖掉(比如手动 Refresh 一下,说明没了,
// 而那台 daemon 还是老版本)。
func (s *service) toView(p *paired_agentred_entity.PairedAgentred) *DeviceView {
	if p == nil {
		return nil
	}
	return &DeviceView{
		ID:                     p.ID,
		Name:                   p.Name,
		URL:                    p.URL,
		DaemonFingerprint:      p.DaemonFingerprint,
		InstanceUUID:           p.InstanceUUID,
		TLSMode:                p.TLSMode,
		TLSCertPEM:             p.TLSCertPEM,
		PairedAt:               p.PairedAt,
		LastSeenAt:             p.LastSeenAt,
		LastError:              p.LastError,
		Online:                 p.IsOnline(time.Now().UnixMilli()),
		SupportsLLMModelTarget: s.SupportsLLMModelTarget(p.ID),
	}
}
