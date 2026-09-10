// Package chat_import_svc 把 CLI 留在磁盘上的会话读回 agentre:发现候选、预览一条、
// 把它落成一条 agentre 会话与逐轮消息。
//
// 三条边界值得先说清:
//
//  1. **只经注册表够读取器**。本包不 import 任何一家 runtime 的构造器,一律走
//     internal/pkg/transcriptimport 的注册表(各 runtime 包 init 时 Register)。
//     后端增删因此不改这里一行。
//  2. **块只由既有 turn.Dispatcher + Accumulator 产出**。chat_svc 的 dispatcher 是
//     私有的、它的适配器直接够 chat_repo 包级全局(每帧一次单列 UPDATE),导入路径
//     用不了;所以这里自建一份 dispatcher,注册同一批导出 handler,只把持久化适配器
//     换成**纯内存**的 —— 一轮的块攒齐之后整条 Create,不在轮内反复写库。
//     绝不另开第二条 blocks_json 生成路径(硬约束 2)。
//  3. **磁盘档案只读**(硬约束 1)。本包只调 Source 的 Scan / Open / Turns。
package chat_import_svc

import (
	"context"
	"os"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

//go:generate mockgen -source svc.go -destination mock_chat_import_svc/mock_chat_import_svc.go

// ChatImportSvc 导入本地会话的服务契约。
type ChatImportSvc interface {
	// ListCandidates 列出某台设备上可导入的候选,按最后活动时间倒序。
	// 单个后端失败只让它自己那一档报出原因,其余照常列出。
	ListCandidates(ctx context.Context, req *ListCandidatesRequest) (*ListCandidatesResponse, error)
	// Preview 打开一条候选,给出元信息、缺口声明与前几轮的真实转录。
	Preview(ctx context.Context, req *PreviewRequest) (*PreviewResponse, error)
	// Import 把一条候选落成一条 agentre 会话与逐轮消息。onProgress 可为 nil;
	// 非 nil 时每写完一轮回调一次(done, total)。
	Import(ctx context.Context, req *ImportRequest, onProgress ProgressFunc) (*ImportResponse, error)
	// Cancel 中断一笔还在写的导入(按发起时的 RequestID 找)。整笔回滚,
	// 不留半截会话;未知 id 答 Canceled=false 而不是报错。
	Cancel(ctx context.Context, req *CancelImportRequest) (*CancelImportResponse, error)
}

// ProgressFunc 按轮计的进度回调。total 为 0 表示总轮数未知(元信息里没有轮数)。
type ProgressFunc func(done, total int)

// TxRunner 把一次导入的全部写入收进一个原子单元:整条落库,或者一条消息都不留
// (spec「导入与落库·原子性」)。抽成接口是因为服务单测不连库 —— 真实现走
// db.Ctx(ctx).Transaction,单测顶一个记录提交/回滚的假实现。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// SourceResolver 回答「这台设备上有哪些磁盘读取器」。
//
// 设备维度刻意留在这一层而不是 transcriptimport.Filter 里:一个 Source 只认识它
// 所在那台机器上的档案,选哪台机器是聚合的事。发现 / 预览 / 导入三条路因此不分叉:
// 它们拿到的都是 Source,本机的读注册表,远端的经 daemon RPC(见 remote.go)。
type SourceResolver func(ctx context.Context, deviceID int64) ([]transcriptimport.Source, error)

// deviceSources 是默认实现:deviceID = 0 读本机注册表快照,非 0 把那台已配对
// 设备包成同形状的一组远端读取器。
func deviceSources(_ context.Context, deviceID int64) ([]transcriptimport.Source, error) {
	if deviceID == 0 {
		return transcriptimport.Sources(), nil
	}
	if deviceID < 0 {
		return nil, errInvalidDevice
	}
	return remoteSources(deviceID, remoteGateway), nil
}

type chatImportSvc struct {
	tx         TxRunner
	dirExists  func(path string) bool
	dispatcher *turn.Dispatcher
	sources    SourceResolver

	// 非属主仓储的窄端口(ISP,决策 5):本包只调这几个方法,不该整包依赖
	// chat_repo.SessionRepo / agent_repo.AgentRepo 等 40 方法级别的胖接口。
	sessions      SessionPort
	messages      MessagePort
	agents        AgentPort
	agentBackends AgentBackendPort

	// running 是「正在写的那几笔导入」的 cancel 函数,key = 前端发起时给的
	// RequestID。取消走它而不是关连接:导入是一次同步调用,中断只能从外面另
	// 敲一下(同 agent_backend_svc 的 probes)。
	runningMu sync.Mutex
	running   map[string]context.CancelFunc
}

func newSvc() *chatImportSvc {
	return &chatImportSvc{
		tx:            dbTxRunner{},
		running:       map[string]context.CancelFunc{},
		dirExists:     dirExists,
		dispatcher:    newImportDispatcher(),
		sessions:      sessionRepoDelegate{},
		messages:      messageRepoDelegate{},
		agents:        agentRepoDelegate{},
		agentBackends: agentBackendRepoDelegate{},
		sources:       deviceSources,
	}
}

var defaultImpl ChatImportSvc = newSvc()

// Default 返回默认实现。
func Default() ChatImportSvc { return defaultImpl }

// Register 替换默认实现(供 e2e / 上层组合根)。
func Register(impl ChatImportSvc) {
	if impl == nil {
		return
	}
	defaultImpl = impl
}

// dirExists 回答「这个工作目录还在不在」。不存在 → 导入降级为只读(spec 决策 16)。
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// sourceFor 取某设备上某后端的读取器;没有则返回 nil。
func (s *chatImportSvc) sourceFor(ctx context.Context, deviceID int64, backend agent_backend_entity.BackendType) (transcriptimport.Source, error) {
	sources, err := s.sources(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		if src != nil && src.Backend() == backend {
			return src, nil
		}
	}
	return nil, nil
}

// registerRun 记下这一笔导入的 cancel,返回注销闭包。RequestID 为空 = 不可中断。
func (s *chatImportSvc) registerRun(id string, cancel context.CancelFunc) func() {
	if id == "" {
		return func() {}
	}
	s.runningMu.Lock()
	if s.running == nil {
		s.running = map[string]context.CancelFunc{}
	}
	s.running[id] = cancel
	s.runningMu.Unlock()
	return func() {
		s.runningMu.Lock()
		delete(s.running, id)
		s.runningMu.Unlock()
	}
}

// Cancel 中断一笔还在写的导入。未知 id 不是错误(前端竞态属常态)。
func (s *chatImportSvc) Cancel(_ context.Context, req *CancelImportRequest) (*CancelImportResponse, error) {
	if req == nil || req.RequestID == "" {
		return &CancelImportResponse{}, nil
	}
	s.runningMu.Lock()
	cancel, ok := s.running[req.RequestID]
	s.runningMu.Unlock()
	if !ok {
		return &CancelImportResponse{}, nil
	}
	cancel()
	return &CancelImportResponse{Canceled: true}, nil
}
