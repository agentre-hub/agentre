// Package device_pref_svc 是设备页的可编辑项（spec 2026-11-25「设备与设备情况上报 › 设备页」）：
// 设备标签、账号设备顺序、「本机始终排第一」本地开关，以及仅此设备的 CLI 路径覆盖
// （按（运行时类型, 设备）存，决策 13）。
//
// 参与派发的设备只有本机与已配对的 agentred（未登录账号时也是这两类）。设备事实（在线、
// 运行时、凭据）不在这里——那是设备自己上报的只读数据。
package device_pref_svc

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// DevicePrefSvc 设备偏好应用服务。
type DevicePrefSvc interface {
	// ListDevices 参与派发的设备，按「有效顺序」：账号设备顺序，开着「本机始终排第一」
	// 时本机固定第一位。没有偏好行的设备（新配对的）排在已有顺序之后。
	ListDevices(ctx context.Context) (*ListDevicesResponse, error)
	// Reorder 按给出的顺序排前，没列出的设备保持原相对顺序排在后面。
	Reorder(ctx context.Context, order []devicefp.Carrier) error
	// UpdateLabels 整体替换一台设备的标签（去空白、去重）。
	UpdateLabels(ctx context.Context, fingerprint devicefp.Carrier, labels []string) error
	// GetLocalFirst / SetLocalFirst 「在这台电脑上，本机始终排第一」，只存本机、默认开。
	GetLocalFirst(ctx context.Context) (bool, error)
	SetLocalFirst(ctx context.Context, on bool) error
	// GetCLIPath 一台设备上某运行时的 CLI 路径覆盖；空 = 自动探测。device 为空取本机。
	GetCLIPath(ctx context.Context, runtimeType string, device devicefp.Carrier) (string, error)
	// SetCLIPath 写覆盖；空路径删除覆盖（回到自动探测）。device 为空取本机。
	SetCLIPath(ctx context.Context, runtimeType string, device devicefp.Carrier, path string) error
}

// DeviceItem 设备列表的一行（只含可编辑项与展示所需的身份）。
type DeviceItem struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	Name        string           `json:"name"`
	IsSelf      bool             `json:"isSelf"`
	Labels      []string         `json:"labels"`
}

// ListDevicesResponse 设备列表与本地开关。
type ListDevicesResponse struct {
	Items      []*DeviceItem `json:"items"`
	LocalFirst bool          `json:"localFirst"`
}

// PairedDevice 一台已配对设备的身份。
type PairedDevice struct {
	Fingerprint devicefp.Carrier
	Name        string
}

// DeviceSource 是本服务眼中「有哪些设备」的窄端口（ISP）：本机指纹与按配对时间排好的
// 已配对设备。生产实现读 remote_device_svc。
type DeviceSource interface {
	Self() (devicefp.Carrier, error)
	// Paired 已配对设备，按配对时间升序。
	Paired(ctx context.Context) ([]PairedDevice, error)
}

// TxRunner 把一次改顺序的全部写入收进一个原子单元；单测顶一个直接调用回调的假实现。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type dbTxRunner struct{}

func (dbTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(db.WithContextDB(ctx, tx))
	})
}

type remoteDeviceSource struct{}

func (remoteDeviceSource) Self() (devicefp.Carrier, error) {
	return remote_device_svc.Default().DeviceFingerprint()
}

func (remoteDeviceSource) Paired(ctx context.Context) ([]PairedDevice, error) {
	rows, err := remote_device_svc.Default().List(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(rows, func(a, b *remote_device_svc.DeviceView) int {
		switch {
		case a.PairedAt < b.PairedAt:
			return -1
		case a.PairedAt > b.PairedAt:
			return 1
		}
		return int(a.ID - b.ID)
	})
	out := make([]PairedDevice, 0, len(rows))
	for _, r := range rows {
		if r != nil && r.DaemonFingerprint != "" {
			out = append(out, PairedDevice{Fingerprint: r.DaemonFingerprint, Name: r.Name})
		}
	}
	return out, nil
}

type devicePrefSvc struct {
	devices DeviceSource
	tx      TxRunner
}

// Option 定制 New 构造出的实例。
type Option func(*devicePrefSvc)

// WithDevices 注入设备来源（测试用）。
func WithDevices(d DeviceSource) Option { return func(s *devicePrefSvc) { s.devices = d } }

// WithTxRunner 注入事务（测试用）。
func WithTxRunner(r TxRunner) Option { return func(s *devicePrefSvc) { s.tx = r } }

// New 构造默认实现。
func New(opts ...Option) DevicePrefSvc {
	s := &devicePrefSvc{devices: remoteDeviceSource{}, tx: dbTxRunner{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

var defaultSvc DevicePrefSvc = New()

// Default 取默认服务单例。
func Default() DevicePrefSvc { return defaultSvc }

// known 参与派发的设备：本机在前，已配对的按配对时间在后。
func (s *devicePrefSvc) known(ctx context.Context) (self devicefp.Carrier, items []*DeviceItem, err error) {
	self, err = s.devices.Self()
	if err != nil {
		return "", nil, err
	}
	paired, err := s.devices.Paired(ctx)
	if err != nil {
		return "", nil, err
	}
	items = append(items, &DeviceItem{Fingerprint: self, IsSelf: true, Labels: []string{}})
	for _, p := range paired {
		if p.Fingerprint == self {
			continue
		}
		items = append(items, &DeviceItem{Fingerprint: p.Fingerprint, Name: p.Name, Labels: []string{}})
	}
	return self, items, nil
}

// storedOrder 已知设备按账号设备顺序排好（不含本机优先）：有偏好行的按位置，没有的
// 追加在后（保持 known 的顺序）。同时把标签带进条目。
func (s *devicePrefSvc) storedOrder(ctx context.Context) (self devicefp.Carrier, ordered []*DeviceItem, rows map[devicefp.Carrier]*device_pref_entity.DevicePref, err error) {
	self, all, err := s.known(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	prefs, err := device_pref_repo.DevicePref().List(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	rows = make(map[devicefp.Carrier]*device_pref_entity.DevicePref, len(prefs))
	for _, p := range prefs {
		rows[p.DeviceFingerprint] = p
	}
	byFP := make(map[devicefp.Carrier]*DeviceItem, len(all))
	for _, it := range all {
		byFP[it.Fingerprint] = it
		if p := rows[it.Fingerprint]; p != nil {
			it.Labels = p.Labels()
		}
	}
	for _, p := range prefs { // List 已按位置排好
		if it := byFP[p.DeviceFingerprint]; it != nil {
			ordered = append(ordered, it)
			delete(byFP, p.DeviceFingerprint)
		}
	}
	for _, it := range all {
		if _, left := byFP[it.Fingerprint]; left {
			ordered = append(ordered, it)
		}
	}
	return self, ordered, rows, nil
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func (s *devicePrefSvc) ListDevices(ctx context.Context) (*ListDevicesResponse, error) {
	_, ordered, _, err := s.storedOrder(ctx)
	if err != nil {
		return nil, err
	}
	localFirst, err := s.GetLocalFirst(ctx)
	if err != nil {
		return nil, err
	}
	if localFirst {
		if i := slices.IndexFunc(ordered, func(it *DeviceItem) bool { return it.IsSelf }); i > 0 {
			self := ordered[i]
			ordered = append([]*DeviceItem{self}, slices.Delete(slices.Clone(ordered), i, i+1)...)
		}
	}
	return &ListDevicesResponse{Items: ordered, LocalFirst: localFirst}, nil
}

func (s *devicePrefSvc) Reorder(ctx context.Context, order []devicefp.Carrier) error {
	seen := make(map[devicefp.Carrier]struct{}, len(order))
	for _, fp := range order {
		if strings.TrimSpace(string(fp)) == "" {
			return i18n.NewError(ctx, code.DevicePrefInvalidOrder)
		}
		if _, dup := seen[fp]; dup {
			return i18n.NewError(ctx, code.DevicePrefInvalidOrder)
		}
		seen[fp] = struct{}{}
	}
	_, current, rows, err := s.storedOrder(ctx)
	if err != nil {
		return err
	}
	isKnown := make(map[devicefp.Carrier]struct{}, len(current))
	for _, it := range current {
		isKnown[it.Fingerprint] = struct{}{}
	}
	final := make([]devicefp.Carrier, 0, len(current))
	for _, fp := range order {
		if _, ok := isKnown[fp]; !ok {
			return i18n.NewError(ctx, code.DevicePrefInvalidDevice)
		}
		final = append(final, fp)
	}
	for _, it := range current {
		if _, listed := seen[it.Fingerprint]; !listed {
			final = append(final, it.Fingerprint)
		}
	}
	// 同步通知等整笔事务提交之后再发：回滚的写入不该上行。只发位置真的变了的行与新建的行。
	var changes []sync_svc.LocalChange
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		changes = changes[:0]
		for i, fp := range final {
			ch, err := s.place(ctx, fp, i, rows[fp])
			if err != nil {
				return err
			}
			if ch != nil {
				changes = append(changes, *ch)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ch := range changes {
		sync_svc.Notify(ctx, ch)
	}
	return nil
}

// place 把一台设备放到 position：没有偏好行就建一行。返回要上行的改动（位置没变时为 nil）。
func (s *devicePrefSvc) place(ctx context.Context, fp devicefp.Carrier, position int, row *device_pref_entity.DevicePref) (*sync_svc.LocalChange, error) {
	if row == nil {
		p, err := s.createRow(ctx, fp, position, "")
		if err != nil {
			return nil, err
		}
		return &sync_svc.LocalChange{Kind: syncwire.KindDevicePref, LocalID: p.ID, Op: sync_svc.OpCreate, Meta: p.SyncMeta}, nil
	}
	if err := device_pref_repo.DevicePref().SetSortOrder(ctx, fp, position); err != nil {
		return nil, err
	}
	if row.SortOrder == position {
		return nil, nil
	}
	return &sync_svc.LocalChange{Kind: syncwire.KindDevicePref, LocalID: row.ID, Op: sync_svc.OpUpdate, Meta: row.SyncMeta}, nil
}

func (s *devicePrefSvc) createRow(ctx context.Context, fp devicefp.Carrier, position int, labelsJSON string) (*device_pref_entity.DevicePref, error) {
	p := &device_pref_entity.DevicePref{DeviceFingerprint: fp, SortOrder: position, Status: consts.ACTIVE, LabelsJSON: labelsJSON}
	if err := p.Check(ctx); err != nil {
		return nil, err
	}
	if err := device_pref_repo.DevicePref().Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *devicePrefSvc) UpdateLabels(ctx context.Context, fingerprint devicefp.Carrier, labels []string) error {
	_, ordered, rows, err := s.storedOrder(ctx)
	if err != nil {
		return err
	}
	position := slices.IndexFunc(ordered, func(it *DeviceItem) bool { return it.Fingerprint == fingerprint })
	if position < 0 {
		return i18n.NewError(ctx, code.DevicePrefInvalidDevice)
	}
	p := &device_pref_entity.DevicePref{}
	p.SetLabels(labels)
	if rows[fingerprint] == nil {
		created, err := s.createRow(ctx, fingerprint, position, p.LabelsJSON)
		if err != nil {
			return err
		}
		sync_svc.NotifyCreate(ctx, syncwire.KindDevicePref, created.ID, created.SyncMeta)
		return nil
	}
	if err := device_pref_repo.DevicePref().SetLabels(ctx, fingerprint, p.LabelsJSON); err != nil {
		return err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindDevicePref, rows[fingerprint].ID, rows[fingerprint].SyncMeta)
	return nil
}

func (s *devicePrefSvc) GetLocalFirst(ctx context.Context) (bool, error) {
	item, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyDeviceLocalFirst)
	if err != nil {
		return false, err
	}
	if item == nil {
		return true, nil
	}
	return strings.TrimSpace(item.Value) != "false", nil
}

func (s *devicePrefSvc) SetLocalFirst(ctx context.Context, on bool) error {
	value := "false"
	if on {
		value = "true"
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key: app_setting_entity.KeyDeviceLocalFirst, Value: value, Updatetime: nowMillis(),
	})
}

// resolveDevice 空 = 本机；非空必须是参与派发的设备。
func (s *devicePrefSvc) resolveDevice(ctx context.Context, device devicefp.Carrier) (devicefp.Carrier, error) {
	self, items, err := s.known(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(device)) == "" {
		return self, nil
	}
	if !slices.ContainsFunc(items, func(it *DeviceItem) bool { return it.Fingerprint == device }) {
		return "", i18n.NewError(ctx, code.DevicePrefInvalidDevice)
	}
	return device, nil
}

func (s *devicePrefSvc) GetCLIPath(ctx context.Context, runtimeType string, device devicefp.Carrier) (string, error) {
	fp, err := s.resolveDevice(ctx, device)
	if err != nil {
		return "", err
	}
	row, err := device_pref_repo.CLIPath().Find(ctx, strings.TrimSpace(runtimeType), fp)
	if err != nil || row == nil {
		return "", err
	}
	return row.CLIPath, nil
}

func (s *devicePrefSvc) SetCLIPath(ctx context.Context, runtimeType string, device devicefp.Carrier, path string) error {
	runtimeType = strings.TrimSpace(runtimeType)
	probe := &device_pref_entity.CLIPathOverride{RuntimeType: runtimeType, DeviceFingerprint: "x"}
	if err := probe.Check(ctx); err != nil {
		return err
	}
	fp, err := s.resolveDevice(ctx, device)
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	row, err := device_pref_repo.CLIPath().Find(ctx, runtimeType, fp)
	if err != nil {
		return err
	}
	switch {
	case row == nil && path == "":
		return nil
	case row == nil:
		created := &device_pref_entity.CLIPathOverride{
			RuntimeType: runtimeType, DeviceFingerprint: fp, CLIPath: path, Status: consts.ACTIVE,
		}
		if err := device_pref_repo.CLIPath().Create(ctx, created); err != nil {
			return err
		}
		sync_svc.NotifyCreate(ctx, syncwire.KindDeviceCLIPath, created.ID, created.SyncMeta)
		return nil
	case path == "":
		if err := device_pref_repo.CLIPath().Delete(ctx, row.ID); err != nil {
			return err
		}
		sync_svc.NotifyDelete(ctx, syncwire.KindDeviceCLIPath, row.ID, row.SyncMeta)
		return nil
	default:
		if err := device_pref_repo.CLIPath().UpdatePath(ctx, row.ID, path); err != nil {
			return err
		}
		sync_svc.NotifyUpdate(ctx, syncwire.KindDeviceCLIPath, row.ID, row.SyncMeta)
		return nil
	}
}
