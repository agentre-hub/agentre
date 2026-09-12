package ctlskill_svc

import (
	"context"
	"os"
	"path/filepath"
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

func TestInstallOnBoot_DegradesInstallerFailureToWarn(t *testing.T) {
	convey.Convey("安装器真的报错时，InstallOnBoot 降级为 warn 而非 panic 或向上传播", t, func() {
		ctx, repo, svc := setupSvcTest(t)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyCtlSkillDeclined).
			Return(&app_setting_entity.AppSetting{Key: app_setting_entity.KeyCtlSkillDeclined, Value: "false"}, nil)

		// 在通用技能目录的必经路径上预置一个同名普通文件，逼 os.MkdirAll 真报错——
		// 不是符号链接，不会被 installUniversal 自己的软链跳过闸拦住、提前吞掉错误。
		home := svc.mustHome(t)
		blocked := filepath.Join(home, ".agents", "skills")
		assert.NoError(t, os.MkdirAll(filepath.Dir(blocked), 0o755))
		assert.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o644))

		assert.NotPanics(t, func() {
			svc.InstallOnBoot(ctx)
		}, "安装器失败必须降级为 warn 日志，不能 panic 带崩启动")

		// 降级意味着调用正常返回、后续操作照常可用；不是进程被带崩，也不是错误被吞成
		// "看起来装成了"。
		info, err := svc.Status(ctx)
		assert.NoError(t, err, "InstallOnBoot 的失败不能向调用方传播")
		assert.False(t, info.UniversalInstalled, "被挡住的那一半确实没装成，证明失败是真的发生了")
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
