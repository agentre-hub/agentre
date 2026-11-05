package skill_svc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc/mock_skill_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// countingDisc 记录 Discover 被调用(= 起 CLI 子进程)的次数;gate 非空时阻塞到被放行。
type countingDisc struct {
	calls *atomic.Int32
	gate  chan struct{}
}

func (c countingDisc) Discover(_ context.Context, _ agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	c.calls.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	return []agentskill.SkillPack{{ID: "p@m", Name: "p", Installed: true, Source: agentskill.SourceInstalled}}, nil
}

type cacheFixture struct {
	svc   *Service
	calls *atomic.Int32
}

// newCacheFixture 造一个本机 claudecode 档的服务,发现器是计数替身。
func newCacheFixture(t *testing.T, gate chan struct{}) *cacheFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	al := mock_skill_svc.NewMockAgentLookup(ctrl)
	bl := mock_skill_svc.NewMockBackendLookup(ctrl)
	al.EXPECT().Find(gomock.Any(), int64(1)).Return(&agent_entity.Agent{ID: 1, AgentBackendID: 9}, nil).AnyTimes()
	bl.EXPECT().Find(gomock.Any(), int64(9)).Return(&agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeClaudeCode), CLIPath: "/bin/claude",
	}, nil).AnyTimes()
	calls := &atomic.Int32{}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, countingDisc{calls: calls, gate: gate})
	t.Cleanup(restore)
	target := skillTarget()
	target.AgentBackendID = 9
	et := &fakeExecTargets{rows: []*agent_entity.AgentExecTarget{target}}
	return &cacheFixture{svc: newForTest(al, bl, et), calls: calls}
}

func TestDiscoveryCache_Local(t *testing.T) {
	ctx := context.Background()
	Convey("给定本机 claudecode 档", t, func() {
		Convey("同一机器与后端的第二次请求不再起子进程(packs 与 commands 共用)", func() {
			f := newCacheFixture(t, nil)
			_, err := f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, false)
			So(err, ShouldBeNil)
			_, err = f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, false)
			So(err, ShouldBeNil)
			So(f.calls.Load(), ShouldEqual, 1)
			_, err = f.svc.ListAgentSkillCommands(ctx, 1, "/proj")
			So(err, ShouldBeNil)
			_, err = f.svc.ListAgentSkillCommands(ctx, 1, "/proj")
			So(err, ShouldBeNil)
			So(f.calls.Load(), ShouldEqual, 1)
		})

		Convey("并发请求只起一次", func() {
			gate := make(chan struct{})
			f := newCacheFixture(t, gate)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, false)
				}()
			}
			for f.calls.Load() == 0 {
			}
			close(gate)
			wg.Wait()
			So(f.calls.Load(), ShouldEqual, 1)
		})

		Convey("refresh=true 强制重扫", func() {
			f := newCacheFixture(t, nil)
			_, _ = f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, false)
			_, err := f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, true)
			So(err, ShouldBeNil)
			So(f.calls.Load(), ShouldEqual, 2)
		})
	})
}

func TestDiscoveryCache_Invalidation(t *testing.T) {
	ctx := context.Background()
	Convey("给定注入时钟与注册文件指纹的本机服务", t, func() {
		f := newCacheFixture(t, nil)
		now := time.Unix(1000, 0)
		stamp := "v1"
		f.svc.cache = agentskill.NewDiscoveryCacheWith(5*time.Minute, func() time.Time { return now })
		f.svc.localStamp = func() string { return stamp }
		list := func() { _, _ = f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, false) }
		list()
		So(f.calls.Load(), ShouldEqual, 1)

		Convey("TTL 内命中,过期后重扫", func() {
			now = now.Add(4*time.Minute + 59*time.Second)
			list()
			So(f.calls.Load(), ShouldEqual, 1)
			now = now.Add(2 * time.Second)
			list()
			So(f.calls.Load(), ShouldEqual, 2)
		})

		Convey("插件注册文件 mtime 变化后重扫", func() {
			stamp = "v2"
			list()
			So(f.calls.Load(), ShouldEqual, 2)
			list()
			So(f.calls.Load(), ShouldEqual, 2)
		})

		Convey("ctlskill 安装(全局代数 +1)后重扫", func() {
			agentskill.BumpGeneration()
			list()
			So(f.calls.Load(), ShouldEqual, 2)
		})

		Convey("refresh=true 也清掉同机器后端的 commands 清单", func() {
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/proj")
			So(f.calls.Load(), ShouldEqual, 1)
			_, err := f.svc.ListAgentSkillPacksForTarget(ctx, 1, 9, true)
			So(err, ShouldBeNil)
			So(f.calls.Load(), ShouldEqual, 2)
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/proj")
			So(f.calls.Load(), ShouldEqual, 2) // packs 刷新后已缓存新结果,commands 重算不再起进程
		})

		Convey("不同 cwd 的 commands 各自一份", func() {
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/a")
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/b")
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/a")
			So(f.calls.Load(), ShouldEqual, 1) // 包发现共用一份
		})
	})
}

type errDisc struct {
	calls *atomic.Int32
	fail  *atomic.Bool
}

func (e errDisc) Discover(_ context.Context, _ agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	e.calls.Add(1)
	if e.fail.Load() {
		return nil, context.DeadlineExceeded
	}
	return []agentskill.SkillPack{{ID: "p@m", Installed: true}}, nil
}

func TestDiscoveryCache_ErrorsAreNotCached(t *testing.T) {
	Convey("发现失败不当成功缓存,下一次重试", t, func() {
		f := newCacheFixture(t, nil)
		calls, fail := &atomic.Int32{}, &atomic.Bool{}
		fail.Store(true)
		restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, errDisc{calls: calls, fail: fail})
		defer restore()
		_, err := f.svc.ListAgentSkillPacksForTarget(context.Background(), 1, 9, false)
		So(err, ShouldNotBeNil)
		fail.Store(false)
		cat, err := f.svc.ListAgentSkillPacksForTarget(context.Background(), 1, 9, false)
		So(err, ShouldBeNil)
		So(calls.Load(), ShouldEqual, 2)
		So(cat.Packs, ShouldNotBeEmpty)
	})
}

// countingRemote 记录远端发现次数,按 deviceID 区分。
type countingRemote struct {
	packCalls, cmdCalls atomic.Int32
}

func (c *countingRemote) ListSkills(_ context.Context, _ int64, _ string) ([]agentskill.SkillPack, error) {
	c.packCalls.Add(1)
	return []agentskill.SkillPack{{ID: "r@m", Installed: true}}, nil
}

func (c *countingRemote) ListSkillCommands(_ context.Context, _ int64, _, _ string, _ []agent_entity.AgentSkillItem) ([]agentskill.SkillCommand, error) {
	c.cmdCalls.Add(1)
	return []agentskill.SkillCommand{{Name: "x"}}, nil
}

func TestDiscoveryCache_Remote(t *testing.T) {
	ctx := context.Background()
	Convey("给定指向两台不同远端 daemon 的 backend", t, func() {
		ctrl := gomock.NewController(t)
		al := mock_skill_svc.NewMockAgentLookup(ctrl)
		bl := mock_skill_svc.NewMockBackendLookup(ctrl)
		al.EXPECT().Find(gomock.Any(), int64(1)).Return(&agent_entity.Agent{ID: 1, AgentBackendID: 8}, nil).AnyTimes()
		al.EXPECT().Find(gomock.Any(), int64(2)).Return(&agent_entity.Agent{ID: 2, AgentBackendID: 9}, nil).AnyTimes()
		bl.EXPECT().Find(gomock.Any(), int64(8)).Return(&agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:d-a",
		}, nil).AnyTimes()
		bl.EXPECT().Find(gomock.Any(), int64(9)).Return(&agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:d-b",
		}, nil).AnyTimes()
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return(devicefp.Carrier("sha256:self"), nil).AnyTimes()
		rds.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
			{ID: 7, DaemonFingerprint: "sha256:d-a"}, {ID: 8, DaemonFingerprint: "sha256:d-b"},
		}, nil).AnyTimes()
		prev := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
		ta, tb := skillTarget(), skillTarget()
		ta.AgentBackendID, tb.AgentBackendID = 8, 9
		et := &fakeExecTargets{rows: []*agent_entity.AgentExecTarget{ta, tb}}
		rem := &countingRemote{}
		s := newForTestRemote(al, bl, et, rem)

		Convey("同设备第二次不再问远端;不同设备不共享", func() {
			_, _ = s.ListAgentSkillPacksForTarget(ctx, 1, 8, false)
			_, _ = s.ListAgentSkillPacksForTarget(ctx, 1, 8, false)
			So(rem.packCalls.Load(), ShouldEqual, 1)
			_, _ = s.ListAgentSkillPacksForTarget(ctx, 2, 9, false)
			So(rem.packCalls.Load(), ShouldEqual, 2)
			_, _ = s.ListAgentSkillPacksForTarget(ctx, 1, 8, true)
			So(rem.packCalls.Load(), ShouldEqual, 3)
		})

		Convey("commands 按设备 / cwd / 授权区分缓存", func() {
			_, _ = s.ListAgentSkillCommands(ctx, 1, "/p")
			_, _ = s.ListAgentSkillCommands(ctx, 1, "/p")
			So(rem.cmdCalls.Load(), ShouldEqual, 1)
			_, _ = s.ListAgentSkillCommands(ctx, 1, "/q") // 另一个 cwd
			So(rem.cmdCalls.Load(), ShouldEqual, 2)
			_, _ = s.ListAgentSkillCommands(ctx, 2, "/p") // 另一台设备
			So(rem.cmdCalls.Load(), ShouldEqual, 3)
			ta.SetSkills([]agent_entity.AgentSkillItem{{ID: "p@m", Enabled: true}}) // 授权变了
			_, _ = s.ListAgentSkillCommands(ctx, 1, "/p")
			So(rem.cmdCalls.Load(), ShouldEqual, 4)
		})
	})
}
