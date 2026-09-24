package chat_svc_test

import (
	"context"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

type fakeExecApprovalRunner struct {
	gotSession  int64
	gotID       string
	gotDecision string
	result      agentruntime.ExecApprovalResolution
	err         error
}

func (f *fakeExecApprovalRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapExecApproval: true}}
}

func (f *fakeExecApprovalRunner) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event)
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

func (f *fakeExecApprovalRunner) ResolveExecApproval(_ context.Context, sessionID int64, approvalID, decision string) (agentruntime.ExecApprovalResolution, error) {
	f.gotSession, f.gotID, f.gotDecision = sessionID, approvalID, decision
	return f.result, f.err
}

func TestResolveExecApproval(t *testing.T) {
	m := setupChatTest(t)
	fake := &fakeExecApprovalRunner{result: agentruntime.ExecApprovalResolution{Status: "resolved", Decision: "allow-once"}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeOpenClaw, fake)
	defer restore()

	m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeOpenClaw), Status: consts.ACTIVE}, nil)

	response, err := m.svc.ResolveExecApproval(m.ctx, &chat_svc.ResolveExecApprovalRequest{
		SessionID: 42, ApprovalID: "approval-1", Decision: "allow-once",
	})
	assert.NoError(t, err)
	assert.Equal(t, "resolved", response.Status)
	assert.Equal(t, "allow-once", response.Decision)
	assert.Equal(t, int64(42), fake.gotSession)
	assert.Equal(t, "approval-1", fake.gotID)
	assert.Equal(t, "allow-once", fake.gotDecision)
}

func TestResolveExecApprovalRejectsIncompleteRequest(t *testing.T) {
	m := setupChatTest(t)
	_, err := m.svc.ResolveExecApproval(m.ctx, &chat_svc.ResolveExecApprovalRequest{SessionID: 42, ApprovalID: "approval-1"})
	assert.Error(t, err)
}

func TestResolveExecApprovalAcceptsAllowSession(t *testing.T) {
	m := setupChatTest(t)
	fake := &fakeExecApprovalRunner{result: agentruntime.ExecApprovalResolution{Status: "resolved", Decision: "allow-session"}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeOpenClaw, fake)
	defer restore()

	m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeOpenClaw), Status: consts.ACTIVE}, nil)

	response, err := m.svc.ResolveExecApproval(m.ctx, &chat_svc.ResolveExecApprovalRequest{
		SessionID: 42, ApprovalID: "approval-1", Decision: "allow-session",
	})
	assert.NoError(t, err)
	assert.Equal(t, "allow-session", response.Decision)
	assert.Equal(t, "allow-session", fake.gotDecision)
}

func TestResolveExecApprovalRejectsUnknownDecision(t *testing.T) {
	m := setupChatTest(t)
	_, err := m.svc.ResolveExecApproval(m.ctx, &chat_svc.ResolveExecApprovalRequest{
		SessionID: 42, ApprovalID: "approval-1", Decision: "allow-forever",
	})
	assert.Error(t, err)
}

func TestProjectBlocksCarriesApprovalKindAndContent(t *testing.T) {
	got := chat_svc.ProjectBlocks([]cagoblocks.ContentBlock{&transcriptblocks.ExecApprovalBlock{
		ID: "p-1", Kind: agentruntime.ApprovalKindPlugin, SessionKey: "agentre:12:42",
		CommandText: "cmd", Description: "d", ToolName: "create_issue", PluginName: "github",
		Warnings: []string{"w"}, ActionCategory: agentruntime.ApprovalActionPublish,
		MessageTargets: []string{"#general"}, RecipientCount: 4, PaymentAmount: "1 USD",
		PaymentPayee: "ACME", PublishTarget: "blog", PublishVisibility: "public", AutomationName: "nightly",
		AllowedDecisions: []string{"allow-once", "allow-always", "deny"}, Status: "pending",
	}})
	assert.Equal(t, []chat_svc.ChatBlock{{Type: chat_svc.ChatBlockTypeExecApproval, ExecApproval: &chat_svc.ChatBlockExecApproval{
		ID: "p-1", Kind: agentruntime.ApprovalKindPlugin,
		CommandText: "cmd", Description: "d", ToolName: "create_issue", PluginName: "github",
		Warnings: []string{"w"}, ActionCategory: agentruntime.ApprovalActionPublish,
		MessageTargets: []string{"#general"}, RecipientCount: 4, PaymentAmount: "1 USD",
		PaymentPayee: "ACME", PublishTarget: "blog", PublishVisibility: "public", AutomationName: "nightly",
		AllowedDecisions: []string{"allow-once", "allow-always", "deny"}, Status: "pending",
	}}}, got)
}
