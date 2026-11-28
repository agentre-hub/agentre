package devicefacts_test

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func TestConnectionLog(t *testing.T) {
	Convey("ConnectionLog 记这台设备最近一次连服务型端点的结果", t, func() {
		now := time.UnixMilli(1700000000000)
		log := devicefacts.NewConnectionLog(func() time.Time { return now })

		Convey("Given 从没连过, Then 是 never、没有时刻", func() {
			status, at := log.Last(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://h:8642"})
			So(status, ShouldEqual, syncwire.ConnectionNever)
			So(at, ShouldEqual, 0)
		})
		Convey("Given 连过一次失败, Then 报 failed 与那次的时刻;地址写法不同但规范化后相同也认得", func() {
			log.Record("hermes", "http://h:8642/", false)
			status, at := log.Last(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://h:8642"})
			So(status, ShouldEqual, syncwire.ConnectionFailed)
			So(at, ShouldEqual, now.UnixMilli())
			Convey("When 之后又连成功, Then 以最近一次为准", func() {
				now = now.Add(time.Minute)
				log.Record("hermes", "http://h:8642", true)
				status, at := log.Last(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://h:8642"})
				So(status, ShouldEqual, syncwire.ConnectionSucceeded)
				So(at, ShouldEqual, now.UnixMilli())
			})
		})
		Convey("Given OpenClaw 网关连成功, Then 按网关地址记,不与同地址的 Hermes 混", func() {
			log.Record("openclaw", "wss://gw.local:18789", true)
			log.Record("hermes", "https://other.local:8642", false)
			status, _ := log.Last(devicefacts.Engine{Runtime: "openclaw", OpenClawGatewayURL: "wss://gw.local:18789"})
			So(status, ShouldEqual, syncwire.ConnectionSucceeded)
			status, _ = log.Last(devicefacts.Engine{Runtime: "hermes", HermesURL: "https://gw.local:18789"})
			So(status, ShouldEqual, syncwire.ConnectionNever)
		})
		Convey("Given 地址不合法, Then 不记,也查不到", func() {
			log.Record("hermes", "::bad", false)
			status, _ := log.Last(devicefacts.Engine{Runtime: "hermes", HermesURL: "::bad"})
			So(status, ShouldEqual, syncwire.ConnectionNever)
		})
	})
}
