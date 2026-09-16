package handlers_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestCCUsage(t *testing.T) {
	Convey("CCUsage 在 fetch 成功时返回 reason='ok' + Data", t, func() {
		rl := &ccoauth.RateLimits{FiveHourPercent: 42, WeeklyPercent: 18}
		res := handlers.CCUsage(context.Background(), func(_ context.Context) (*ccoauth.RateLimits, error) {
			return rl, nil
		})
		So(res.Reason, ShouldEqual, "ok")
		So(res.Data, ShouldNotBeNil)
		So(res.Data.FiveHourPercent, ShouldEqual, 42)
	})

	Convey("CCUsage 在 ErrNoCredentials 时返回 reason='no_credentials' + Data 为 nil", t, func() {
		res := handlers.CCUsage(context.Background(), func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrNoCredentials
		})
		So(res.Reason, ShouldEqual, "no_credentials")
		So(res.Data, ShouldBeNil)
	})

	Convey("CCUsage 在 ErrAuthExpired 时返回 reason='auth_expired'", t, func() {
		res := handlers.CCUsage(context.Background(), func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrAuthExpired
		})
		So(res.Reason, ShouldEqual, "auth_expired")
	})

	Convey("CCUsage 在 ErrRateLimited 时返回 reason='rate_limited'", t, func() {
		res := handlers.CCUsage(context.Background(), func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrRateLimited
		})
		So(res.Reason, ShouldEqual, "rate_limited")
	})

	Convey("CCUsage 在 ErrNetwork 或未知错误时返回 reason='network'", t, func() {
		res := handlers.CCUsage(context.Background(), func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, errors.New("dial tcp: nope")
		})
		So(res.Reason, ShouldEqual, "network")
	})

	Convey("CCUsage 在 fetch fn 为 nil 时返回 reason='no_credentials'(防御性)", t, func() {
		res := handlers.CCUsage(context.Background(), nil)
		So(res.Reason, ShouldEqual, "no_credentials")
	})
}
