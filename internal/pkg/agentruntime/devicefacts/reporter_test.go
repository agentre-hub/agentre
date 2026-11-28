package devicefacts_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// scriptedFacts 每次采集交出当前的 version(测试改它来模拟「本机探测结果变了」),
// 采集时刻逐次递增 —— 时刻变了而内容没变,不该被当成变化。
type scriptedFacts struct {
	mu       sync.Mutex
	version  string
	calls    int
	err      error
	uploads  []syncwire.DeviceFactsReport
	uploadOK bool
}

func (s *scriptedFacts) collect(context.Context) (syncwire.DeviceFactsReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return syncwire.DeviceFactsReport{}, s.err
	}
	return syncwire.DeviceFactsReport{
		CollectedAt: int64(1000 + s.calls),
		Runtimes:    []syncwire.RuntimeFact{{Runtime: "claudecode", Found: true, Version: s.version}},
	}, nil
}

func (s *scriptedFacts) upload(_ context.Context, report *syncwire.DeviceFactsReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, *report)
	if !s.uploadOK {
		return errors.New("404 page not found")
	}
	return nil
}

func TestReporter(t *testing.T) {
	Convey("Reporter 决定什么时候把设备情况交给 server", t, func() {
		ctx := context.Background()
		facts := &scriptedFacts{version: "2.1.0", uploadOK: true}
		r := devicefacts.NewReporter(facts.collect, facts.upload)

		Convey("Given 连上账号中继, When Connected, Then 重新探测并整份上报", func() {
			So(r.Connected(ctx), ShouldBeNil)
			So(facts.uploads, ShouldHaveLength, 1)
			Convey("When 再连一次而内容没变, Then 照样整份上报(连上就报一次全量)", func() {
				So(r.Connected(ctx), ShouldBeNil)
				So(facts.uploads, ShouldHaveLength, 2)
			})
		})

		Convey("Given 已经报过, When 检查变化而探测结果相同, Then 不发请求", func() {
			So(r.Connected(ctx), ShouldBeNil)
			So(r.CheckForChange(ctx), ShouldBeNil)
			So(facts.uploads, ShouldHaveLength, 1)
			So(facts.calls, ShouldEqual, 2)

			Convey("When 本机探测结果变了, Then 报一次", func() {
				facts.version = "2.2.0"
				So(r.CheckForChange(ctx), ShouldBeNil)
				So(facts.uploads, ShouldHaveLength, 2)
				So(facts.uploads[1].Runtimes[0].Version, ShouldEqual, "2.2.0")
			})
		})

		Convey("Given 用户点了刷新, When Refresh 而内容没变, Then 仍然立即重探并上报,并交回这份新情况", func() {
			So(r.Connected(ctx), ShouldBeNil)
			report, err := r.Refresh(ctx)
			So(err, ShouldBeNil)
			So(facts.uploads, ShouldHaveLength, 2)
			So(report.CollectedAt, ShouldEqual, facts.uploads[1].CollectedAt)
		})

		Convey("Given 上报失败, When Refresh, Then 刚探到的情况照样交回且不报错(界面要显示它)", func() {
			facts.uploadOK = false
			report, err := r.Refresh(ctx)
			So(err, ShouldBeNil)
			So(report.Runtimes[0].Version, ShouldEqual, "2.1.0")
			So(facts.uploads, ShouldHaveLength, 1)
		})

		Convey("Given 探测失败, When Refresh, Then 报错", func() {
			facts.err = errors.New("db down")
			_, err := r.Refresh(ctx)
			So(err, ShouldNotBeNil)
		})

		Convey("Given server 还没有这个端点(404)或够不着, When 上报失败, Then 返回错误、最新情况照样记下,下一次检查变化时重试", func() {
			facts.uploadOK = false
			So(r.Connected(ctx), ShouldNotBeNil)
			latest, ok := r.Latest()
			So(ok, ShouldBeTrue)
			So(latest.Runtimes[0].Version, ShouldEqual, "2.1.0")

			facts.uploadOK = true
			So(r.CheckForChange(ctx), ShouldBeNil)
			So(facts.uploads, ShouldHaveLength, 2)
			Convey("Then 成功之后同样的内容不再重发", func() {
				So(r.CheckForChange(ctx), ShouldBeNil)
				So(facts.uploads, ShouldHaveLength, 2)
			})
		})

		Convey("Given 探测失败, When 检查变化, Then 报错、不上报、保留上一份", func() {
			So(r.Connected(ctx), ShouldBeNil)
			facts.err = errors.New("db down")
			So(r.CheckForChange(ctx), ShouldNotBeNil)
			So(facts.uploads, ShouldHaveLength, 1)
			latest, ok := r.Latest()
			So(ok, ShouldBeTrue)
			So(latest.Runtimes[0].Version, ShouldEqual, "2.1.0")
		})

		Convey("Given 此刻报不了(未登录账号), When 周期检查变化, Then 不探测也不上报 —— 没人收的探测是白起一批子进程", func() {
			loggedIn := false
			gated := devicefacts.NewReporter(facts.collect, facts.upload, devicefacts.WithUploadGate(func() bool { return loggedIn }))
			So(gated.CheckForChange(ctx), ShouldBeNil)
			So(facts.calls, ShouldEqual, 0)
			So(facts.uploads, ShouldBeEmpty)
			Convey("When 登录之后再检查, Then 照常探测并上报", func() {
				loggedIn = true
				So(gated.CheckForChange(ctx), ShouldBeNil)
				So(facts.uploads, ShouldHaveLength, 1)
			})
			Convey("When 用户点刷新, Then 门关着也照样重探(界面要看),只是不上报", func() {
				report, err := gated.Refresh(ctx)
				So(err, ShouldBeNil)
				So(report.Runtimes, ShouldHaveLength, 1)
				So(facts.uploads, ShouldBeEmpty)
			})
		})

		Convey("Given 宿主没有上报口(未接 server), When 刷新, Then 只探测不上报", func() {
			local := devicefacts.NewReporter(facts.collect, nil)
			report, err := local.Refresh(ctx)
			So(err, ShouldBeNil)
			So(report.Runtimes[0].Version, ShouldEqual, "2.1.0")
			So(facts.uploads, ShouldBeEmpty)
		})

		Convey("Given 从没探测过, When Current, Then 当场探测一次;之后交回记下的那份", func() {
			_, ok := r.Latest()
			So(ok, ShouldBeFalse)
			first, err := r.Current(ctx)
			So(err, ShouldBeNil)
			second, err := r.Current(ctx)
			So(err, ShouldBeNil)
			So(second, ShouldResemble, first)
			So(facts.calls, ShouldEqual, 1)
			So(facts.uploads, ShouldBeEmpty)
		})
	})
}
