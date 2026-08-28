package agentskill

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// TestMergeCatalog 是「推荐 + 已装 + 这一档的授权 → 目录」这件事的唯一实现。
// 它从 skill_svc 搬到这里,是因为执行端(agentred)也要答同一份目录,而 agentred
// 里没有 service 层 —— 两边各写一份 merge 就是两份会各自漂开的真相。
func TestMergeCatalog(t *testing.T) {
	recommended := []SkillPack{
		{ID: "superpowers@official", Name: "superpowers", Recommended: true, Source: SourceRecommended},
		{ID: "dataviz@official", Name: "dataviz", Recommended: true, Source: SourceRecommended},
	}
	installed := []SkillPack{
		{ID: "superpowers@official", Name: "superpowers", Installed: true, GloballyEnabled: true, Source: SourceInstalled},
		{ID: "local@mine", Name: "local", Installed: true, Source: SourceInstalled},
	}

	Convey("已装的排在推荐之前,同 id 合并成一行并同时带上两个旗标", t, func() {
		got := MergeCatalog(recommended, installed, nil)
		ids := make([]string, 0, len(got))
		for _, e := range got {
			ids = append(ids, e.Pack.ID)
		}
		So(ids, ShouldResemble, []string{"superpowers@official", "local@mine", "dataviz@official"})
		So(got[0].Pack.Installed, ShouldBeTrue)
		So(got[0].Pack.Recommended, ShouldBeTrue)
		So(got[0].Pack.Source, ShouldEqual, SourceInstalled)
	})

	Convey("没有授权时 Enabled 全为假,生效态继承全局", t, func() {
		got := MergeCatalog(recommended, installed, nil)
		So(got[0].Enabled, ShouldBeFalse)
		So(got[0].EffectiveEnabled, ShouldBeTrue) // 已装 + 全局开 → 继承为开
		So(got[1].EffectiveEnabled, ShouldBeFalse)
	})

	Convey("显式授权覆盖全局:强制关能把全局开着的包关掉", t, func() {
		got := MergeCatalog(recommended, installed, []agent_entity.AgentSkillItem{
			{ID: "superpowers@official", Enabled: false},
			{ID: "local@mine", Enabled: true},
		})
		So(got[0].Enabled, ShouldBeFalse)
		So(got[0].EffectiveEnabled, ShouldBeFalse)
		So(got[1].Enabled, ShouldBeTrue)
		So(got[1].EffectiveEnabled, ShouldBeTrue)
	})

	Convey("没装的包再怎么授权也不会生效 —— 得先装", t, func() {
		got := MergeCatalog(recommended, installed, []agent_entity.AgentSkillItem{
			{ID: "dataviz@official", Enabled: true},
		})
		So(got[2].Pack.ID, ShouldEqual, "dataviz@official")
		So(got[2].Enabled, ShouldBeTrue)
		So(got[2].EffectiveEnabled, ShouldBeFalse)
	})
}
