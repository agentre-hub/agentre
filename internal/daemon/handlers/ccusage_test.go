package handlers_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestCCUsageHandlers_Get(t *testing.T) {
	Convey("Get 在 fetch 成功时返回 reason='ok' + Data", t, func() {
		rl := &ccoauth.RateLimits{FiveHourPercent: 42, WeeklyPercent: 18}
		h := handlers.NewCCUsageHandlers(func(_ context.Context) (*ccoauth.RateLimits, error) {
			return rl, nil
		})
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "ok")
		So(res.Data, ShouldNotBeNil)
		So(res.Data.FiveHourPercent, ShouldEqual, 42)
	})

	Convey("Get 在 ErrNoCredentials 时返回 reason='no_credentials' + Data 为 nil", t, func() {
		h := handlers.NewCCUsageHandlers(func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrNoCredentials
		})
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "no_credentials")
		So(res.Data, ShouldBeNil)
	})

	Convey("Get 在 ErrAuthExpired 时返回 reason='auth_expired'", t, func() {
		h := handlers.NewCCUsageHandlers(func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrAuthExpired
		})
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "auth_expired")
	})

	Convey("Get 在 ErrRateLimited 时返回 reason='rate_limited'", t, func() {
		h := handlers.NewCCUsageHandlers(func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, ccoauth.ErrRateLimited
		})
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "rate_limited")
	})

	Convey("Get 在 ErrNetwork 或未知错误时返回 reason='network'", t, func() {
		h := handlers.NewCCUsageHandlers(func(_ context.Context) (*ccoauth.RateLimits, error) {
			return nil, errors.New("dial tcp: nope")
		})
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "network")
	})

	Convey("Get 在 fetch fn 为 nil 时返回 reason='no_credentials'(防御性)", t, func() {
		h := handlers.NewCCUsageHandlers(nil)
		res, err := h.Get(context.Background())
		So(err, ShouldBeNil)
		So(res.Reason, ShouldEqual, "no_credentials")
	})
}
