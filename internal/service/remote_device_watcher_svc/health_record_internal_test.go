package remote_device_watcher_svc

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// recordSpy 记下 watcher 在一次成功心跳后交给 recorder 的东西。
//
// 用包内测试而不是 _test 包:心跳循环里那次 health.ping 需要一条活的传输才跑得起来,
// 而这一层要验的只是「应答里的哪些字段被记了下来」—— 把应答到缓存的翻译单独拿出来验,
// 比为它撑起一条真连接更贴近它自己的失败模式。
type recordSpy struct {
	mu       sync.Mutex
	caps     []string
	version  string
	commit   string
	builds   int
	provider []ProviderSummary
}

func (s *recordSpy) RecordDeviceProviders(_ int64, ps []ProviderSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = ps
}

func (s *recordSpy) RecordDeviceCapabilities(_ int64, caps []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps = caps
}

func (s *recordSpy) RecordDeviceBuild(_ int64, version, commit string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version, s.commit = version, commit
	s.builds++
}

// 设备行显示的版本必须是远端此刻真在跑的那一个,来源只有 health.ping 的自报字段
// （决策 4）。翻译丢掉其中任何一个,界面就只能退回「不知道」——而短 commit 一旦丢掉,
// 本地构建会被当成发布构建劝升(决策 5)。
func TestRecordHealth_GivenSelfReportedBuild_WhenRecording_ThenBothFieldsReachTheCache(t *testing.T) {
	Convey("health.ping 的自报构建标识落进缓存", t, func() {
		Convey("发布构建:版本号与短 commit 原样记下", func() {
			spy := &recordSpy{}

			recordHealth(spy, 7, &agentrewire.HealthPingResponse{
				DaemonVersion: "0.5.2",
				DaemonCommit:  "a1b2c3d",
				Capabilities:  []string{"llm-model-target-v1"},
			})

			So(spy.version, ShouldEqual, "0.5.2")
			So(spy.commit, ShouldEqual, "a1b2c3d")
			So(spy.caps, ShouldResemble, []string{"llm-model-target-v1"})
		})

		Convey("非发布构建:短 commit 是空串,照样记 —— 它正是「开发构建」的判据", func() {
			spy := &recordSpy{}

			recordHealth(spy, 7, &agentrewire.HealthPingResponse{DaemonVersion: "1.0.0"})

			So(spy.builds, ShouldEqual, 1)
			So(spy.version, ShouldEqual, "1.0.0")
			So(spy.commit, ShouldEqual, "")
		})

		Convey("旧 daemon 不报这两个字段:记成空,界面据此什么都不说", func() {
			spy := &recordSpy{}

			recordHealth(spy, 7, &agentrewire.HealthPingResponse{})

			So(spy.builds, ShouldEqual, 1)
			So(spy.version, ShouldEqual, "")
		})

		Convey("没有 recorder(单测里不关心缓存)不炸", func() {
			So(func() { recordHealth(nil, 7, &agentrewire.HealthPingResponse{}) }, ShouldNotPanic)
		})
	})
}

// 心跳与重连是设备行 lastError 的常驻写者:refresh.go 把握手被拒落成
// protocol_mismatch,下一次心跳若把它盖成 "dial_failed:…",设备面板上那条
// 「版本太旧,连不上」的强提示活不过几秒 —— 用户看到的是一句泛泛的连接失败,
// 被指去查网络与端口,而真正要做的是升级那台机器上的 agentred。
func TestClassifyMessage_GivenAProtocolRejection_ThenKeepsItsOwnCode(t *testing.T) {
	Convey("协议层的拒绝不折进 dial_failed", t, func() {
		Convey("版本对不上", func() {
			err := errors.New("remote agentred speaks another agentre protocol version")

			So(classifyMessage(err), ShouldEqual, "protocol_mismatch")
		})

		Convey("压根不认识这套协议", func() {
			err := errors.New("remote agentred speaks no agentre protobuf protocol")

			So(classifyMessage(err), ShouldEqual, "protocol_unsupported")
		})

		Convey("包着一层上下文也认得出来", func() {
			err := fmt.Errorf("open ws://lab/rpc: %w",
				errors.New("remote agentred speaks another agentre protocol version"))

			So(classifyMessage(err), ShouldEqual, "protocol_mismatch")
		})

		Convey("真的网络失败仍旧是 dial_failed", func() {
			So(classifyMessage(errors.New("connection refused")), ShouldEqual, "dial_failed:connection refused")
		})
	})
}
