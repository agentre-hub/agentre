// Package project_location_svc 维护 project × agentred 指纹维度的工作目录配置。
// 本地路径仍住 projects.path；本 svc 只承接远端 agentred 上的路径。账号内自然键
// 是 (project, device_fingerprint)（决策 26）；device_id 是由指纹解析出的本地
// 缓存——ListByProject 每次读取时按本机配对表自愈：查到该指纹就回填/刷新
// device_id 并呈现该行，查不到（R2b：未配对）就清空缓存、把该行从结果里剔除，
// 但不删除数据；配对/解除配对因此都在下一次读取时自动生效，不需要用户再做
// 第二件事。
package project_location_svc

import (
	"context"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type ProjectLocationSvc interface {
	ListByProject(ctx context.Context, projectID int64) ([]*ProjectLocationView, error)
	Upsert(ctx context.Context, projectID int64, deviceID, path string) (*ProjectLocationView, error)
	RemoveByProjectAndDevice(ctx context.Context, projectID int64, deviceID string) error
}

type ProjectLocationView struct {
	ID        int64 `json:"id"`
	ProjectID int64 `json:"projectId"`
	// DeviceID 是 paired_agentreds 的数字 id，**只是缓存**：解不开指纹时会被清空
	// （见 impl.go 的 R2b 分支）。宿主拿到的 Agent 设备键是指纹而不是它,
	// 所以要把两边对起来请用 DeviceFingerprint。
	DeviceID string `json:"deviceId"`
	// DeviceFingerprint 是账号内的自然键 (project, device_fingerprint)，也是
	// chat_svc 给前端的 ChatAgentItem.DeviceID 用的同一个键。前端据此判断
	// 「这个远端 Agent 的机器配没配过路径」。
	DeviceFingerprint devicefp.Carrier `json:"deviceFingerprint"`
	Path              string           `json:"path"`
	DeviceName        string           `json:"deviceName"`
	Online            bool             `json:"online"`
}

var defaultSvc ProjectLocationSvc = &projectLocationImpl{}

func Default() ProjectLocationSvc { return defaultSvc }
