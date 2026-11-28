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
	"sort"
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
//
// 读(List)只读缓存,从不为它发 RPC:派发的每次 Plan、可用性与界面都走它。缓存由三路
// 喂:本机采集;已配对 agentred 的 device.facts(Sync 周期巡检、DeviceOnline 上线时、
// Refresh);账号 server 里其它桌面端与账号下 agentred 的最后一次上报(Sync)。
type DeviceFactsSvc interface {
	// List 参与派发的设备的情况:本机在前,已配对的 agentred 其次,再是只在账号里出现的
	// 设备。每台交出缓存里最后一份情况与它的上报时间(没有则 Facts 为空);不做任何网络请求。
	List(ctx context.Context) ([]*DeviceFactsView, error)
	// Refresh 让一台设备(本机或已配对的 agentred)立即重探并交回新情况(也记进缓存);
	// 本机的新情况同时报给 server,agentred 的由它自己报。fingerprint 为空即本机。
	Refresh(ctx context.Context, fingerprint devicefp.Carrier) (*DeviceFactsView, error)
	// Sync 巡检一次:并行读每台已配对 agentred 的情况,再从账号 server 读各设备最后一次
	// 上报并并入缓存(本机与直连够得着的 agentred 不被 server 覆盖)。读不到的设备与
	// server 不可达都保留已缓存的值。
	Sync(ctx context.Context)
	// DeviceOnline 一台已配对 agentred(重新)上线:立即读它的情况记进缓存。
	DeviceOnline(ctx context.Context, deviceID int64)
	// AccountChannelConnected 连上账号中继(首次与每次重连):本机情况整份上报一次。
	AccountChannelConnected(ctx context.Context)
	// Start 起本机情况的周期变化检查(变了才报)与缓存的周期巡检,直到 ctx 结束。
	Start(ctx context.Context)
}

// DeviceFactsView 设备页的一行。
type DeviceFactsView struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	// Name 设备的显示名;本机为空,界面写「本机」。
	Name string `json:"name"`
	// Kind 是 desktop 或 agentred。
	Kind   string `json:"kind"`
	IsSelf bool   `json:"isSelf"`
	// Facts 是已知的最后一份情况,探测时间在 Facts.CollectedAt;从没读到过时为 nil。
	Facts *syncwire.DeviceFactsReport `json:"facts"`
	// ReportedAt 是这份情况的上报时间(Unix 毫秒),界面据此写「多久之前」:经 server 读到
	// 的是 server 收到它的时刻,直接读到的与本机是 Facts.CollectedAt;Facts 为空时为 0。
	ReportedAt int64 `json:"reportedAt"`
	// Reachable 最近一次直接读它成功(本机恒为 true);为 false 时 Facts 是之前读到的那份
	// (或经 server 读到的那份,或 nil)。
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

// ServerFacts 是账号 server 上各设备最后一次上报的读口(server_svc.DeviceFactsReader)。
type ServerFacts interface {
	ListDeviceFacts(ctx context.Context) (syncwire.DeviceFactsList, error)
}

// cacheEntry 是缓存里一台非本机设备:最后一份情况、它的上报时间,以及来源。
type cacheEntry struct {
	name, kind string
	report     *syncwire.DeviceFactsReport
	reportedAt int64
	// direct 最近一次直接读成功;设备离线后界面仍显示 report 并标出时间。
	direct bool
}

type deviceFactsSvc struct {
	self    SelfFacts
	devices Devices
	server  ServerFacts

	mu    sync.Mutex
	cache map[devicefp.Carrier]*cacheEntry
}

// Option 定制 New 构造出的实例。
type Option func(*deviceFactsSvc)

// WithSelf 注入本机情况。
func WithSelf(s SelfFacts) Option { return func(svc *deviceFactsSvc) { svc.self = s } }

// WithDevices 注入设备来源(测试用)。
func WithDevices(d Devices) Option { return func(svc *deviceFactsSvc) { svc.devices = d } }

// WithServer 注入账号 server 的设备情况读口;不注入就只有本机与已配对的 agentred。
func WithServer(r ServerFacts) Option { return func(svc *deviceFactsSvc) { svc.server = r } }

// New 构造实现。生产由 bootstrap 用 WithSelf(NewSelfReporter(...)) 装配。
func New(opts ...Option) DeviceFactsSvc {
	s := &deviceFactsSvc{devices: remoteDevices{}, cache: map[devicefp.Carrier]*cacheEntry{}}
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
	views := []*DeviceFactsView{s.selfView(ctx, selfFP)}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[devicefp.Carrier]bool{}
	for _, d := range remotes {
		seen[d.Fingerprint] = true
		views = append(views, s.cachedView(d.Fingerprint, d.Name, string(dispatch.KindAgentred)))
	}
	var accountOnly []devicefp.Carrier
	for fp := range s.cache {
		if fp != selfFP && !seen[fp] {
			accountOnly = append(accountOnly, fp)
		}
	}
	sort.Slice(accountOnly, func(i, j int) bool { return accountOnly[i] < accountOnly[j] })
	for _, fp := range accountOnly {
		e := s.cache[fp]
		views = append(views, s.cachedView(fp, e.name, e.kind))
	}
	return views, nil
}

// cachedView 把缓存里一台设备摆成一行;没有缓存时情况为空。调用方持有 s.mu。
func (s *deviceFactsSvc) cachedView(fp devicefp.Carrier, name, kind string) *DeviceFactsView {
	view := &DeviceFactsView{Fingerprint: fp, Name: name, Kind: kind}
	if e := s.cache[fp]; e != nil {
		view.Reachable = e.direct
		if e.report != nil {
			report := *e.report
			view.Facts = &report
			view.ReportedAt = e.reportedAt
		}
	}
	return view
}

func (s *deviceFactsSvc) Sync(ctx context.Context) {
	selfFP, err := s.devices.Self()
	if err != nil {
		return
	}
	if paired, err := s.devices.Paired(ctx); err == nil {
		var wg sync.WaitGroup
		for _, d := range otherDevices(paired, selfFP) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.readRemote(ctx, d, false)
			}()
		}
		wg.Wait()
	}
	s.mergeServer(ctx, selfFP)
}

func (s *deviceFactsSvc) DeviceOnline(ctx context.Context, deviceID int64) {
	selfFP, err := s.devices.Self()
	if err != nil {
		return
	}
	paired, err := s.devices.Paired(ctx)
	if err != nil {
		return
	}
	for _, d := range otherDevices(paired, selfFP) {
		if d.ID == deviceID {
			s.readRemote(ctx, d, false)
			return
		}
	}
}

// mergeServer 把账号 server 里其它设备最后一次上报并进缓存。本机自己不取(本机有一手的);
// 直连刚读成功的 agentred 不取(一手的更新);读不到 server 时一律不动缓存。
func (s *deviceFactsSvc) mergeServer(ctx context.Context, selfFP devicefp.Carrier) {
	if s.server == nil {
		return
	}
	list, err := s.server.ListDeviceFacts(ctx)
	if err != nil {
		logger.Ctx(ctx).Debug("device_facts_svc.mergeServer: server device facts unavailable, keeping cache", zap.Error(err))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range list.Devices {
		fp := devicefp.Carrier(item.Fingerprint)
		if fp == "" || fp == selfFP {
			continue
		}
		cur := s.cache[fp]
		if cur != nil && cur.direct {
			continue
		}
		next := &cacheEntry{name: item.Name, kind: item.Kind}
		if cur != nil {
			if cur.name != "" && next.name == "" {
				next.name = cur.name
			}
			if cur.report != nil {
				next.report, next.reportedAt = cur.report, cur.reportedAt
			}
		}
		if item.Reported && item.Report != nil &&
			(next.report == nil || item.Report.CollectedAt >= next.report.CollectedAt) {
			report := *item.Report
			next.report, next.reportedAt = &report, item.ReportedAt
		}
		s.cache[fp] = next
	}
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
		return &DeviceFactsView{Fingerprint: selfFP, Kind: string(dispatch.KindDesktop), IsSelf: true, Facts: &report, ReportedAt: report.CollectedAt, Reachable: true}, nil
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
	// 刚连上账号:顺手把 server 里其它设备的最后一次上报读进缓存(不读 agentred)。
	if selfFP, err := s.devices.Self(); err == nil {
		s.mergeServer(ctx, selfFP)
	}
}

func (s *deviceFactsSvc) Start(ctx context.Context) {
	go s.self.Run(ctx, devicefacts.ChangeCheckInterval)
	go func() {
		s.Sync(ctx)
		ticker := time.NewTicker(devicefacts.ChangeCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Sync(ctx)
			}
		}
	}()
}

func (s *deviceFactsSvc) selfView(ctx context.Context, selfFP devicefp.Carrier) *DeviceFactsView {
	view := &DeviceFactsView{Fingerprint: selfFP, Kind: string(dispatch.KindDesktop), IsSelf: true, Reachable: true}
	report, err := s.self.Current(ctx)
	if err != nil {
		// 已由 Reporter 记过;本机那一行显示「没有情况」,不拖垮整张列表。
		return view
	}
	view.Facts = &report
	view.ReportedAt = report.CollectedAt
	return view
}

// readRemote 直接读一台已配对 agentred 的情况并记进缓存。读不到时保留上一份、标为够不着。
func (s *deviceFactsSvc) readRemote(ctx context.Context, d PairedDevice, refresh bool) *DeviceFactsView {
	readCtx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()
	report, err := s.devices.ReadFacts(readCtx, d.ID, refresh)
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.cache[d.Fingerprint]
	if e == nil {
		e = &cacheEntry{}
		s.cache[d.Fingerprint] = e
	}
	e.name, e.kind = d.Name, string(dispatch.KindAgentred)
	if err != nil {
		logger.Ctx(ctx).Debug("device_facts_svc.readRemote: device facts unavailable, keeping last known",
			zap.Int64("deviceId", d.ID), zap.Bool("refresh", refresh), zap.Error(err))
		e.direct = false
		return s.cachedView(d.Fingerprint, d.Name, e.kind)
	}
	e.report, e.reportedAt, e.direct = &report, report.CollectedAt, true
	return s.cachedView(d.Fingerprint, d.Name, e.kind)
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
