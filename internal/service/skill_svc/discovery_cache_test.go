package skill_svc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
)

// countingDisc 记录 Discover 被调用(= 起 CLI 子进程)的次数与最后一次的 CLI 路径;gate 非空时阻塞到被放行。
type countingDisc struct {
	calls   *atomic.Int32
	gate    chan struct{}
	cliPath *atomic.Value
}

func (c countingDisc) Discover(_ context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	c.calls.Add(1)
	if c.cliPath != nil {
		c.cliPath.Store(q.CLIPath)
	}
	if c.gate != nil {
		<-c.gate
	}
	return []agentskill.SkillPack{{ID: "p@m", Name: "p", Installed: true, Source: agentskill.SourceInstalled}}, nil
}

type cacheFixture struct {
	*fixture
	calls   *atomic.Int32
	cliPath *atomic.Value
}

// newCacheFixture 造一个只在本机能跑 claudecode 的成员,本机 CLI 路径覆盖为 /bin/claude,
// 发现器是计数替身。
func newCacheFixture(t *testing.T, gate chan struct{}) *cacheFixture {
	t.Helper()
	f := newFixture(t)
	f.cliPath = "/bin/claude"
	calls, cliPath := &atomic.Int32{}, &atomic.Value{}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, countingDisc{calls: calls, gate: gate, cliPath: cliPath})
	t.Cleanup(restore)
	return &cacheFixture{fixture: f, calls: calls, cliPath: cliPath}
}

func (f *cacheFixture) list(ctx context.Context, refresh bool) (MemberSkillCatalogDTO, error) {
	return f.svc.MemberSkillCatalog(ctx, 1, refresh)
}

func TestDiscoveryCache_Local(t *testing.T) {
	ctx := context.Background()
	Convey("给定本机 claudecode 档", t, func() {
		Convey("同一机器与后端的第二次请求不再起子进程(packs 与 commands 共用)", func() {
			f := newCacheFixture(t, nil)
			_, err := f.list(ctx, false)
			So(err, ShouldBeNil)
			_, err = f.list(ctx, false)
			So(err, ShouldBeNil)
			So(f.calls.Load(), ShouldEqual, 1)
			So(f.cliPath.Load(), ShouldEqual, "/bin/claude") // 本机发现用本机的 CLI 路径覆盖
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
					_, _ = f.list(ctx, false)
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
			_, _ = f.list(ctx, false)
			_, err := f.list(ctx, true)
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
		list := func() { _, _ = f.list(ctx, false) }
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
			_, err := f.list(ctx, true)
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
		_, err := f.list(context.Background(), false)
		So(err, ShouldNotBeNil)
		fail.Store(false)
		cat, err := f.list(context.Background(), false)
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
	Convey("给定只能在已配对 agentred 上跑的成员", t, func() {
		f := newFixture(t)
		f.devices = []Device{
			{Fingerprint: selfFP, IsSelf: true, CanRun: false},
			{Fingerprint: "sha256:d-a", PairedID: 7, CanRun: true},
		}
		rem := &countingRemote{}
		f.svc.remote = rem
		// 本机发现器在场(运行时支持技能包),但本机跑不了,不该被问到。
		local := &atomic.Int32{}
		t.Cleanup(agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, countingDisc{calls: local}))

		Convey("同设备第二次不再问远端;换了设备不共享;refresh 重问", func() {
			_, _ = f.svc.MemberSkillCatalog(ctx, 1, false)
			_, _ = f.svc.MemberSkillCatalog(ctx, 1, false)
			So(rem.packCalls.Load(), ShouldEqual, 1)
			f.devices[1] = Device{Fingerprint: "sha256:d-b", PairedID: 8, CanRun: true}
			_, _ = f.svc.MemberSkillCatalog(ctx, 1, false)
			So(rem.packCalls.Load(), ShouldEqual, 2)
			_, _ = f.svc.MemberSkillCatalog(ctx, 1, true)
			So(rem.packCalls.Load(), ShouldEqual, 3)
			So(local.Load(), ShouldEqual, 0)
		})

		Convey("commands 按设备 / cwd / 授权区分缓存", func() {
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/p")
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/p")
			So(rem.cmdCalls.Load(), ShouldEqual, 1)
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/q") // 另一个 cwd
			So(rem.cmdCalls.Load(), ShouldEqual, 2)
			f.devices[1] = Device{Fingerprint: "sha256:d-b", PairedID: 8, CanRun: true} // 另一台设备
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/p")
			So(rem.cmdCalls.Load(), ShouldEqual, 3)
			f.grant("claudecode", agent_entity.AgentSkillItem{ID: "p@m", Enabled: true}) // 授权变了
			_, _ = f.svc.ListAgentSkillCommands(ctx, 1, "/p")
			So(rem.cmdCalls.Load(), ShouldEqual, 4)
		})
	})
}
