package ctlskill_svc

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/pkg/ctlskill"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
)

// setupSvcTest 注入 mock 仓储 + 临时 home，避免碰真实用户目录或数据库。
func setupSvcTest(t *testing.T) (context.Context, *mock_app_setting_repo.MockAppSettingRepo, *ctlSkillSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	repo := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	app_setting_repo.RegisterAppSetting(repo)

	home := t.TempDir()
	svc := &ctlSkillSvc{
		agrctlPath: "/tmp/agentre-data/bin/agrctl",
		version:    "test-version",
		homeDir:    func() (string, error) { return home, nil },
	}
	return context.Background(), repo, svc
}

func TestInstallOnBoot_SkipsWhenDeclined(t *testing.T) {
	convey.Convey("拒绝标记为真时启动安装被跳过", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyCtlSkillDeclined).
			Return(&app_setting_entity.AppSetting{Key: app_setting_entity.KeyCtlSkillDeclined, Value: "true"}, nil)

		svc.InstallOnBoot(ctx)

		info := ctlskill.Status(svc.mustHome(t))
		assert.False(t, info.PluginInstalled, "拒绝标记为真时不应安装")
		assert.False(t, info.UniversalInstalled)
	})
}

func TestInstallOnBoot_SkipsWhenTestEnv(t *testing.T) {
	convey.Convey("AGENTRE_ENV=test 时启动安装被跳过", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		t.Setenv("AGENTRE_ENV", "test")
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyCtlSkillDeclined).
			Return(nil, nil)

		svc.InstallOnBoot(ctx)

		info := ctlskill.Status(svc.mustHome(t))
		assert.False(t, info.PluginInstalled, "AGENTRE_ENV=test 时不应安装")
		assert.False(t, info.UniversalInstalled)
	})
}

func TestInstallOnBoot_InstallsWhenNeitherGateHits(t *testing.T) {
	convey.Convey("拒绝标记与 AGENTRE_ENV=test 都不成立时执行安装", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyCtlSkillDeclined).
			Return(&app_setting_entity.AppSetting{Key: app_setting_entity.KeyCtlSkillDeclined, Value: "false"}, nil)

		svc.InstallOnBoot(ctx)

		info := ctlskill.Status(svc.mustHome(t))
		assert.True(t, info.PluginInstalled)
		assert.True(t, info.UniversalInstalled)
	})
}

func TestInstall_ClearsDeclinedFlagAndInstalls(t *testing.T) {
	convey.Convey("Install 清除拒绝标记并执行一次安装", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		repo.EXPECT().Set(gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(*app_setting_entity.AppSetting)
			return ok && s.Key == app_setting_entity.KeyCtlSkillDeclined && s.Value == "false"
		})).Return(nil)

		info, err := svc.Install(ctx)
		assert.NoError(t, err)
		assert.True(t, info.PluginInstalled)
		assert.True(t, info.UniversalInstalled)
	})
}

func TestUninstall_WritesDeclinedFlagAndRemovesFiles(t *testing.T) {
	convey.Convey("Uninstall 删除两处安装并写下拒绝标记", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		// 先装一遍，方便验证卸载确实清掉了文件。
		home := svc.mustHome(t)
		assert.NoError(t, ctlskill.Install(ctlskill.Options{Home: home, AgrctlPath: svc.agrctlPath, Version: svc.version}))

		repo.EXPECT().Set(gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(*app_setting_entity.AppSetting)
			return ok && s.Key == app_setting_entity.KeyCtlSkillDeclined && s.Value == "true"
		})).Return(nil)

		info, err := svc.Uninstall(ctx)
		assert.NoError(t, err)
		assert.False(t, info.PluginInstalled)
		assert.False(t, info.UniversalInstalled)
	})
}

func TestStatus_ReportsCurrentInstallState(t *testing.T) {
	convey.Convey("Status 直接反映磁盘上的安装态", t, func() {
		ctx, _, svc := setupSvcTest(t)

		info, err := svc.Status(ctx)
		assert.NoError(t, err)
		assert.False(t, info.PluginInstalled)
		assert.False(t, info.UniversalInstalled)
	})
}

// mustHome 是测试专用小助手，直接暴露注入的 home，避免每个用例都重复类型断言。
func (s *ctlSkillSvc) mustHome(t *testing.T) string {
	t.Helper()
	home, err := s.homeDir()
	assert.NoError(t, err)
	return home
}
