package device_pref_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/device_pref_svc"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	self   devicefp.Carrier = "sha256:self"
	boxA   devicefp.Carrier = "sha256:boxa"
	boxB   devicefp.Carrier = "sha256:boxb"
	nobody devicefp.Carrier = "sha256:nobody"
)

type fakeDevices struct{ paired []devicefp.Carrier }

func (f fakeDevices) Self() (devicefp.Carrier, error) { return self, nil }
func (f fakeDevices) Paired(context.Context) ([]device_pref_svc.PairedDevice, error) {
	out := make([]device_pref_svc.PairedDevice, 0, len(f.paired))
	for _, fp := range f.paired {
		out = append(out, device_pref_svc.PairedDevice{Fingerprint: fp, Name: "name-" + string(fp)})
	}
	return out, nil
}

type directTx struct{}

func (directTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type harness struct {
	ctx      context.Context
	prefs    *mock_device_pref_repo.MockDevicePrefRepo
	cli      *mock_device_pref_repo.MockCLIPathRepo
	settings *mock_app_setting_repo.MockAppSettingRepo
	svc      device_pref_svc.DevicePrefSvc
}

func setup(t *testing.T, paired ...devicefp.Carrier) *harness {
	t.Helper()
	ctrl := gomock.NewController(t)
	h := &harness{
		ctx:      context.Background(),
		prefs:    mock_device_pref_repo.NewMockDevicePrefRepo(ctrl),
		cli:      mock_device_pref_repo.NewMockCLIPathRepo(ctrl),
		settings: mock_app_setting_repo.NewMockAppSettingRepo(ctrl),
	}
	device_pref_repo.RegisterDevicePref(h.prefs)
	device_pref_repo.RegisterCLIPath(h.cli)
	app_setting_repo.RegisterAppSetting(h.settings)
	h.svc = device_pref_svc.New(device_pref_svc.WithDevices(fakeDevices{paired: paired}), device_pref_svc.WithTxRunner(directTx{}))
	return h
}

func pref(fp devicefp.Carrier, order int, labels string) *device_pref_entity.DevicePref {
	return &device_pref_entity.DevicePref{ID: int64(order + 1), DeviceFingerprint: fp, SortOrder: order, LabelsJSON: labels, Status: consts.ACTIVE}
}

func fingerprints(items []*device_pref_svc.DeviceItem) []devicefp.Carrier {
	out := make([]devicefp.Carrier, 0, len(items))
	for _, it := range items {
		out = append(out, it.Fingerprint)
	}
	return out
}

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func TestListDevices(t *testing.T) {
	convey.Convey("ListDevices", t, func() {
		convey.Convey("Given no stored prefs When listed Then this machine leads and paired devices follow in pairing order", func() {
			h := setup(t, boxA, boxB)
			h.prefs.EXPECT().List(h.ctx).Return(nil, nil)
			h.settings.EXPECT().Get(h.ctx, app_setting_entity.KeyDeviceLocalFirst).Return(nil, nil)
			res, err := h.svc.ListDevices(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, []devicefp.Carrier{self, boxA, boxB}, fingerprints(res.Items))
			assert.True(t, res.LocalFirst)
			assert.True(t, res.Items[0].IsSelf)
		})

		convey.Convey("Given a stored order and local-first off When listed Then the stored order wins and a device without a row goes last", func() {
			h := setup(t, boxA, boxB)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{
				pref(boxB, 0, `["gpu"]`), pref(self, 1, `[]`),
			}, nil)
			h.settings.EXPECT().Get(h.ctx, app_setting_entity.KeyDeviceLocalFirst).
				Return(&app_setting_entity.AppSetting{Key: app_setting_entity.KeyDeviceLocalFirst, Value: "false"}, nil)
			res, err := h.svc.ListDevices(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, []devicefp.Carrier{boxB, self, boxA}, fingerprints(res.Items))
			assert.False(t, res.LocalFirst)
			assert.Equal(t, []string{"gpu"}, res.Items[0].Labels)
			assert.Equal(t, "name-"+string(boxB), res.Items[0].Name)
		})

		convey.Convey("Given the same stored order and local-first on When listed Then this machine is pinned first", func() {
			h := setup(t, boxA, boxB)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(boxB, 0, `[]`), pref(self, 1, `[]`)}, nil)
			h.settings.EXPECT().Get(h.ctx, app_setting_entity.KeyDeviceLocalFirst).Return(nil, nil)
			res, err := h.svc.ListDevices(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, []devicefp.Carrier{self, boxB, boxA}, fingerprints(res.Items))
		})

		convey.Convey("Given a stored row for an unpaired device When listed Then it is not shown", func() {
			h := setup(t)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(nobody, 0, `[]`), pref(self, 1, `[]`)}, nil)
			h.settings.EXPECT().Get(h.ctx, gomock.Any()).Return(nil, nil)
			res, err := h.svc.ListDevices(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, []devicefp.Carrier{self}, fingerprints(res.Items))
		})
	})
}

func TestReorder(t *testing.T) {
	convey.Convey("Reorder", t, func() {
		convey.Convey("Given a full order When reordered Then each device gets its index, creating missing rows", func() {
			h := setup(t, boxA, boxB)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(self, 0, `[]`)}, nil)
			h.prefs.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *device_pref_entity.DevicePref) error {
				assert.Equal(t, boxB, p.DeviceFingerprint)
				assert.Equal(t, 0, p.SortOrder)
				return nil
			})
			h.prefs.EXPECT().SetSortOrder(h.ctx, self, 1).Return(nil)
			h.prefs.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *device_pref_entity.DevicePref) error {
				assert.Equal(t, boxA, p.DeviceFingerprint)
				assert.Equal(t, 2, p.SortOrder)
				return nil
			})
			require.NoError(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxB, self, boxA}))
		})

		convey.Convey("Given a partial order When reordered Then the listed devices lead and the rest keep their relative order", func() {
			h := setup(t, boxA, boxB)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(self, 0, `[]`), pref(boxA, 1, `[]`), pref(boxB, 2, `[]`)}, nil)
			var got []devicefp.Carrier
			h.prefs.EXPECT().SetSortOrder(h.ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, fp devicefp.Carrier, _ int) error {
				got = append(got, fp)
				return nil
			}).Times(3)
			require.NoError(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxB}))
			assert.Equal(t, []devicefp.Carrier{boxB, self, boxA}, got)
		})

		convey.Convey("Given a duplicate or empty entry When reordered Then it is refused and nothing is written", func() {
			h := setup(t, boxA)
			requireCode(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxA, boxA}), code.DevicePrefInvalidOrder)
			requireCode(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{""}), code.DevicePrefInvalidOrder)
		})

		convey.Convey("Given a device that is not paired When reordered Then it is refused", func() {
			h := setup(t, boxA)
			h.prefs.EXPECT().List(h.ctx).Return(nil, nil).AnyTimes()
			requireCode(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{nobody}), code.DevicePrefInvalidDevice)
		})

		convey.Convey("Given a write fails When reordered Then the error is returned", func() {
			h := setup(t, boxA)
			h.prefs.EXPECT().List(h.ctx).Return(nil, nil)
			h.prefs.EXPECT().Create(h.ctx, gomock.Any()).Return(errors.New("boom"))
			assert.EqualError(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxA}), "boom")
		})
	})
}

func TestUpdateLabels(t *testing.T) {
	convey.Convey("UpdateLabels", t, func() {
		convey.Convey("Given a device with a row When labels are set Then they are normalized and written", func() {
			h := setup(t, boxA)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(self, 0, `[]`), pref(boxA, 1, `[]`)}, nil)
			h.prefs.EXPECT().SetLabels(h.ctx, boxA, `["gpu","mac"]`).Return(nil)
			require.NoError(t, h.svc.UpdateLabels(h.ctx, boxA, []string{" gpu", "mac", "gpu"}))
		})

		convey.Convey("Given a device without a row When labels are set Then a row is created at its current position", func() {
			h := setup(t, boxA)
			h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{pref(self, 0, `[]`)}, nil)
			h.prefs.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *device_pref_entity.DevicePref) error {
				assert.Equal(t, 1, p.SortOrder)
				assert.JSONEq(t, `["x"]`, p.LabelsJSON)
				return nil
			})
			require.NoError(t, h.svc.UpdateLabels(h.ctx, boxA, []string{"x"}))
		})

		convey.Convey("Given an unknown device When labels are set Then it is refused", func() {
			h := setup(t)
			h.prefs.EXPECT().List(h.ctx).Return(nil, nil)
			requireCode(t, h.svc.UpdateLabels(h.ctx, nobody, []string{"x"}), code.DevicePrefInvalidDevice)
		})
	})
}

func TestLocalFirst(t *testing.T) {
	convey.Convey("local-first switch", t, func() {
		convey.Convey("Given nothing stored When read Then it is on", func() {
			h := setup(t)
			h.settings.EXPECT().Get(h.ctx, app_setting_entity.KeyDeviceLocalFirst).Return(nil, nil)
			on, err := h.svc.GetLocalFirst(h.ctx)
			require.NoError(t, err)
			assert.True(t, on)
		})
		convey.Convey("When switched off Then the setting row is written as false", func() {
			h := setup(t)
			h.settings.EXPECT().Set(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, s *app_setting_entity.AppSetting) error {
				assert.Equal(t, app_setting_entity.KeyDeviceLocalFirst, s.Key)
				assert.Equal(t, "false", s.Value)
				return nil
			})
			require.NoError(t, h.svc.SetLocalFirst(h.ctx, false))
		})
	})
}

func TestCLIPath(t *testing.T) {
	convey.Convey("per-runtime CLI path", t, func() {
		convey.Convey("Given no override When read Then it is empty (auto-detect)", func() {
			h := setup(t)
			h.cli.EXPECT().Find(h.ctx, "claudecode", self).Return(nil, nil)
			got, err := h.svc.GetCLIPath(h.ctx, "claudecode", "")
			require.NoError(t, err)
			assert.Equal(t, "", got)
		})

		convey.Convey("Given no row When a path is set for this machine Then a row keyed by (runtime, self) is created", func() {
			h := setup(t)
			h.cli.EXPECT().Find(h.ctx, "codex", self).Return(nil, nil)
			h.cli.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, o *device_pref_entity.CLIPathOverride) error {
				assert.Equal(t, "codex", o.RuntimeType)
				assert.Equal(t, self, o.DeviceFingerprint)
				assert.Equal(t, "/opt/codex", o.CLIPath)
				return nil
			})
			require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", "", " /opt/codex "))
		})

		convey.Convey("Given a row When a new path is set for a paired device Then it is updated in place", func() {
			h := setup(t, boxA)
			h.cli.EXPECT().Find(h.ctx, "codex", boxA).Return(&device_pref_entity.CLIPathOverride{ID: 7, CLIPath: "/old"}, nil)
			h.cli.EXPECT().UpdatePath(h.ctx, int64(7), "/new").Return(nil)
			require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", boxA, "/new"))
		})

		convey.Convey("Given a row When the path is cleared Then the row is removed so detection takes over", func() {
			h := setup(t, boxA)
			h.cli.EXPECT().Find(h.ctx, "codex", boxA).Return(&device_pref_entity.CLIPathOverride{ID: 7, CLIPath: "/old"}, nil)
			h.cli.EXPECT().Delete(h.ctx, int64(7)).Return(nil)
			require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", boxA, "  "))
		})

		convey.Convey("Given no row When the path is cleared Then nothing is written", func() {
			h := setup(t, boxA)
			h.cli.EXPECT().Find(h.ctx, "codex", boxA).Return(nil, nil)
			require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", boxA, ""))
		})

		convey.Convey("Given a runtime with no CLI When a path is set Then it is refused", func() {
			h := setup(t)
			requireCode(t, h.svc.SetCLIPath(h.ctx, "hermes", "", "/x"), code.DeviceCLIRuntimeInvalid)
		})

		convey.Convey("Given an unpaired device When a path is set Then it is refused", func() {
			h := setup(t)
			requireCode(t, h.svc.SetCLIPath(h.ctx, "codex", nobody, "/x"), code.DevicePrefInvalidDevice)
		})
	})
}
