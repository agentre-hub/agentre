package accountpeer

import (
	"context"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// Agentred is a scripted fake daemon behind the relay. Its backend is deterministic: a
// task containing "ask:" makes the turn stop on a question (ask-1, options Vanilla and
// Chocolate); any other task finishes at once. Answering the question finishes the turn
// with "You chose <label>.".
type Agentred struct {
	mu       sync.Mutex
	sessions map[string]*session
	seq      int64
	answers  []string
}

type session struct {
	summary *agentrewire.SessionSummary
	pending *agentrewire.SessionPendingWaitersResponse
	notes   []*agentrewire.DurableNotification
}

func newAgentred() *Agentred { return &Agentred{sessions: map[string]*session{}} }

// AnsweredLabels are the option labels submitted through runtime.submitAnswer.
func (a *Agentred) AnsweredLabels() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.answers...)
}

// add appends one durable frame; the caller holds a.mu.
func (a *Agentred) add(s *session, n *agentrewire.RpcNotification) {
	a.seq++
	s.notes = append(s.notes, &agentrewire.DurableNotification{Seq: a.seq, Payload: n})
	s.summary.LatestSeq = a.seq
}

func (a *Agentred) event(s *session, id string, ev *agentrewire.RuntimeEventNotification) {
	ev.ConversationId = id
	a.add(s, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: ev}})
}

func (a *Agentred) finish(s *session, id, text string) {
	a.event(s, id, &agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: text}}})
	a.add(s, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{ConversationId: id}}})
	s.summary.LifecycleState = "idle"
	s.summary.WaitingForInput = false
	s.pending = nil
}

func (a *Agentred) registry() *protorpc.Registry {
	reg := protorpc.NewRegistry()
	m := func(method agentrewire.RpcMethod) uint32 { return uint32(method) }

	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN),
		func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			id := req.GetConversationId()
			a.mu.Lock()
			defer a.mu.Unlock()
			s := a.sessions[id]
			if s == nil {
				origin := req.GetPeerFingerprint()
				if c := protorpc.ConnFromContext(ctx); origin == "" && c != nil {
					origin = c.Auth().DeviceFingerprint
				}
				s = &session{summary: &agentrewire.SessionSummary{ConversationId: id, PeerFingerprint: origin,
					AgentSyncId: req.GetAgentSyncId(), Title: req.GetTitle(), BackendType: req.GetBackend().GetType()}}
				a.sessions[id] = s
			} else if s.summary.GetLifecycleState() == "running" {
				return nil, &rpcerror.Error{Code: rpcerror.CodeRuntimeSessionRunning, Message: "runtime: conversation is running a turn"}
			}
			s.summary.LifecycleState = "running"
			a.event(s, id, &agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_UserMessage{UserMessage: &agentrewire.UserMessage{Text: req.GetUserText()}}})
			if strings.Contains(req.GetUserText(), "ask:") {
				s.summary.WaitingForInput = true
				s.pending = &agentrewire.SessionPendingWaitersResponse{AskUserQuestions: []*agentrewire.PendingAskUserQuestion{{
					RequestId: "ask-1", Questions: []*agentrewire.AskQuestion{{Question: "Which flavor?",
						Options: []*agentrewire.AskOption{{Label: "Vanilla"}, {Label: "Chocolate"}}}}}}}
			} else {
				a.finish(s, id, "Done.")
			}
			return &agentrewire.RuntimeRunResponse{ConversationId: id}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER),
		func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} },
		func(_ context.Context, req *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			s := a.sessions[req.GetConversationId()]
			if s == nil || s.pending == nil || len(s.pending.GetAskUserQuestions()) == 0 || s.pending.GetAskUserQuestions()[0].GetRequestId() != req.GetRequestId() {
				return nil, &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: "no pending question " + req.GetRequestId()}
			}
			labels := req.GetAnswers()[0].GetLabels()
			a.answers = append(a.answers, labels...)
			a.finish(s, req.GetConversationId(), "You chose "+strings.Join(labels, ", ")+".")
			return &agentrewire.PeerSessionControlResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
		func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} },
		func(_ context.Context, req *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			out := &agentrewire.SessionListResponse{}
			for id, s := range a.sessions {
				if want := req.GetConversationIds(); len(want) > 0 && !contains(want, id) {
					continue
				}
				out.Sessions = append(out.Sessions, proto.Clone(s.summary).(*agentrewire.SessionSummary))
			}
			return out, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL),
		func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} },
		func(_ context.Context, req *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			out := &agentrewire.SessionPullResponse{Cursor: req.GetCursor()}
			s := a.sessions[req.GetConversationId()]
			if s == nil {
				return out, nil
			}
			for _, n := range s.notes {
				if n.GetSeq() <= req.GetCursor() {
					continue
				}
				if limit := int(req.GetLimit()); limit > 0 && len(out.Notifications) >= limit {
					out.HasMore = true
					break
				}
				out.Notifications = append(out.Notifications, n)
				out.Cursor = n.GetSeq()
			}
			return out, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS),
		func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} },
		func(_ context.Context, req *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if s := a.sessions[req.GetConversationId()]; s != nil && s.pending != nil {
				return s.pending, nil
			}
			return &agentrewire.SessionPendingWaitersResponse{}, nil
		})
	return reg
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
