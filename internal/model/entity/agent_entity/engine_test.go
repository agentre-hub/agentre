package agent_entity

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func TestAgent_ExecConstraint(t *testing.T) {
	ctx := context.Background()

	t.Run("Given a fresh row (column default) When read Then it means any device", func(t *testing.T) {
		for _, raw := range []string{"", "{}"} {
			a := &Agent{ExecConstraintJSON: raw}
			assert.Equal(t, ExecConstraint{Mode: ExecConstraintAny}, a.GetExecConstraint(), "raw=%q", raw)
		}
	})

	t.Run("Given a labels constraint When set Then labels are trimmed, de-duplicated and read back", func(t *testing.T) {
		a := &Agent{}
		require.NoError(t, a.SetExecConstraint(ctx, ExecConstraint{Mode: ExecConstraintLabels, Labels: []string{" gpu ", "gpu", "linux"}}))
		assert.Equal(t, ExecConstraint{Mode: ExecConstraintLabels, Labels: []string{"gpu", "linux"}}, a.GetExecConstraint())
	})

	t.Run("Given a devices constraint When set Then the device list survives a round trip", func(t *testing.T) {
		a := &Agent{}
		devices := []devicefp.Carrier{"sha256:aa", "sha256:bb", "sha256:aa"}
		require.NoError(t, a.SetExecConstraint(ctx, ExecConstraint{Mode: ExecConstraintDevices, Devices: devices}))
		assert.Equal(t, ExecConstraint{Mode: ExecConstraintDevices, Devices: []devicefp.Carrier{"sha256:aa", "sha256:bb"}}, a.GetExecConstraint())
	})

	t.Run("Given any device with leftover labels When set Then the leftovers are dropped", func(t *testing.T) {
		a := &Agent{}
		require.NoError(t, a.SetExecConstraint(ctx, ExecConstraint{Mode: ExecConstraintAny, Labels: []string{"gpu"}}))
		assert.Equal(t, ExecConstraint{Mode: ExecConstraintAny}, a.GetExecConstraint())
	})

	for name, bad := range map[string]ExecConstraint{
		"unknown mode":           {Mode: "somewhere"},
		"labels without a label": {Mode: ExecConstraintLabels, Labels: []string{"  "}},
		"devices without device": {Mode: ExecConstraintDevices},
	} {
		t.Run("Given "+name+" When set Then it is rejected and the stored value is unchanged", func(t *testing.T) {
			a := &Agent{ExecConstraintJSON: `{"mode":"labels","labels":["keep"]}`}
			err := a.SetExecConstraint(ctx, bad)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.EngineExecConstraintInvalid, httpErr.Code)
			assert.Equal(t, ExecConstraint{Mode: ExecConstraintLabels, Labels: []string{"keep"}}, a.GetExecConstraint())
		})
	}
}

func TestAgent_SkillGrants(t *testing.T) {
	grants := []AgentSkillItem{{ID: "superpowers@claude-plugins-official", Enabled: true}, {ID: "opsctl@opskat", Enabled: false}}

	t.Run("Given grants made under claudecode When the effective runtime is claudecode Then all are valid", func(t *testing.T) {
		a := &Agent{}
		a.SetSkillGrants("claudecode", grants)
		valid, invalid := a.SkillGrantsFor("claudecode")
		assert.Equal(t, grants, valid)
		assert.Empty(t, invalid)
	})

	t.Run("Given grants made under claudecode When the effective runtime becomes codex Then all are reported invalid", func(t *testing.T) {
		a := &Agent{}
		a.SetSkillGrants("claudecode", grants)
		valid, invalid := a.SkillGrantsFor("codex")
		assert.Empty(t, valid)
		assert.Equal(t, grants, invalid)
	})

	t.Run("Given no grants When any runtime asks Then nothing is valid or invalid", func(t *testing.T) {
		a := &Agent{SkillGrantsJSON: "[]"}
		valid, invalid := a.SkillGrantsFor("codex")
		assert.Empty(t, valid)
		assert.Empty(t, invalid)
	})

	t.Run("Given a corrupt grants column When read Then it degrades to no grants", func(t *testing.T) {
		a := &Agent{SkillGrantsJSON: "not json", SkillGrantsRuntime: "claudecode"}
		assert.Equal(t, []AgentSkillItem{}, a.GetSkillGrants())
	})
}
