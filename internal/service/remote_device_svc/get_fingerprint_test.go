package remote_device_svc_test

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// TestDeviceFingerprint (R17 本机侧):DeviceFingerprint 交出与 LAN 配对 / 账号登录
// 共用的本机设备指纹(agentre-device-fingerprint keychain 账号)。前端拿它与消息上的
// sourceDevice 比对,相等就不渲染来源标识(本机不带)。
func TestDeviceFingerprint(t *testing.T) {
	Convey("returns the persisted device fingerprint", t, func() {
		_, _, kc, _, svc := setupSvc(t)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("sha256:existing", nil)
		fp, err := svc.DeviceFingerprint()
		So(err, ShouldBeNil)
		So(fp, ShouldEqual, "sha256:existing")
	})
	Convey("generates and persists a fresh fingerprint when absent", t, func() {
		_, _, kc, _, svc := setupSvc(t)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("", keychain.ErrNotFound)
		kc.EXPECT().Set("agentre-device-fingerprint", gomock.Any()).Return(nil)
		fp, err := svc.DeviceFingerprint()
		So(err, ShouldBeNil)
		So(fp, ShouldNotBeEmpty)
		So(strings.HasPrefix(fp, "sha256:"), ShouldBeTrue)
	})
}
