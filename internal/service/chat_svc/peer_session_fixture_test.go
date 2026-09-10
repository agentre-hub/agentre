package chat_svc

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 本文件是 chat_svc 侧对端会话用例的公共夹具。与 chat_svc/peerstream 里同名夹具
// 是同一套桩,差别只有一处 —— 这里造的是 *chatSvc(用例要驱动整轮),那边造的是
// 出口自己(*peerstream.Publisher)。夹具跨包不能共享,这份重复是包边界的代价。

// fakeFrameSeqLedger 是帧编号台账的内存替身。
type fakeFrameSeqLedger struct {
	mu   sync.Mutex
	rows []transcript_repo.FrameSeqRow
}

func (l *fakeFrameSeqLedger) Load(_ context.Context, sessionID int64) (map[transcript_repo.FrameKey]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[transcript_repo.FrameKey]int64{}
	for _, row := range l.rows {
		if row.SessionID != sessionID {
			continue
		}
		key := transcript_repo.FrameKey{MessageID: row.MessageID, BlockIdx: row.BlockIdx, Ordinal: row.Ordinal}
		if prev, ok := out[key]; ok && prev >= row.Seq {
			continue
		}
		out[key] = row.Seq
	}
	return out, nil
}

func (l *fakeFrameSeqLedger) Allocate(_ context.Context, sessionID int64, keys []transcript_repo.FrameKey) ([]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(keys) == 0 {
		return nil, nil
	}
	var latest int64
	for _, row := range l.rows {
		if row.SessionID == sessionID && row.Seq > latest {
			latest = row.Seq
		}
	}
	seqs := make([]int64, 0, len(keys))
	for i, key := range keys {
		seq := latest + int64(i) + 1
		l.rows = append(l.rows, transcript_repo.FrameSeqRow{
			SessionID: sessionID, MessageID: key.MessageID,
			BlockIdx: key.BlockIdx, Ordinal: key.Ordinal, Seq: seq,
		})
		seqs = append(seqs, seq)
	}
	return seqs, nil
}

func (l *fakeFrameSeqLedger) DeleteBySession(_ context.Context, sessionID int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.rows[:0]
	var removed int64
	for _, row := range l.rows {
		if row.SessionID == sessionID {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	l.rows = kept
	return removed, nil
}

type peerSessionTestDeps struct {
	agent   *mock_agent_repo.MockAgentRepo
	backend *mock_agent_backend_repo.MockAgentBackendRepo
	session *mock_chat_repo.MockSessionRepo
	message *mock_transcript_repo.MockMessageRepo
	device  *mock_remote_device_svc.MockRemoteDeviceSvc
	project *mock_project_repo.MockProjectRepo
	// ledger 是帧编号台账。它按会话记账并活得比 svc 长，用例据此模拟「宿主重启」。
	ledger *fakeFrameSeqLedger
	svc    *chatSvc
	// projects 是这台电脑上的项目清单，由用例按需摆好；projectListCalls 记下它被
	// 读了几次——「一次列举只读一遍」是这份清单唯一的性能约束，它得测得到。
	projects         []*project_entity.Project
	projectListCalls int
}

func setupPeerSessionTest(t *testing.T) *peerSessionTestDeps {
	t.Helper()
	ctrl := gomock.NewController(t)
	deps := &peerSessionTestDeps{
		agent:   mock_agent_repo.NewMockAgentRepo(ctrl),
		backend: mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		session: mock_chat_repo.NewMockSessionRepo(ctrl),
		message: mock_transcript_repo.NewMockMessageRepo(ctrl),
		device:  mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl),
		project: mock_project_repo.NewMockProjectRepo(ctrl),
		ledger:  &fakeFrameSeqLedger{},
		svc:     NewChat(NoopEmitter{}).(*chatSvc),
	}
	prevAgent, prevBackend, prevSession, prevMessage, prevDevice := agent_repo.Agent(), agent_backend_repo.AgentBackend(), chat_repo.Session(), transcript_repo.Message(), remote_device_svc.Default()
	prevProject := project_repo.Project()
	prevFrameSeq := transcript_repo.FrameSeq()
	transcript_repo.RegisterFrameSeq(deps.ledger)
	agent_repo.RegisterAgent(deps.agent)
	agent_backend_repo.RegisterAgentBackend(deps.backend)
	chat_repo.RegisterSession(deps.session)
	transcript_repo.RegisterMessage(deps.message)
	remote_device_svc.SetDefault(deps.device)
	project_repo.RegisterProject(deps.project)
	// 项目清单是列会话时的一张查询表；不摆内容的用例读到的是空表。
	deps.project.EXPECT().List(gomock.Any()).DoAndReturn(
		func(context.Context) ([]*project_entity.Project, error) {
			deps.projectListCalls++
			return deps.projects, nil
		}).AnyTimes()
	// 会话摘要上的 peer_fingerprint 报的是这台桌面端自己。
	deps.device.EXPECT().DeviceFingerprint().Return(testDesktopFingerprint, nil).AnyTimes()
	// conversation_id → 本地主键的反查是 conversation_id 唯一索引上的一次查询
	// (见 ResolvePeerConversation)。41/42/43 是这些用例里本机有的三条会话。
	deps.session.EXPECT().FindByConversationID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, conversationID string) (*chat_entity.Session, error) {
			for _, id := range []int64{41, 42, 43} {
				if conversationID == convID(id) {
					return &chat_entity.Session{ID: id, ConversationID: conversationID, Status: consts.ACTIVE}, nil
				}
			}
			return nil, nil
		}).AnyTimes()
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		chat_repo.RegisterSession(prevSession)
		transcript_repo.RegisterMessage(prevMessage)
		remote_device_svc.SetDefault(prevDevice)
		project_repo.RegisterProject(prevProject)
		transcript_repo.RegisterFrameSeq(prevFrameSeq)
		ctrl.Finish()
	})
	return deps
}

// convID 是这些用例里本机第 n 条会话**落库的那个** conversation_id。取值形态无所谓
// (库里存什么就是什么),这里沿用一个确定性派生,只是为了让「同一条会话」在用例的
// 多处写出同一个字面值。
const testDesktopFingerprint devicefp.Carrier = "sha256:desktop"

func convID(n int64) string {
	return conversationid.Derive(conversationid.Namespace, string(testDesktopFingerprint), strconv.FormatInt(n, 10))
}

func agentForPeerSession() *agent_entity.Agent {
	return &agent_entity.Agent{ID: 7, AgentBackendID: 11}
}

type peerNotification struct {
	method string
	params any
}

type peerRecordingSubscriber struct {
	mu      sync.Mutex
	done    chan struct{}
	records []peerNotification
}

func newRecordingPeerSubscriber() *peerRecordingSubscriber {
	return &peerRecordingSubscriber{done: make(chan struct{})}
}

func (s *peerRecordingSubscriber) Notify(method string, params any) error {
	s.mu.Lock()
	s.records = append(s.records, peerNotification{method: method, params: params})
	s.mu.Unlock()
	return nil
}

func (s *peerRecordingSubscriber) Done() <-chan struct{} { return s.done }
func (s *peerRecordingSubscriber) notifications() []peerNotification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]peerNotification(nil), s.records...)
}
