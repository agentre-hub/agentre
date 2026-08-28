package agentskill

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

type fakeDisc struct{ packs []SkillPack }

func (f fakeDisc) Discover(context.Context, DiscoverQuery) ([]SkillPack, error) { return f.packs, nil }

func TestAgentSkill(t *testing.T) {
	Convey("recommended 非空且稳定", t, func() {
		r := Recommended()
		So(len(r), ShouldBeGreaterThan, 0)
		So(r[0].Recommended, ShouldBeTrue)
	})
	Convey("recommended 按 backend 隔离", t, func() {
		So(RecommendedFor(agent_backend_entity.TypeClaudeCode), ShouldResemble, Recommended())
		So(RecommendedFor(agent_backend_entity.TypeCodex), ShouldBeEmpty)
	})
	Convey("discoverer 注册/查询", t, func() {
		restore := SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fakeDisc{packs: []SkillPack{{ID: "x@y"}}})
		defer restore()
		d, ok := DiscovererFor(agent_backend_entity.TypeClaudeCode)
		So(ok, ShouldBeTrue)
		got, _ := d.Discover(context.Background(), DiscoverQuery{})
		So(got[0].ID, ShouldEqual, "x@y")
		_, ok2 := DiscovererFor(agent_backend_entity.TypeCodex)
		So(ok2, ShouldBeFalse)
	})
}
