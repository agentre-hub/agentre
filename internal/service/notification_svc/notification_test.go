package notification_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/service/notification_svc"
	"github.com/agentre-hub/agentre/internal/service/notification_svc/mock_notification_svc"
)

func TestShow(t *testing.T) {
	convey.Convey("Show", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		n := mock_notification_svc.NewMockNotifier(ctrl)
		notification_svc.RegisterNotifier(n)
		t.Cleanup(func() { notification_svc.RegisterNotifier(nil) })

		convey.Convey("透传 title/body/sessionID", func() {
			n.EXPECT().Notify(gomock.Any(), "fix bug", "已完成", int64(42)).Return(nil)
			assert.NoError(t, notification_svc.Notification().Show(
				context.Background(),
				&notification_svc.ShowRequest{Title: "fix bug", Body: "已完成", SessionID: 42}))
		})

		convey.Convey("空 title 兜底 Agentre", func() {
			n.EXPECT().Notify(gomock.Any(), "Agentre", "x", int64(0)).Return(nil)
			assert.NoError(t, notification_svc.Notification().Show(
				context.Background(), &notification_svc.ShowRequest{Title: "  ", Body: "x"}))
		})

		convey.Convey("Notifier 报错向上传播", func() {
			n.EXPECT().Notify(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("boom"))
			assert.Error(t, notification_svc.Notification().Show(
				context.Background(), &notification_svc.ShowRequest{Title: "t", Body: "b"}))
		})
	})

	convey.Convey("未注入 notifier 时 no-op", t, func() {
		notification_svc.RegisterNotifier(nil)
		assert.NoError(t, notification_svc.Notification().Show(
			context.Background(), &notification_svc.ShowRequest{Title: "t"}))
	})
}
