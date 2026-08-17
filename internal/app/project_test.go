package app

import (
	"testing"

	"github.com/agentre-ai/agentre/internal/model/entity/project_entity"
	"github.com/agentre-ai/agentre/internal/service/project_svc"
)

func TestProjectMemberItemsIncludeAgentDisplayFields(t *testing.T) {
	t.Parallel()

	got := toProjectMembers([]*project_svc.ProjectAgentMember{
		{
			AgentID:       5,
			JoinedAt:      100,
			FromProjectID: 1,
			FromName:      "Parent",
			AgentName:     "Builder",
			AvatarColor:   "agent-2",
			AvatarIcon:    "hammer",
			AvatarDataURL: "data:image/png;base64,Yg==",
		},
	}, true)

	if len(got) != 1 {
		t.Fatalf("expected one project member item, got %d", len(got))
	}
	if got[0].AgentName != "Builder" {
		t.Fatalf("expected agentName to be mapped, got %q", got[0].AgentName)
	}
	if got[0].AvatarColor != "agent-2" || got[0].AvatarIcon != "hammer" || got[0].AvatarDataURL == "" {
		t.Fatalf("expected avatar fields to be mapped, got %#v", got[0])
	}
	if !got[0].Inherited {
		t.Fatal("expected inherited flag to be preserved")
	}
}

// R10：项目树的「未配置」角标与基本页签的「指定…」入口都要靠这个字段驱动，
// toProjectItem 必须把 project_entity.Project.LocalPathMissing 透给前端 DTO。
func TestToProjectItemIncludesLocalPathMissing(t *testing.T) {
	t.Parallel()

	got := toProjectItem(&project_entity.Project{
		ID: 9, Name: "agentre-hub", LocalPathMissing: true,
	})

	if got == nil {
		t.Fatal("expected a project item")
	}
	if !got.LocalPathMissing {
		t.Fatal("expected localPathMissing to be mapped from the entity")
	}

	configured := toProjectItem(&project_entity.Project{
		ID: 10, Name: "agentre", Path: "/tmp/agentre", LocalPathMissing: false,
	})
	if configured.LocalPathMissing {
		t.Fatal("expected localPathMissing=false for a configured project")
	}
}
