// Package device_facts_svc 是桌面端眼中的「设备情况」(spec 2026-11-25「设备与设备
// 情况上报」):这台桌面端自己的情况(探测、记下、按时机报给账号 server),以及已配对
// agentred 的情况(未登录账号时经 device.facts 直接向它们读,规格「概念与归属」末段)。
//
// 情况是**事实**:设备自己探、自己报,这里只读、只决定何时上报,不参与同步、不可编辑
// (可编辑的设备偏好在 device_pref_svc)。其它桌面端的情况日后从 server 读;在那之前
// 列表里只有本机与已配对的 agentred,每台交出已知的最后一份情况与它的采集时间。
package device_facts_svc

import (
	"context"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// remoteReadTimeout 是读一台 agentred 情况的上限:一台卡住的设备不该拖住整张列表。
const remoteReadTimeout = 10 * time.Second

// DeviceFactsSvc 设备情况应用服务。
type DeviceFactsSvc interface {
	// List 参与派发的设备的情况:本机在前,已配对的 agentred 在后。读不到的设备交出
	// 最后一次读到的情况(没有则为空)并标为够不着。
	List(ctx context.Context) ([]*DeviceFactsView, error)
	// Refresh 让一台设备(本机或已配对的 agentred)立即重探并交回新情况;本机的新情况
	// 同时报给 server,agentred 的由它自己报。fingerprint 为空即本机。
	Refresh(ctx context.Context, fingerprint devicefp.Carrier) (*DeviceFactsView, error)
	// AccountChannelConnected 连上账号中继(首次与每次重连):本机情况整份上报一次。
	AccountChannelConnected(ctx context.Context)
	// Start 起本机情况的周期变化检查(变了才报),直到 ctx 结束。
	Start(ctx context.Context)
}

// DeviceFactsView 设备页的一行。
type DeviceFactsView struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	// Name 已配对设备的显示名;本机为空,界面写「本机」。
	Name string `json:"name"`
	// Kind 是 desktop 或 agentred。
	Kind   string `json:"kind"`
	IsSelf bool   `json:"isSelf"`
	// Facts 是已知的最后一份情况,采集时间在 Facts.CollectedAt;从没读到过时为 nil。
	Facts *syncwire.DeviceFactsReport `json:"facts"`
	// Reachable 这一次读到了它;为 false 时 Facts 是之前读到的那份(或 nil)。
	Reachable bool `json:"reachable"`
}

// SelfFacts 是本机情况(devicefacts.Reporter 满足它)。
type SelfFacts interface {
	Current(ctx context.Context) (syncwire.DeviceFactsReport, error)
	Refresh(ctx context.Context) (syncwire.DeviceFactsReport, error)
	Connected(ctx context.Context) error
	Run(ctx context.Context, every time.Duration)
}

// PairedDevice 一台已配对的 agentred。
type PairedDevice struct {
	ID          int64
	Fingerprint devicefp.Carrier
	Name        string
}

// Devices 是「有哪些设备、怎么读它们」的窄端口。生产实现读 remote_device_svc 并经
// 连接池调 device.facts。
type Devices interface {
	Self() (devicefp.Carrier, error)
	Paired(ctx context.Context) ([]PairedDevice, error)
	ReadFacts(ctx context.Context, deviceID int64, refresh bool) (syncwire.DeviceFactsReport, error)
}

type deviceFactsSvc struct {
	self    SelfFacts
	devices Devices

	mu sync.Mutex
	// lastKnown 是每台已配对设备最后一次读到的情况:设备离线后界面仍显示它并标出时间。
	lastKnown map[devicefp.Carrier]syncwire.DeviceFactsReport
}

// Option 定制 New 构造出的实例。
type Option func(*deviceFactsSvc)

// WithSelf 注入本机情况。
func WithSelf(s SelfFacts) Option { return func(svc *deviceFactsSvc) { svc.self = s } }

// WithDevices 注入设备来源(测试用)。
func WithDevices(d Devices) Option { return func(svc *deviceFactsSvc) { svc.devices = d } }

// New 构造实现。生产由 bootstrap 用 WithSelf(NewSelfReporter(...)) 装配。
func New(opts ...Option) DeviceFactsSvc {
	s := &deviceFactsSvc{devices: remoteDevices{}, lastKnown: map[devicefp.Carrier]syncwire.DeviceFactsReport{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

var defaultSvc DeviceFactsSvc

// Default 取默认实现;未装配时为 nil。
func Default() DeviceFactsSvc { return defaultSvc }

// SetDefault 由 bootstrap 注入实现。
func SetDefault(s DeviceFactsSvc) { defaultSvc = s }

func (s *deviceFactsSvc) List(ctx context.Context) ([]*DeviceFactsView, error) {
	selfFP, err := s.devices.Self()
	if err != nil {
		return nil, err
	}
	paired, err := s.devices.Paired(ctx)
	if err != nil {
		return nil, err
	}
	remotes := otherDevices(paired, selfFP)
	views := make([]*DeviceFactsView, 0, 1+len(remotes))
	views = append(views, s.selfView(ctx, selfFP))
	remoteViews := make([]*DeviceFactsView, len(remotes))
	var wg sync.WaitGroup
	for i, d := range remotes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remoteViews[i] = s.readRemote(ctx, d, false)
		}()
	}
	wg.Wait()
	return append(views, remoteViews...), nil
}

func (s *deviceFactsSvc) Refresh(ctx context.Context, fingerprint devicefp.Carrier) (*DeviceFactsView, error) {
	selfFP, err := s.devices.Self()
	if err != nil {
		return nil, err
	}
	if fingerprint == "" || fingerprint == selfFP {
		report, err := s.self.Refresh(ctx)
		if err != nil {
			return nil, err
		}
		return &DeviceFactsView{Fingerprint: selfFP, Kind: string(dispatch.KindDesktop), IsSelf: true, Facts: &report, Reachable: true}, nil
	}
	paired, err := s.devices.Paired(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range otherDevices(paired, selfFP) {
		if d.Fingerprint != fingerprint {
			continue
		}
		view := s.readRemote(ctx, d, true)
		if !view.Reachable {
			return nil, i18n.NewError(ctx, code.DeviceFactsDeviceUnreachable)
		}
		return view, nil
	}
	return nil, i18n.NewError(ctx, code.DeviceFactsDeviceNotFound)
}

func (s *deviceFactsSvc) AccountChannelConnected(ctx context.Context) {
	_ = s.self.Connected(ctx) // 失败已由 Reporter 记过,下一次检查变化时重试
}

func (s *deviceFactsSvc) Start(ctx context.Context) {
	go s.self.Run(ctx, devicefacts.ChangeCheckInterval)
}

func (s *deviceFactsSvc) selfView(ctx context.Context, selfFP devicefp.Carrier) *DeviceFactsView {
	view := &DeviceFactsView{Fingerprint: selfFP, Kind: string(dispatch.KindDesktop), IsSelf: true, Reachable: true}
	report, err := s.self.Current(ctx)
	if err != nil {
		// 已由 Reporter 记过;本机那一行显示「没有情况」,不拖垮整张列表。
		return view
	}
	view.Facts = &report
	return view
}

// readRemote 读一台已配对 agentred 的情况。读到就记下;读不到交出上一次读到的那份。
func (s *deviceFactsSvc) readRemote(ctx context.Context, d PairedDevice, refresh bool) *DeviceFactsView {
	view := &DeviceFactsView{Fingerprint: d.Fingerprint, Name: d.Name, Kind: string(dispatch.KindAgentred)}
	readCtx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()
	report, err := s.devices.ReadFacts(readCtx, d.ID, refresh)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		logger.Ctx(ctx).Debug("device_facts_svc.readRemote: device facts unavailable, showing last known",
			zap.Int64("deviceId", d.ID), zap.Bool("refresh", refresh), zap.Error(err))
		if last, ok := s.lastKnown[d.Fingerprint]; ok {
			view.Facts = &last
		}
		return view
	}
	s.lastKnown[d.Fingerprint] = report
	view.Facts = &report
	view.Reachable = true
	return view
}

// otherDevices 已配对设备里去掉本机与没有指纹的行。
func otherDevices(paired []PairedDevice, selfFP devicefp.Carrier) []PairedDevice {
	out := make([]PairedDevice, 0, len(paired))
	for _, d := range paired {
		if d.Fingerprint == "" || d.Fingerprint == selfFP {
			continue
		}
		out = append(out, d)
	}
	return out
}
