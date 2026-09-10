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
		{file: "turn_run.go", name: "consumeEvents"},
		{file: "autonomous_turn_run.go", name: "consumeEvents"},
		{file: "subagent_activity.go", name: "driveSubagentActivity"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			source, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, source, 0)
			require.NoError(t, err)

			var eventLoop *ast.RangeStmt
			ast.Inspect(file, func(node ast.Node) bool {
				decl, ok := node.(*ast.FuncDecl)
				if !ok || decl.Name.Name != tc.name {
					return true
				}
				ast.Inspect(decl.Body, func(inner ast.Node) bool {
					rangeStmt, ok := inner.(*ast.RangeStmt)
					if !ok || eventLoop != nil {
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
						eventLoop = rangeStmt
					}
					return true
				})
				return false
			})
			require.NotNil(t, eventLoop, "must retain a canonical event range loop")

			var tee, apply token.Pos
			ast.Inspect(eventLoop.Body, func(node ast.Node) bool {
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
