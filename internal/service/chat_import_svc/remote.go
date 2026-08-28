package chat_import_svc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// remote.go 是「问另一台机器」的那一半:把已配对 daemon 上的同一份磁盘读取器包成
// 本地看起来一模一样的 transcriptimport.Source。
//
// 设备维度就落在这里(而不是 transcriptimport.Filter 里):一个 Source 只认识它
// 所在那台机器上的档案,选哪台机器是聚合的事。发现 / 预览 / 导入三条路因此一行都
// 不必分叉 —— 它们拿到的都是 Source。
//
// 三态在这里定型(spec「远端」):
//   - ok —— 问出来了,照它增删。
//   - unavailable —— 这台机器此刻答不出:拨号失败,或某个后端的目录读不动。
//   - unsupported —— 这个 daemon 版本不认识 transcriptimport.* 方法族(-32601)。
//     它**必须**与「这台机器没有会话」分得开,否则升级 daemon 这条出路说不出口。

var (
	// errDeviceOffline 拨不通那台设备 / 租约借不出来。
	errDeviceOffline = errors.New("chat_import_svc: 远端设备此刻连不上")
	// errBackendUnavailable 那台机器上这个后端答不出(没装那个 CLI、目录读不动)。
	errBackendUnavailable = errors.New("chat_import_svc: 这台设备上没有这个后端的会话档案")
)

// transcriptGateway 是一次远端往返。抽成接口只为单测顶掉真实连接 —— 真实往返
// 由 internal/daemon 的 protorpc 用例端到端钉住。
type transcriptGateway interface {
	Scan(ctx context.Context, deviceID int64, params wire.ScanParams) (*wire.ScanResult, error)
	Open(ctx context.Context, deviceID int64, params wire.OpenParams) (*wire.OpenResult, error)
	Turns(ctx context.Context, deviceID int64, params wire.TurnsParams) (*wire.TurnsResult, error)
}

var remoteGateway transcriptGateway = deviceGateway{}

// ── gateway over protorpc ───────────────────────────────────────────────────

type deviceGateway struct{}

func (deviceGateway) Scan(ctx context.Context, deviceID int64, params wire.ScanParams) (*wire.ScanResult, error) {
	resp, err := callDevice(ctx, deviceID, agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN,
		protowire.TranscriptScanParamsToProto(params),
		func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })
	if err != nil {
		return nil, err
	}
	result := protowire.TranscriptScanResultFromProto(resp)
	return &result, nil
}

func (deviceGateway) Open(ctx context.Context, deviceID int64, params wire.OpenParams) (*wire.OpenResult, error) {
	resp, err := callDevice(ctx, deviceID, agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN,
		protowire.TranscriptOpenParamsToProto(params),
		func() *agentrewire.TranscriptImportOpenResponse { return &agentrewire.TranscriptImportOpenResponse{} })
	if err != nil {
		return nil, err
	}
	result := protowire.TranscriptOpenResultFromProto(resp)
	return &result, nil
}

func (deviceGateway) Turns(ctx context.Context, deviceID int64, params wire.TurnsParams) (*wire.TurnsResult, error) {
	resp, err := callDevice(ctx, deviceID, agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS,
		protowire.TranscriptTurnsParamsToProto(params),
		func() *agentrewire.TranscriptImportTurnsResponse { return &agentrewire.TranscriptImportTurnsResponse{} })
	if err != nil {
		return nil, err
	}
	result, derr := protowire.TranscriptTurnsResultFromProto(resp)
	if derr != nil {
		return nil, derr
	}
	return &result, nil
}

// callDevice 跑一次远端往返。租约在返回前释放,调用方不持有它。
func callDevice[Req proto.Message, Resp proto.Message](
	ctx context.Context, deviceID int64, method agentrewire.RpcMethod, req Req, newResponse func() Resp,
) (Resp, error) {
	var zero Resp
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_import_svc.callDevice: borrow lease failed",
			zap.Int64("deviceId", deviceID), zap.Uint32("methodId", uint32(method)), zap.Error(err))
		return zero, fmt.Errorf("%w: %s", errDeviceOffline, err.Error())
	}
	defer lease.Release()
	resp, cerr := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(method), req, newResponse)
	if cerr != nil {
		return zero, cerr
	}
	return resp, nil
}

// classifyRemoteErr 把一次远端往返的失败翻成本包的三态哨兵。**它在读取器这一侧
// 而不是网关里**:网关只管字节,「这是哪一态」是领域判断,放在这里单测才够得着,
// 而够得着正是硬约束 3 要的 —— -32601 必须被证明翻成 unsupported。
//
// -32601 单独成一类:旧 agentred 根本没有 transcriptimport.* 方法族,那是
// 「升级 daemon」而不是「调用失败」。
//
// 这里不打日志:每条调用路径末端都会记一次(发现聚合的 Warn、预览 / 导入的
// failed()),在这儿再记一遍就是同一个失败沿调用链记三次。
func classifyRemoteErr(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *protorpc.Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	switch rpcErr.Code {
	case wire.ErrCodeBackendUnavailable:
		return fmt.Errorf("%w: %s", errBackendUnavailable, rpcErr.Message)
	case wire.ErrCodeTranscriptOpen:
		return fmt.Errorf("%w: %s", errRemoteTranscriptOpen, rpcErr.Message)
	}
	return err
}

// errRemoteTranscriptOpen 远端那份转录打不开(文件已删 / 已损坏 / 定位符越界)。
var errRemoteTranscriptOpen = errors.New("chat_import_svc: 远端转录打不开")

// ── remote sources ──────────────────────────────────────────────────────────

// remoteSources 把这台设备包成一组 Source,每个后端一个。
//
// 后端清单取本机注册表的键:daemon 与桌面是同一个 Go 模块、同一批 runtime 包,
// 本机认不出的后端在界面上也无处安放。**那台机器上缺的那个后端不会被静默略过** ——
// 它在扫描应答里没有自己那一档,读取器据此报 unavailable。
func remoteSources(deviceID int64, gw transcriptGateway) []transcriptimport.Source {
	local := transcriptimport.Sources()
	scans := &remoteScans{deviceID: deviceID, gw: gw}
	out := make([]transcriptimport.Source, 0, len(local))
	for _, src := range local {
		if src == nil {
			continue
		}
		out = append(out, &remoteSource{backend: src.Backend(), deviceID: deviceID, gw: gw, scans: scans})
	}
	return out
}

// remoteScans 让「一次 ListCandidates = 一台设备一次往返」成立:三个后端的读取器
// 共用同一次扫描应答,而不是各打一发。生命周期就是一次请求 —— 解析器每次请求
// 新建一组 Source,缓存跟着一起走,不存在陈旧快照。
type remoteScans struct {
	deviceID int64
	gw       transcriptGateway

	mu      sync.Mutex
	results map[transcriptimport.Filter]*scanOnce
}

type scanOnce struct {
	byBackend map[string]wire.BackendScan
	err       error
}

func (r *remoteScans) forBackend(ctx context.Context, filter transcriptimport.Filter, backend string) (wire.BackendScan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results == nil {
		r.results = map[transcriptimport.Filter]*scanOnce{}
	}
	once, ok := r.results[filter]
	if !ok {
		once = &scanOnce{byBackend: map[string]wire.BackendScan{}}
		result, err := r.gw.Scan(ctx, r.deviceID, wire.ScanParams{Filter: filter})
		if err != nil {
			once.err = classifyRemoteErr(err)
		} else if result != nil {
			for _, entry := range result.Backends {
				once.byBackend[entry.Backend] = entry
			}
		}
		r.results[filter] = once
	}
	if once.err != nil {
		return wire.BackendScan{}, once.err
	}
	entry, ok := once.byBackend[backend]
	if !ok {
		return wire.BackendScan{}, errBackendUnavailable
	}
	return entry, nil
}

type remoteSource struct {
	backend  agent_backend_entity.BackendType
	deviceID int64
	gw       transcriptGateway
	scans    *remoteScans
}

func (s *remoteSource) Backend() agent_backend_entity.BackendType { return s.backend }

func (s *remoteSource) Scan(ctx context.Context, filter transcriptimport.Filter) ([]transcriptimport.Candidate, error) {
	entry, err := s.scans.forBackend(ctx, filter, string(s.backend))
	if err != nil {
		return nil, err
	}
	if entry.Status != wire.StatusOK {
		// 空候选自带理由:「问出来就是没有」与「这台机器答不出」是两句话。
		return nil, fmt.Errorf("%w: %s", errBackendUnavailable, entry.Reason)
	}
	return entry.Candidates, nil
}

func (s *remoteSource) Open(ctx context.Context, loc transcriptimport.Locator) (transcriptimport.Transcript, error) {
	result, err := s.gw.Open(ctx, s.deviceID, wire.OpenParams{Backend: string(s.backend), Locator: string(loc)})
	if err != nil {
		return nil, classifyRemoteErr(err)
	}
	if result == nil {
		return nil, errRemoteTranscriptOpen
	}
	return &remoteTranscript{backend: string(s.backend), locator: string(loc), deviceID: s.deviceID, gw: s.gw, meta: result.Meta}, nil
}

// remoteTranscript 把「一页一页取回」翻译回契约里的「一轮一轮回调」:页是取回的
// 单位,不是回放的单位。调用方(预览取前 N 轮、导入逐轮落库)因此与本机读取器
// 用同一段代码,yield 返回非 nil 时立刻停 —— 连下一页都不去取。
type remoteTranscript struct {
	backend  string
	locator  string
	deviceID int64
	gw       transcriptGateway
	meta     transcriptimport.Meta
}

func (t *remoteTranscript) Meta() transcriptimport.Meta { return t.meta }

func (t *remoteTranscript) Turns(ctx context.Context, yield func(transcriptimport.Turn) error) error {
	start := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := t.gw.Turns(ctx, t.deviceID, wire.TurnsParams{
			Backend: t.backend, Locator: t.locator, StartIndex: start, MaxTurns: wire.DefaultMaxTurns,
		})
		if err != nil {
			return classifyRemoteErr(err)
		}
		if page == nil || len(page.Turns) == 0 {
			return nil
		}
		for _, turn := range page.Turns {
			if err := yield(turn); err != nil {
				return err
			}
		}
		if !page.HasMore {
			return nil
		}
		if page.NextIndex <= start {
			// 游标不前进就说明对端答得不自洽;继续问下去只会原地打转。
			logger.Ctx(ctx).Warn("chat_import_svc.remoteTranscript.Turns: cursor did not advance",
				zap.Int64("deviceId", t.deviceID), zap.String("backendType", t.backend), zap.Int("startIndex", start))
			return nil
		}
		start = page.NextIndex
	}
}

// Close 没有远端句柄要还:daemon 侧每次调用自己开自己关(见
// internal/daemon/transcriptimport 的包注释),断线因此不留下要回收的会话状态。
func (t *remoteTranscript) Close() error { return nil }
