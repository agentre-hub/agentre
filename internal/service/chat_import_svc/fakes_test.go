package chat_import_svc

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/service/chat_import_svc/mock_deps"
)

// fakeTranscript 是一份内存转录:Turns 逐轮回调 yield,与磁盘读取器同一个推式契约。
type fakeTranscript struct {
	meta   transcriptimport.Meta
	turns  []transcriptimport.Turn
	closed bool
}

func (f *fakeTranscript) Meta() transcriptimport.Meta { return f.meta }

func (f *fakeTranscript) Turns(ctx context.Context, yield func(transcriptimport.Turn) error) error {
	for _, t := range f.turns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := yield(t); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeTranscript) Close() error {
	f.closed = true
	return nil
}

// fakeSource 顶替注册表里某个后端的磁盘读取器。
type fakeSource struct {
	backend    agent_backend_entity.BackendType
	candidates []transcriptimport.Candidate
	scanErr    error
	transcript *fakeTranscript
	openErr    error

	scanFilters []transcriptimport.Filter
	openedWith  []transcriptimport.Locator
}

func (f *fakeSource) Backend() agent_backend_entity.BackendType { return f.backend }

func (f *fakeSource) Scan(_ context.Context, filter transcriptimport.Filter) ([]transcriptimport.Candidate, error) {
	f.scanFilters = append(f.scanFilters, filter)
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.candidates, nil
}

func (f *fakeSource) Open(_ context.Context, loc transcriptimport.Locator) (transcriptimport.Transcript, error) {
	f.openedWith = append(f.openedWith, loc)
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.transcript, nil
}

// installSource 把 fake 读取器挂进注册表,测试结束还原。
func installSource(t *testing.T, src *fakeSource) *fakeSource {
	t.Helper()
	restore := transcriptimport.SwapSourceForTest(src.backend, src)
	t.Cleanup(restore)
	return src
}

// fakeTx 顶替真事务:记录 fn 有没有跑、最终是提交还是回滚。
// 「中途失败不留半截」的判据靠它 —— 单测不连库,能证明的是「全部写入都发生在同一个
// 原子单元里,且失败时这个单元没有提交」。
type fakeTx struct {
	ran        int
	committed  bool
	rolledBack bool
	inTx       bool
}

func (f *fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	f.ran++
	f.inTx = true
	err := fn(ctx)
	f.inTx = false
	if err != nil {
		f.rolledBack = true
		return err
	}
	f.committed = true
	return nil
}

type repoMocks struct {
	session *mock_deps.MockSessionPort
	message *mock_deps.MockMessagePort
	agent   *mock_deps.MockAgentPort
	backend *mock_deps.MockAgentBackendPort
	tx      *fakeTx
	svc     *chatImportSvc
}

// withMocks 装好四个窄 repo port mock + 假事务,并把「工作目录还在不在」钉成确定集合
// (单测不碰磁盘)。四个 mock 直接注进被测实例的字段,不再经 chat_repo/agent_repo 的
// 包级 Register(ISP,决策 5):这些 mock 只实现本包实际调用的那几个方法,不必满足
// chat_repo.SessionRepo 等仓储的完整方法集。返回的 svc 是被测实例。
func withMocks(t *testing.T, existingDirs ...string) *repoMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &repoMocks{
		session: mock_deps.NewMockSessionPort(ctrl),
		message: mock_deps.NewMockMessagePort(ctrl),
		agent:   mock_deps.NewMockAgentPort(ctrl),
		backend: mock_deps.NewMockAgentBackendPort(ctrl),
		tx:      &fakeTx{},
	}

	dirs := make(map[string]struct{}, len(existingDirs))
	for _, p := range existingDirs {
		dirs[p] = struct{}{}
	}
	m.svc = newSvc()
	m.svc.tx = m.tx
	m.svc.sessions = m.session
	m.svc.messages = m.message
	m.svc.agents = m.agent
	m.svc.agentBackends = m.backend
	m.svc.dirExists = func(path string) bool {
		_, ok := dirs[path]
		return ok
	}
	return m
}

var errBoom = errors.New("boom")
