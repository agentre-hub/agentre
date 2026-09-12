package update_svc

import "context"

// Service 是 update_svc 对外暴露的依赖倒置接口；App 绑定层通过 Update()
// 获取可替换实现，测试可通过 RegisterUpdate 注入 fake。
type Service interface {
	// CheckForUpdate 查询指定通道的最新版本，与当前 configs.Version 比对。
	CheckForUpdate(channel, mirrorPrefix string) (*UpdateInfo, error)
	// DownloadAndUpdate 下载并安装指定通道的最新版本；校验和取不到即失败。
	// onProgress 可为 nil；非 nil 时按字节流回调下载进度。
	DownloadAndUpdate(channel, mirrorPrefix string, onProgress func(downloaded, total int64)) error
	// GetAvailableMirrors 返回内置可用镜像列表（包含 GitHub 直连占位项）。
	GetAvailableMirrors() []MirrorInfo

	// GetChannel / SetChannel 持久化的更新通道。
	GetChannel(ctx context.Context) (string, error)
	SetChannel(ctx context.Context, channel string) error
	// GetMirror / SetMirror 持久化的下载镜像前缀。
	GetMirror(ctx context.Context) (string, error)
	SetMirror(ctx context.Context, mirror string) error
	// GetLastUpdateCheck / SetLastUpdateCheck 启动自动检查的 24h 节流时间戳。
	GetLastUpdateCheck(ctx context.Context) (int64, error)
	SetLastUpdateCheck(ctx context.Context, ts int64) error
}

var defaultService Service = &service{}

// Update 返回当前注册的更新服务实现。
func Update() Service {
	return defaultService
}

// RegisterUpdate 用于测试或扩展，替换默认实现。
func RegisterUpdate(svc Service) {
	defaultService = svc
}

// service 是默认实现，转发到包级函数。
type service struct{}

func (s *service) CheckForUpdate(channel, mirrorPrefix string) (*UpdateInfo, error) {
	return CheckForUpdate(channel, mirrorPrefix)
}

func (s *service) DownloadAndUpdate(channel, mirrorPrefix string, onProgress func(downloaded, total int64)) error {
	return DownloadAndUpdate(channel, mirrorPrefix, onProgress)
}

func (s *service) GetAvailableMirrors() []MirrorInfo {
	return GetAvailableMirrors()
}

func (s *service) GetChannel(ctx context.Context) (string, error) { return GetChannel(ctx) }
func (s *service) SetChannel(ctx context.Context, channel string) error {
	return SetChannel(ctx, channel)
}
func (s *service) GetMirror(ctx context.Context) (string, error) { return GetMirror(ctx) }
func (s *service) SetMirror(ctx context.Context, mirror string) error {
	return SetMirror(ctx, mirror)
}
func (s *service) GetLastUpdateCheck(ctx context.Context) (int64, error) {
	return GetLastUpdateCheck(ctx)
}
func (s *service) SetLastUpdateCheck(ctx context.Context, ts int64) error {
	return SetLastUpdateCheck(ctx, ts)
}
