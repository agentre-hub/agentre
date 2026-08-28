// Package paired_agentred_entity 维护桌面端 LAN 直连 agentred 的充血实体。
package paired_agentred_entity

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// PairedAgentred 单台已配对 agentred 的元数据。
type PairedAgentred struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name              string `gorm:"column:name;type:text;not null"`
	URL               string `gorm:"column:url;type:text;not null"`
	DaemonFingerprint string `gorm:"column:daemon_fingerprint;type:text;not null"`
	InstanceUUID      string `gorm:"column:instance_uuid;type:text;not null"`
	TLSMode           string `gorm:"column:tls_mode;type:text;not null;default:'default'"`
	TLSCertPEM        string `gorm:"column:tls_cert_pem;type:text;not null;default:''"`
	PairedAt          int64  `gorm:"column:paired_at;type:bigint;not null"`
	LastSeenAt        int64  `gorm:"column:last_seen_at;type:bigint;not null;default:0"`
	LastError         string `gorm:"column:last_error;type:text;not null;default:''"`
	Status            int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime        int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime        int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

// TableName 绑定表名。
func (*PairedAgentred) TableName() string { return "paired_agentreds" }

// IsActive 是否处于启用态。
func (p *PairedAgentred) IsActive() bool { return p != nil && p.Status == consts.ACTIVE }

// IsRelayOnly 报告这一行只有中转一条路径。它由账号来源收编而来（本机从没 LAN 配对
// 过这台机器），因此没有 LAN 地址可拨；连接侧据此跳过直连、只走中转。
func (p *PairedAgentred) IsRelayOnly() bool {
	return p != nil && strings.TrimSpace(p.URL) == ""
}

// onlineWindowMs 是 last_seen_at 之后被视为「在线」的窗口。
const onlineWindowMs = int64(5 * 60 * 1000)

// IsOnline 给定 now（unix ms）判断是否仍在 5 min 探活窗口内。
// LastSeenAt 为 0（从未握手成功）一律 false。
func (p *PairedAgentred) IsOnline(nowMs int64) bool {
	if p == nil || p.LastSeenAt == 0 {
		return false
	}
	return nowMs-p.LastSeenAt <= onlineWindowMs
}

// validTLSModes 与 internal/daemon/client/tls.go::TLSMode 一致。
var validTLSModes = map[string]struct{}{
	"default": {}, "pin-cert": {}, "ca-bundle": {}, "skip-verify": {},
}

// Check 校验跨字段一致性。
func (p *PairedAgentred) Check(ctx context.Context) error {
	if p == nil {
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	if strings.TrimSpace(p.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	// 空 URL 合法，且**只**表示一件事：这一行没有 LAN 路径（IsRelayOnly）。
	// 账号里已有、本机从没配对过的 agentred 就长这样——中转按 DaemonFingerprint
	// 寻址，本来就不需要地址。填了地址就必须是个能拨的 ws 地址。
	url := strings.TrimSpace(p.URL)
	if url != "" && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		return i18n.NewError(ctx, code.RemoteDeviceURLInvalid)
	}
	if _, ok := validTLSModes[p.TLSMode]; !ok {
		return i18n.NewError(ctx, code.RemoteDeviceTLSConfigInvalid)
	}
	needsPEM := p.TLSMode == "pin-cert" || p.TLSMode == "ca-bundle"
	hasPEM := strings.TrimSpace(p.TLSCertPEM) != ""
	if needsPEM != hasPEM {
		return i18n.NewError(ctx, code.RemoteDeviceTLSConfigInvalid)
	}
	if strings.TrimSpace(p.DaemonFingerprint) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}
