// internal/service/remote_device_svc/update_tls_test.go
package remote_device_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

func TestUpdateTLS(t *testing.T) {
	Convey("not found returns error", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(9)).Return(nil, nil)
		_, err := svc.UpdateTLS(context.Background(), 9, "default", "")
		So(err, ShouldNotBeNil)
	})
	Convey("invalid combo (default + PEM) returns TLSConfigInvalid", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		_, err := svc.UpdateTLS(context.Background(), 1, "default", "GARBAGE")
		So(err, ShouldNotBeNil)
	})
	Convey("happy path writes and calls Refresh", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		repo.EXPECT().UpdateTLS(gomock.Any(), int64(1), "pin-cert", "PEM").Return(nil)
		w.EXPECT().Restart(gomock.Any(), int64(1)).Return(nil)
		// Refresh path:
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).Return(remote_device_svc.ConnectResult{}, nil)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), gomock.Any(), "").Return(nil)
		got, err := svc.UpdateTLS(context.Background(), 1, "pin-cert", "PEM")
		So(err, ShouldBeNil)
		So(got, ShouldNotBeNil)
	})
}
