package update_svc

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
)

// setupSettingsTest 注入 mock_app_setting_repo，所有 settings.go 里的 Get/Set 都走 mock。
func setupSettingsTest(t *testing.T) (context.Context, *mock_app_setting_repo.MockAppSettingRepo) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	repo := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	app_setting_repo.RegisterAppSetting(repo)

	return context.Background(), repo
}

func TestGetMirror(t *testing.T) {
	convey.Convey("GetMirror", t, func() {
		ctx, repo := setupSettingsTest(t)

		convey.Convey("未设置返回空串（直连）", func() {
			repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyDownloadMirror).Return(nil, nil)
			got, err := getMirror(ctx)
			assert.NoError(t, err)
			assert.Equal(t, "", got)
		})

		convey.Convey("已设置返回 trim 后值", func() {
			repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyDownloadMirror).
				Return(&app_setting_entity.AppSetting{Value: "https://ghfast.top/"}, nil)
			got, err := getMirror(ctx)
			assert.NoError(t, err)
			assert.Equal(t, "https://ghfast.top/", got)
		})
	})
}

func TestSetMirror(t *testing.T) {
	convey.Convey("SetMirror", t, func() {
		ctx, repo := setupSettingsTest(t)

		convey.Convey("写入镜像 URL", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.AssignableToTypeOf(&app_setting_entity.AppSetting{})).
				DoAndReturn(func(_ context.Context, s *app_setting_entity.AppSetting) error {
					assert.Equal(t, app_setting_entity.KeyDownloadMirror, s.Key)
					assert.Equal(t, "https://ghfast.top/", s.Value)
					return nil
				})
			assert.NoError(t, setMirror(ctx, "https://ghfast.top/"))
		})

		convey.Convey("空串清空", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.AssignableToTypeOf(&app_setting_entity.AppSetting{})).
				DoAndReturn(func(_ context.Context, s *app_setting_entity.AppSetting) error {
					assert.Equal(t, "", s.Value)
					return nil
				})
			assert.NoError(t, setMirror(ctx, "   "))
		})
	})
}

func TestGetSetLastUpdateCheck(t *testing.T) {
	convey.Convey("LastUpdateCheck 时间戳读写", t, func() {
		ctx, repo := setupSettingsTest(t)

		convey.Convey("未设置返回 0", func() {
			repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyLastUpdateCheck).Return(nil, nil)
			ts, err := getLastUpdateCheck(ctx)
			assert.NoError(t, err)
			assert.Equal(t, int64(0), ts)
		})

		convey.Convey("非数字值容错返回 0", func() {
			repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyLastUpdateCheck).
				Return(&app_setting_entity.AppSetting{Value: "not-a-number"}, nil)
			ts, err := getLastUpdateCheck(ctx)
			assert.NoError(t, err)
			assert.Equal(t, int64(0), ts)
		})

		convey.Convey("正常时间戳解析", func() {
			repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyLastUpdateCheck).
				Return(&app_setting_entity.AppSetting{Value: "1700000000"}, nil)
			ts, err := getLastUpdateCheck(ctx)
			assert.NoError(t, err)
			assert.Equal(t, int64(1700000000), ts)
		})

		convey.Convey("Set 写入字符串形式时间戳", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.AssignableToTypeOf(&app_setting_entity.AppSetting{})).
				DoAndReturn(func(_ context.Context, s *app_setting_entity.AppSetting) error {
					assert.Equal(t, app_setting_entity.KeyLastUpdateCheck, s.Key)
					assert.Equal(t, "1700000000", s.Value)
					return nil
				})
			assert.NoError(t, setLastUpdateCheck(ctx, 1700000000))
		})
	})
}
