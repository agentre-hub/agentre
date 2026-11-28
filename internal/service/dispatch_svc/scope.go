package dispatch_svc

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func millis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (s *dispatchSvc) ResolveLocalCommandScope(ctx context.Context, req *ScopeRequest) (*CommandScope, error) {
	sess, err := s.scopeSession(ctx, req)
	if err != nil {
		return nil, err
	}
	place, err := s.sessionPlace(ctx, sess)
	if err != nil {
		return nil, err
	}
	if place.remote && place.pairedID <= 0 {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidDevice)
	}
	cwd, err := s.sessionCwd(ctx, sess, place)
	if err != nil {
		return nil, err
	}
	scope := &CommandScope{Cwd: cwd}
	if place.remote {
		scope.DeviceID = place.fingerprint
	}
	return scope, nil
}

func (s *dispatchSvc) ResolveSessionWorkspace(ctx context.Context, sessionID int64) (int64, string, error) {
	if sessionID <= 0 {
		return 0, "", i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := s.findSession(ctx, sessionID)
	if err != nil {
		return 0, "", err
	}
	place, err := s.sessionPlace(ctx, sess)
	if err != nil {
		return 0, "", err
	}
	// 远端会话没在本机配对时直接报错，不回落本机 —— 那会拿远端机器上的路径去列本机文件。
	if place.remote && place.pairedID <= 0 {
		return 0, "", i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	cwd, err := s.sessionCwd(ctx, sess, place)
	if err != nil {
		return 0, "", err
	}
	return place.pairedID, cwd, nil
}

// scopeSession 已有会话取库里那一行；预会话只构造值对象，不建会话。
func (s *dispatchSvc) scopeSession(ctx context.Context, req *ScopeRequest) (*chat_entity.Session, error) {
	if req == nil || req.SessionID < 0 || req.AgentID < 0 || req.ProjectID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if req.SessionID > 0 {
		if req.AgentID != 0 || req.ProjectID != 0 {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		return s.findSession(ctx, req.SessionID)
	}
	if req.AgentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	return &chat_entity.Session{AgentID: req.AgentID, ProjectID: req.ProjectID}, nil
}

func (s *dispatchSvc) findSession(ctx context.Context, id int64) (*chat_entity.Session, error) {
	sess, err := s.Sessions.Find(ctx, id)
	if err != nil {
		return nil, operationFailed(ctx, err, zap.Int64("sessionId", id))
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	return sess, nil
}

// place 一条会话落在哪台设备：remote=false 即本机。
type place struct {
	remote      bool
	fingerprint devicefp.Carrier
	pairedID    int64
}

// sessionPlace 会话在哪台设备。钉住了就用钉住的（决策 10：会话不能换设备）：
//   - exec_device_fingerprint 非空：那台设备（本机指纹即本机）；
//   - 只钉了旧的执行目标档（exec_agent_backend_id）：那一档 backend 的设备；
//   - 只钉了引擎配置：本机（远端执行总会带上设备指纹）；
//
// 都没钉（预会话、首轮前）时按派发规则现算：成员 + 项目，取选中的那台；无处可跑报错。
func (s *dispatchSvc) sessionPlace(ctx context.Context, sess *chat_entity.Session) (place, error) {
	var fp devicefp.Carrier
	switch {
	case sess.ExecDeviceFingerprint != "":
		fp = sess.ExecDeviceFingerprint
	case sess.ExecAgentBackendID > 0:
		be, err := s.Engines.LegacyBackend(ctx, sess.ExecAgentBackendID)
		if err != nil {
			return place{}, operationFailed(ctx, err, zap.Int64("agentBackendId", sess.ExecAgentBackendID))
		}
		if be != nil {
			fp = be.DeviceFingerprint
		}
	case sess.EngineProfileID > 0:
	default:
		p, err := s.Plan(ctx, &PlanRequest{AgentID: sess.AgentID, ProjectID: sess.ProjectID})
		if err != nil {
			return place{}, err
		}
		if !p.Found {
			return place{}, i18n.NewError(ctx, code.DispatchNoEligibleDevice)
		}
		fp = p.Chosen
	}
	if fp == "" || fp == s.self() {
		return place{}, nil
	}
	pl := place{remote: true, fingerprint: fp}
	paired, err := s.Devices.Paired(ctx)
	if err != nil {
		return place{}, operationFailed(ctx, err)
	}
	for _, d := range paired {
		if d.Fingerprint == fp {
			pl.pairedID = d.ID
			break
		}
	}
	return pl, nil
}

// sessionCwd 会话自己钉了工作目录（导入的会话）先认它；本机走本机规则（项目路径 / Agent
// 默认目录）；远端自由会话交空串由那台设备兜底，远端项目会话取那台设备上的项目路径。
func (s *dispatchSvc) sessionCwd(ctx context.Context, sess *chat_entity.Session, pl place) (string, error) {
	if sess.Cwd != "" {
		return sess.Cwd, nil
	}
	if !pl.remote {
		return s.Projects.SelfCwd(ctx, sess)
	}
	if sess.ProjectID == 0 {
		return "", nil
	}
	locs, err := s.Projects.Locations(ctx, sess.ProjectID)
	if err != nil {
		return "", operationFailed(ctx, err, zap.Int64("projectId", sess.ProjectID))
	}
	for _, l := range locs {
		if l != nil && l.IsActive() && l.DeviceFingerprint == pl.fingerprint && strings.TrimSpace(l.Path) != "" {
			return l.Path, nil
		}
	}
	return "", i18n.NewError(ctx, code.ProjectLocationMissing)
}
