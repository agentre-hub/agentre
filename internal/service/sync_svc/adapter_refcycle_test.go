package sync_svc

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// allRefFieldsPayload 把每种载荷用到的**全部**引用字段名塞进同一个 JSON 对象。
// 每个适配器的 refs 只反序列化自己认得的那几个键，所以一份公共载荷就能把每种
// 对象类型的引用一次性全部激活。
func allRefFieldsPayload(t *testing.T) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"parent_sync_id":        "other-1",
		"lead_agent_sync_id":    "other-2",
		"department_sync_id":    "other-3",
		"parent_agent_sync_id":  "other-4",
		"agent_sync_id":         "other-5",
		"backend_sync_id":       "other-6",
		"project_sync_id":       "other-7",
		"agent_backend_sync_id": "other-8",
		"issue_sync_id":         "other-9",
		"label_sync_id":         "other-10",
	})
	require.NoError(t, err)
	return body
}

// TestAdapterRefs_GivenEveryKind_ThenTheKindGraphHasNoCycle 是一条守卫：**两种对象
// 类型不得互相把对方当作阻塞引用**。
//
// resolveRefs 把任何一个解析不出的非空引用变成 errRefMissing，applyInbound 据此把
// 整行挂进入站队列暂缓落地（R2a）。replayDeferred 只在「有一条落地成功」时才再来
// 一轮，因此当 A 的落地等 B、B 的落地又等 A 时，两边永远都不会有第一条落地成功——
// 八轮重放全部空转，两行一起在队列里躺满 30 天，然后被 gcDeferred 当成「引用丢失」
// 丢掉。整个过程没有任何错误：SyncOnce 返回 nil。
//
// 这不是理论风险：department.lead_agent_sync_id 与 agent.department_sync_id 构成的
// 正是这样一个二元环，而 department_svc.Update 要求部门负责人必须是该部门成员，
// 所以「部门有负责人」的库里这两行必然互指——接收端因此一个部门都收不到，连带
// 收不到部门里的每一个 Agent、它们的执行目标与项目成员关系。
//
// 自环（部门的父部门、Agent 的上级 Agent）不在此列：父行是先被创建的另一行，
// 按版本升序下行时必然先到，不构成互等。
func TestAdapterRefs_GivenEveryKind_ThenTheKindGraphHasNoCycle(t *testing.T) {
	payload := allRefFieldsPayload(t)
	adapters := defaultAdapters(nil)

	// edges[kind] = 这种对象类型阻塞等待的其它对象类型。
	edges := map[string][]string{}
	for kind, ad := range adapters {
		in := &inbound{
			Kind:                kind,
			SyncID:              "self",
			ProjectSyncID:       "other-7",
			AgentredFingerprint: "fp-somewhere",
			Payload:             payload,
		}
		for _, r := range ad.refs(in) {
			// 指纹引用不是对象类型之间的依赖（它等的是本机配对表，不等另一条下行）。
			if r.Kind == "" || r.SyncID == "" || r.Kind == kind {
				continue
			}
			edges[kind] = append(edges[kind], r.Kind)
		}
	}

	for _, list := range edges {
		sort.Strings(list)
	}

	if cycle := findKindCycle(edges); cycle != nil {
		t.Fatalf("对象类型之间存在互等的阻塞引用，两边都永远落不了地：%v\n"+
			"（完整依赖边：%v）", cycle, edges)
	}
}

// findKindCycle 在依赖图上做一次深度优先，返回找到的第一个环（含闭合节点）。
func findKindCycle(edges map[string][]string) []string {
	const (
		white = 0 // 未访问
		grey  = 1 // 在当前递归栈上
		black = 2 // 已完成
	)
	color := map[string]int{}
	var stack []string
	var walk func(node string) []string
	walk = func(node string) []string {
		color[node] = grey
		stack = append(stack, node)
		for _, next := range edges[node] {
			switch color[next] {
			case grey:
				// 回边：从栈里 next 第一次出现的位置起就是环。
				for i, n := range stack {
					if n == next {
						return append(append([]string{}, stack[i:]...), next)
					}
				}
			case white:
				if c := walk(next); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[node] = black
		return nil
	}

	// 固定遍历次序，让失败信息可复现。
	kinds := make([]string, 0, len(edges))
	for k := range edges {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		if color[k] == white {
			if c := walk(k); c != nil {
				return c
			}
		}
	}
	return nil
}

// TestDepartmentAdapter_GivenLeadAgentNotArrived_ThenDepartmentStillLands 是上面那条
// 守卫的具体一半：部门的落地不能被负责人卡住。负责人是这个部门的成员，它自己的
// 落地正等着这个部门，谁都不先落地就是死锁。
func TestDepartmentAdapter_GivenLeadAgentNotArrived_ThenDepartmentStillLands(t *testing.T) {
	in := &inbound{
		Kind:    syncwire.KindDepartment,
		SyncID:  "dep-1",
		Payload: allRefFieldsPayload(t),
	}
	for _, r := range (departmentAdapter{}).refs(in) {
		require.NotEqual(t, syncwire.KindAgent, r.Kind,
			"部门不能把负责人当成阻塞引用：负责人的落地本身在等这个部门")
	}
}
