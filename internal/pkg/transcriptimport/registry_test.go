package transcriptimport

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

type fakeSource struct {
	backend agent_backend_entity.BackendType
}

func (f fakeSource) Backend() agent_backend_entity.BackendType { return f.backend }

func (f fakeSource) Scan(context.Context, Filter) ([]Candidate, error) { return nil, nil }

func (f fakeSource) Open(context.Context, Locator) (Transcript, error) { return nil, nil }

// TestRegistry 消费方只依赖注册表,不引用任何后端的具体构造器。
func TestRegistry(t *testing.T) {
	Convey("Given 一个注册进来的 source", t, func() {
		restore := SwapSourceForTest(agent_backend_entity.TypeBuiltin, fakeSource{backend: agent_backend_entity.TypeBuiltin})
		defer restore()

		Convey("When 按后端类型查询, Then 拿到同一个实例", func() {
			src := SourceFor(agent_backend_entity.TypeBuiltin)
			So(src, ShouldNotBeNil)
			So(src.Backend(), ShouldEqual, agent_backend_entity.TypeBuiltin)
		})

		Convey("When 列举全部, Then 快照里含它", func() {
			var found bool
			for _, s := range Sources() {
				if s.Backend() == agent_backend_entity.TypeBuiltin {
					found = true
				}
			}
			So(found, ShouldBeTrue)
		})
	})

	Convey("Given 没注册过的后端, When 查询, Then 返回 nil 而不是 panic", t, func() {
		So(SourceFor(agent_backend_entity.BackendType("nope")), ShouldBeNil)
	})
}

// TestFilter_Matches 钉住扫描期筛选的口径。它住在契约这一侧,三个读取器都调它 ——
// 各抄一份的话,某一家把标题匹配写成大小写敏感、或者把零值 Since 当成"1970 之后"
// (于是什么都过得了)时,表现只是那一家的列表少了或多了几行,没有任何一处会报错。
func TestFilter_Matches(t *testing.T) {
	at := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	cand := Candidate{
		Title:   "浇水 Plan",
		Cwd:     "/tmp/garden/plot",
		EndedAt: at,
	}

	cases := []struct {
		name   string
		filter Filter
		want   bool
	}{
		{"零值不过滤", Filter{}, true},
		{"Limit 不参与逐条判定", Filter{Limit: 1}, true},
		{"目录前缀命中", Filter{CwdPrefix: "/tmp/garden"}, true},
		{"目录前缀不命中", Filter{CwdPrefix: "/tmp/other"}, false},
		{"标题大小写不敏感", Filter{TitleQuery: "plan"}, true},
		{"标题不命中", Filter{TitleQuery: "施肥"}, false},
		{"末次活动不早于 Since", Filter{Since: at}, true},
		{"末次活动早于 Since", Filter{Since: at.Add(time.Second)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.filter.Matches(cand); got != c.want {
				t.Fatalf("Matches = %v, 期望 %v", got, c.want)
			}
		})
	}
}
