package chat_import_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

// Import 把一条磁盘会话落成一条 agentre 会话 + 逐轮消息。
//
// 顺序是刻意的,每一步都挡住一个"导完才发现不对"的坑:
//  1. 先对上 agent 的后端与转录的后端 —— 选了一个 codex agent 去接 claude 会话时,
//     CLI 那边根本不认识这个 id(spec「续跑」),必须在写任何一行之前拒掉。
//  2. 打开转录拿到 provider session id,再判重 —— 定位符不是 id,不打开就无从判重。
//  3. 判重命中直接指回库里那条,连事务都不开(硬约束 4)。
//  4. 其余写入全部收在一个事务里:整条落库,或者一条都不留。
func (s *chatImportSvc) Import(ctx context.Context, req *ImportRequest, onProgress ProgressFunc) (*ImportResponse, error) {
	if req == nil || req.AgentID <= 0 || strings.TrimSpace(req.Locator) == "" || strings.TrimSpace(req.Backend) == "" {
		return nil, errInvalid(ctx)
	}
	backend := agent_backend_entity.BackendType(req.Backend)
	if err := s.assertAgentMatchesBackend(ctx, req.AgentID, backend); err != nil {
		return nil, err
	}
	// 取消的抓手在这一笔的 ctx 上:回放与落库全程跑在它下面,取消即整笔回滚
	// (spec「导入过程给出按轮计的进度,可取消」)。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.registerRun(req.RequestID, cancel)()

	tr, err := s.open(ctx, req.DeviceID, backend, transcriptimport.Locator(req.Locator))
	if err != nil {
		return nil, err
	}
	defer func() { _ = tr.Close() }()

	meta := tr.Meta()
	existing, err := s.importedSessionIDs(ctx, []string{meta.ProviderSessionID})
	if err != nil {
		return nil, err
	}
	if id, ok := existing[meta.ProviderSessionID]; ok {
		logger.Ctx(ctx).Info("chat_import_svc.Import: already imported",
			zap.String("backend", req.Backend), zap.Int64("sessionId", id))
		return &ImportResponse{SessionID: id, AlreadyImported: true, Cwd: meta.Cwd}, nil
	}

	// 工作目录:转录里记的那个是首选(spec「续跑」),用户另选了目录就用他选的
	// ("选择新目录"那条出口)。目录已经不在、用户也没另选时**不钉**——钉一个不存在
	// 的目录只会让下一轮起不来,空串是让解析按老规矩现算。
	cwd := strings.TrimSpace(req.Cwd)
	adopted := cwd != ""
	if !adopted && s.dirExists(meta.Cwd) {
		cwd = meta.Cwd
	}

	// cwd 已不存在 → 降级为只读导入:转录照写,但不写 provider_session_id。
	// 续跑必须在原目录启动 CLI,钉一个跑不起来的 id 只会让下一轮报一个更难懂的错;
	// 另选了目录同样只读 —— 那条 id 是按原目录记的,换个目录 CLI 一样找不到它。
	//
	// provider session id 为空同样只读:没有它既 resume 不了、也判不了重,写一个空串
	// 进去只会让这条会话每次都被当成"没导过"。
	resumable := meta.ProviderSessionID != "" && !adopted && s.dirExists(meta.Cwd)

	sess := &chat_entity.Session{
		AgentID:     req.AgentID,
		ProjectID:   req.ProjectID,
		Cwd:         cwd,
		Title:       strings.TrimSpace(meta.Title),
		AgentStatus: "idle",
		Status:      consts.ACTIVE,
		// 建档时间取转录起点:一条三个月前的会话不该因为今天导入就排到列表最前。
		Createtime:    unixMilli(meta.StartedAt),
		LastMessageAt: unixMilli(meta.EndedAt),
	}
	if resumable {
		sess.ProviderSessionID = meta.ProviderSessionID
		// 执行设备跟着转录来的那台机器:从别的机器导进来的会话,下一轮要回到
		// 那台机器上跑 —— 记成本机等于拿一个本机根本没有的 id 去 resume。
		sess.ExecDeviceID = req.DeviceID
	}

	imported := 0
	err = s.tx.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.sessions.Create(txCtx, sess); err != nil {
			return err
		}
		gaps := newGapNotifier(meta.Gaps)
		seq := 1
		lastAt := sess.LastMessageAt
		replayErr := tr.Turns(txCtx, func(t transcriptimport.Turn) error {
			// 取消在轮与轮之间生效:读取器各有各的检查点(本机按行、远端按页),
			// 这里再看一眼,取消的落点因此与读取器无关。
			if err := txCtx.Err(); err != nil {
				return err
			}
			pair, err := s.replayTurn(txCtx, sess, req.Backend, t, seq, gaps)
			if err != nil {
				return err
			}
			if err := s.messages.Create(txCtx, pair.user); err != nil {
				return err
			}
			if err := s.messages.Create(txCtx, pair.assistant); err != nil {
				return err
			}
			seq += 2
			imported++
			if pair.assistant.Createtime > lastAt {
				lastAt = pair.assistant.Createtime
			}
			// 标题:元信息没给时取首条用户消息的首行(codex 另有现成标题,已在 meta 里)。
			// 判据是"这是写下的第一轮"而不是 t.Index —— Index 由读取器给,不该让标题
			// 依赖它从 0 起算。
			if sess.Title == "" && imported == 1 {
				sess.Title = firstLine(t.UserText)
			}
			if onProgress != nil {
				onProgress(imported, meta.Turns)
			}
			return nil
		})
		if replayErr != nil {
			return replayErr
		}
		if imported == 0 {
			return errEmptyTranscript
		}
		sess.LastMessageAt = lastAt
		return s.sessions.Update(txCtx, sess)
	})
	if err != nil {
		if errors.Is(err, errEmptyTranscript) {
			return nil, failed(ctx, code.ChatImportTranscriptEmpty, nil, zap.String("backend", req.Backend))
		}
		return nil, failed(ctx, code.ChatImportTranscriptReplayFailed, err,
			zap.String("backend", req.Backend), zap.Int("importedTurns", imported))
	}
	logger.Ctx(ctx).Info("chat_import_svc.Import: imported",
		zap.String("backend", req.Backend),
		zap.Int64("sessionId", sess.ID),
		zap.Int("turns", imported),
		zap.Bool("resumable", resumable),
		zap.String("cwd", cwd))
	return &ImportResponse{
		SessionID:     sess.ID,
		ReadOnly:      !resumable,
		Cwd:           meta.Cwd,
		ImportedTurns: imported,
	}, nil
}

// assertAgentMatchesBackend 把「这个 agent 接不接得住这条会话」问在写入之前。
func (s *chatImportSvc) assertAgentMatchesBackend(ctx context.Context, agentID int64, backend agent_backend_entity.BackendType) error {
	a, err := s.agents.Find(ctx, agentID)
	if err != nil {
		return failed(ctx, code.ChatImportAgentNoBackend, err, zap.Int64("agentId", agentID))
	}
	if a == nil || a.AgentBackendID <= 0 {
		return failed(ctx, code.ChatImportAgentNoBackend, nil, zap.Int64("agentId", agentID))
	}
	be, err := s.agentBackends.Find(ctx, a.AgentBackendID)
	if err != nil {
		return failed(ctx, code.ChatImportAgentNoBackend, err, zap.Int64("agentId", agentID))
	}
	if be == nil {
		return failed(ctx, code.ChatImportAgentNoBackend, nil, zap.Int64("agentId", agentID))
	}
	if agent_backend_entity.BackendType(be.Type) != backend {
		return failed(ctx, code.ChatImportBackendMismatch, nil,
			zap.Int64("agentId", agentID),
			zap.String("agentBackend", be.Type),
			zap.String("transcriptBackend", string(backend)))
	}
	return nil
}

// open 取这台设备上这个后端的读取器并打开定位符。
func (s *chatImportSvc) open(
	ctx context.Context,
	deviceID int64,
	backend agent_backend_entity.BackendType,
	loc transcriptimport.Locator,
) (transcriptimport.Transcript, error) {
	src, err := s.sourceFor(ctx, deviceID, backend)
	if err != nil {
		return nil, failed(ctx, codeOf(err), err, zap.String("backend", string(backend)))
	}
	if src == nil {
		return nil, failed(ctx, code.ChatImportBackendUnavailable, nil, zap.String("backend", string(backend)))
	}
	tr, err := src.Open(ctx, loc)
	if err != nil {
		// 远端三态先认:「升级 agentred」「设备连不上」与「文件没了」是三条不同
		// 的出路,统一报成"转录打不开"会把前两条藏起来。
		openCode := code.ChatImportTranscriptOpenFailed
		if c, ok := remoteCodeOf(err); ok {
			openCode = c
		}
		return nil, failed(ctx, openCode, err, zap.String("backend", string(backend)))
	}
	if tr == nil {
		return nil, failed(ctx, code.ChatImportTranscriptOpenFailed, nil, zap.String("backend", string(backend)))
	}
	return tr, nil
}

// importedSessionIDs 判重:以库里的 provider_session_id 为准(spec 决策 18)。
func (s *chatImportSvc) importedSessionIDs(ctx context.Context, ids []string) (map[string]int64, error) {
	got, err := s.sessions.ListIDsByProviderSessions(ctx, ids)
	if err != nil {
		return nil, failed(ctx, code.OperationFailed, err)
	}
	return got, nil
}

// firstLine 取首行并去掉首尾空白;整段为空时返回空串(标题宁可空着也不编)。
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
