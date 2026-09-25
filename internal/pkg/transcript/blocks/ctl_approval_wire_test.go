package blocks

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestCtlResultText(t *testing.T) {
	change := func(op agentrewire.CtlOp) *agentrewire.CtlChange {
		return &agentrewire.CtlChange{Op: op, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12, Name: "reviewer"}
	}
	assert.Equal(t, "已创建 agent reviewer（id 12）", CtlResultText(change(agentrewire.CtlOp_CTL_OP_CREATE)))
	assert.Equal(t, "已更新 agent reviewer", CtlResultText(change(agentrewire.CtlOp_CTL_OP_UPDATE)))
	assert.Equal(t, "已删除 agent reviewer", CtlResultText(change(agentrewire.CtlOp_CTL_OP_DELETE)))
	assert.Equal(t, "", CtlResultText(nil))
}

func TestRedactCtlCommand_GivenSecretInCommandThenMasked(t *testing.T) {
	req := &agentrewire.CtlWriteRequest{
		Command:  "agrctl update provider x --api-key=sk-live-123 --token=gw-456",
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{ApiKey: "sk-live-123"}}},
	}
	assert.Equal(t, "agrctl update provider x --api-key=… --token=gw-456", RedactCtlCommand(req))
	req.Resource = &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Token: "gw-456"}}}
	assert.Equal(t, "agrctl update provider x --api-key=sk-live-123 --token=…", RedactCtlCommand(req))
}

func TestNewCtlApprovalChange(t *testing.T) {
	after := "qa"
	got := NewCtlApprovalChange(&agentrewire.CtlChange{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_AGENT, Id: 12, Name: "reviewer",
		Fields: []*agentrewire.CtlFieldChange{{Field: "departmentId", After: &after}, {Field: "token", Secret: true}},
	}, nil)
	assert.Equal(t, CtlApprovalChange{Op: "update", Kind: "agent", ID: 12, Name: "reviewer", Fields: []CtlApprovalField{
		{Field: "departmentId", After: &after}, {Field: "token", Secret: true},
	}}, got)
}
