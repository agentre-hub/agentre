package chat_svc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given any of the three canonical event loops, when it reduces an event for
// the local desktop UI, then the peer observer has already received that same
// original event; local dispatcher delivery remains an additional consumer.
func TestCanonicalEventLoops_GivenPeerSubscribers_ThenTeeBeforeLocalDispatcherApply(t *testing.T) {
	for _, tc := range []struct {
		file string
		name string
	}{
		// turn_run.go 的事件处理体拆到了 applyLive:远端执行那一路是两级帧,呈现
		// (预览帧)与转录(持久帧)走两条流,consumeEvents 只做分派。扇出仍须在
		// 本地 Apply 之前看到原始密封事件,守的还是这一条。
		{file: "turn_run.go", name: "applyLive"},
		// autonomous_turn_run.go 同理:远端执行的自主续轮也是两级帧,per-event 的
		// 处理体拆到了 applyLive,consumeEvents 只做分派。
		{file: "autonomous_turn_run.go", name: "applyLive"},
		{file: "subagent_activity.go", name: "driveSubagentActivity"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			source, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, source, 0)
			require.NoError(t, err)

			// 事件处理体:range 循环,或(拆成 per-event 函数之后)函数体本身。
			var eventLoop ast.Node
			var rangeLoop *ast.RangeStmt
			ast.Inspect(file, func(node ast.Node) bool {
				decl, ok := node.(*ast.FuncDecl)
				if !ok || decl.Name.Name != tc.name {
					return true
				}
				if eventLoop == nil {
					eventLoop = decl.Body
				}
				ast.Inspect(decl.Body, func(inner ast.Node) bool {
					rangeStmt, ok := inner.(*ast.RangeStmt)
					if !ok || rangeLoop != nil {
						return true
					}
					var hasApply bool
					ast.Inspect(rangeStmt.Body, func(candidate ast.Node) bool {
						call, ok := candidate.(*ast.CallExpr)
						if ok && selectorName(call) == "Apply" {
							hasApply = true
						}
						return true
					})
					if hasApply {
						rangeLoop, eventLoop = rangeStmt, rangeStmt
					}
					return true
				})
				return false
			})
			require.NotNil(t, eventLoop, "must retain a canonical event handling body")

			var tee, apply token.Pos
			ast.Inspect(eventLoop, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch selectorName(call) {
				case "publishPeerEvent":
					if tee == token.NoPos || call.Pos() < tee {
						tee = call.Pos()
					}
				case "Apply":
					if apply == token.NoPos || call.Pos() < apply {
						apply = call.Pos()
					}
				}
				return true
			})
			require.NotEqual(t, token.NoPos, tee, "every canonical loop must tee peer events")
			require.NotEqual(t, token.NoPos, apply, "the loop must keep local dispatcher delivery")
			assert.Less(t, tee, apply, "the tee must observe the canonical event before local Apply")
		})
	}
}

func selectorName(call *ast.CallExpr) string {
	selector, _ := call.Fun.(*ast.SelectorExpr)
	if selector == nil {
		return ""
	}
	return selector.Sel.Name
}
