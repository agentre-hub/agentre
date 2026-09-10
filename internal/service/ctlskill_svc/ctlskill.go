// Package ctlskill_svc wires the leaf ctl-skill installer (internal/pkg/ctlskill) to
// app lifecycle and app_settings: it owns the persisted "user declined" flag and the
// two startup skip gates, and exposes Status/Install/Uninstall for the Wails bindings.
package ctlskill_svc

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/pkg/ctlskill"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// CtlSkillSvc 管理 ctl 控制通道技能包（internal/pkg/ctlskill）的安装生命周期：启动期按
// 两道闸做一次幂等安装，设置页可手动安装/卸载；卸载时落一枚“用户已拒绝”持久标记，
// 阻止下次启动自动复装，安装则清除它。
type CtlSkillSvc interface {
	// Status 报出两种形态各自的安装态与路径。
	Status(ctx context.Context) (*ctlskill.Info, error)
	// Install 清除拒绝标记并执行一次安装（幂等；已是当前版本则内部 no-op）。
	Install(ctx context.Context) (*ctlskill.Info, error)
	// Uninstall 卸载两种形态、写下拒绝标记，下次启动不会重装。
	Uninstall(ctx context.Context) (*ctlskill.Info, error)
	// InstallOnBoot 启动期安装：拒绝标记为真或 AGENTRE_ENV=test 时跳过；失败降级为 warn
	// 日志，不向调用方传播错误、不阻断启动。
	InstallOnBoot(ctx context.Context)
}

type ctlSkillSvc struct {
	// agrctlPath / version 由 bootstrap 在算出本机 agrctl 路径与应用版本后经 Register 注入。
	agrctlPath string
	version    string
	// homeDir 可注入的 home 解析口子，测试用临时目录替换，默认 os.UserHomeDir。
	homeDir func() (string, error)
}

var defaultSvc CtlSkillSvc = &ctlSkillSvc{homeDir: os.UserHomeDir}

// CtlSkill 取默认服务单例。
func CtlSkill() CtlSkillSvc { return defaultSvc }

// Register 注入本机 agrctl 绝对路径与当前应用版本，由 bootstrap 在算出这两个值之后调用一次
// （与 agrctlinstall 安装 hook shim 同一处，见 internal/bootstrap/cago.go）。
func Register(agrctlPath, version string) {
	if s, ok := defaultSvc.(*ctlSkillSvc); ok {
		s.agrctlPath = agrctlPath
		s.version = version
	}
}

func (s *ctlSkillSvc) Status(ctx context.Context) (*ctlskill.Info, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	info := ctlskill.Status(home)
	return &info, nil
}

func (s *ctlSkillSvc) Install(ctx context.Context) (*ctlskill.Info, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	if err := s.setDeclined(ctx, false); err != nil {
		return nil, err
	}
	if err := ctlskill.Install(ctlskill.Options{Home: home, AgrctlPath: s.agrctlPath, Version: s.version}); err != nil {
		return nil, err
	}
	info := ctlskill.Status(home)
	return &info, nil
}

func (s *ctlSkillSvc) Uninstall(ctx context.Context) (*ctlskill.Info, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	if err := ctlskill.Uninstall(home); err != nil {
		return nil, err
	}
	if err := s.setDeclined(ctx, true); err != nil {
		return nil, err
	}
	info := ctlskill.Status(home)
	return &info, nil
}

func (s *ctlSkillSvc) InstallOnBoot(ctx context.Context) {
	declined, err := s.declined(ctx)
	if err != nil {
		// 读不到拒绝标记本身就是降级信号：warn 并按未拒绝处理，继续走下面的安装路径，
		// 与 bootstrap/debug_logging.go 的 applyDebugLoggingOnBoot 同一降级口径。
		logger.Default().Warn("ctlskill_svc.InstallOnBoot: read declined flag", zap.Error(err))
	}
	if declined {
		logger.Default().Info("ctlskill_svc.InstallOnBoot: skip, user declined")
		return
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTRE_ENV")), "test") {
		logger.Default().Info("ctlskill_svc.InstallOnBoot: skip, AGENTRE_ENV=test")
		return
	}
	home, err := s.home()
	if err != nil {
		logger.Default().Warn("ctlskill_svc.InstallOnBoot: resolve home", zap.Error(err))
		return
	}
	if err := ctlskill.Install(ctlskill.Options{Home: home, AgrctlPath: s.agrctlPath, Version: s.version}); err != nil {
		logger.Default().Warn("ctlskill_svc.InstallOnBoot: install", zap.Error(err))
	}
}

func (s *ctlSkillSvc) home() (string, error) {
	if s.homeDir != nil {
		return s.homeDir()
	}
	return os.UserHomeDir()
}

func (s *ctlSkillSvc) declined(ctx context.Context) (bool, error) {
	got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyCtlSkillDeclined)
	if err != nil {
		return false, err
	}
	if got == nil {
		return false, nil
	}
	return app_setting_entity.ParseBoolSetting(got.Value), nil
}

func (s *ctlSkillSvc) setDeclined(ctx context.Context, declined bool) error {
	val := "false"
	if declined {
		val = "true"
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        app_setting_entity.KeyCtlSkillDeclined,
		Value:      val,
		Updatetime: time.Now().UnixMilli(),
	})
}
