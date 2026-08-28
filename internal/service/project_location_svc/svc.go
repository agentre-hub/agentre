// Package project_location_svc 维护 project × agentred 指纹维度的工作目录配置。
// 本地路径仍住 projects.path；本 svc 只承接远端 agentred 上的路径。账号内自然键
// 是 (project, device_fingerprint)（决策 26）；device_id 是由指纹解析出的本地
// 缓存——ListByProject 每次读取时按本机配对表自愈：查到该指纹就回填/刷新
// device_id 并呈现该行，查不到（R2b：未配对）就清空缓存、把该行从结果里剔除，
// 但不删除数据；配对/解除配对因此都在下一次读取时自动生效，不需要用户再做
// 第二件事。
package project_location_svc

import "context"

//go:generate mockgen -source svc.go -destination mock_project_location_svc/mock_svc.go

type ProjectLocationSvc interface {
	ListByProject(ctx context.Context, projectID int64) ([]*ProjectLocationView, error)
	Upsert(ctx context.Context, projectID int64, deviceID, path string) (*ProjectLocationView, error)
	RemoveByProjectAndDevice(ctx context.Context, projectID int64, deviceID string) error
}

type ProjectLocationView struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"projectId"`
	DeviceID   string `json:"deviceId"`
	Path       string `json:"path"`
	DeviceName string `json:"deviceName"`
	Online     bool   `json:"online"`
}

var defaultSvc ProjectLocationSvc = &projectLocationImpl{}

func Default() ProjectLocationSvc { return defaultSvc }
