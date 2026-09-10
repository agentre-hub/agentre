package rpcerror_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/turnstate"
)

// 段位划分:每个方法族占一段连续的错误码,段与段之间不重叠。
//
// 这张表原本散落在 internal/pkg/workspacefs/wire 与 agentruntime remote wire 的
// 包注释里 —— 各写一份,各自只数得清自己看得见的那几族。码全部搬进本包之后,
// 它成为唯一一份,下面的守卫才能真的覆盖全段位。
//
// **加新码前先在这里给它的族划一段**:守卫要求本包每个 Code* 常量都落在某一段
// 里,没段可落就变红,逼着新族先拿到自己的段位而不是随手挑一个空号。
var codeSegments = []struct {
	family   string
	min, max int32 // 闭区间
}{
	{"json-rpc 标准码", -32700, -32600},
	{"protorpc 取消", -32800, -32800},
	{"daemon 会话/鉴权", -32006, -32001},
	{"runtime.*", -32015, -32010},
	{"remotefs.*", -32035, -32030},
	{"workspacefs.*", -32043, -32040},
	{"project.*", -32052, -32050},
	{"transcriptImport.*", -32062, -32060},
	{"portforward.*", -32075, -32070},
}

// TestCodeSegments_GivenTwoFamiliesOnOneConnection_WhenAFamilyAllocatesANewCode_ThenItCannotReuseAnotherFamilysNumber
// 是撞号守卫。
//
// 同一条连接上跑着好几个方法族,而客户端只拿得到一个数字:两个族用同一个码,
// 客户端就会把别人的失败 rehydrate 成自己的 sentinel。
//
// 它**扫本包源码**(AST)而不是照抄一张常量清单来比对:抄下来的清单不会随新增
// 的码更新,那样守卫就只在写它的那天成立 —— 搬家之前正是这个形态,workspacefs
// 那条守卫只对得上 remotefs 一族,agentruntime 与 project 的码它根本看不见。
// 码全部住进本包之后,「新加一个码」这条路径必然经过这里,守卫因此是**构造上
// 完备**的:不可能有一个本包的 Code* 常量逃过下面两条断言。
func TestCodeSegments_GivenTwoFamiliesOnOneConnection_WhenAFamilyAllocatesANewCode_ThenItCannotReuseAnotherFamilysNumber(t *testing.T) {
	t.Parallel()

	// 段位之间自己先不能重叠 —— 段表错了,下面按段归属的判定就没有意义。
	for i, a := range codeSegments {
		require.LessOrEqual(t, a.min, a.max, "%s 段位写反了", a.family)
		for _, b := range codeSegments[i+1:] {
			require.Falsef(t, a.min <= b.max && b.min <= a.max,
				"段位 %s [%d,%d] 与 %s [%d,%d] 重叠", a.family, a.min, a.max, b.family, b.min, b.max)
		}
	}

	codes := parseExportedCodes(t)
	require.NotEmpty(t, codes, "AST 没扫到任何 Code* 常量,守卫等于没跑")

	byValue := map[int32]string{}
	for _, c := range codes {
		if prev, dup := byValue[c.value]; dup {
			t.Fatalf("错误码 %d 被 %s 与 %s 同时占用:两个方法族撞号,客户端会把别人的失败认成自己的",
				c.value, prev, c.name)
		}
		byValue[c.value] = c.name

		// 段与段不重叠已在上面断言过,所以第一个命中的就是唯一的归属。
		var owner string
		for _, seg := range codeSegments {
			if c.value >= seg.min && c.value <= seg.max {
				owner = seg.family
				break
			}
		}
		require.NotEmptyf(t, owner,
			"%s = %d 不在任何已划定的段位里:先在 codeSegments 里给它的方法族划一段",
			c.name, c.value)
	}
}

// TestAbortedCode_GivenTurnstateDeclaresItToo_WhenEitherMoves_ThenTheyAreCaught
// 把同一个数字的两处声明钉在一起。
//
// turnstate 判定「这一轮是不是故障收场」时要认出用户自己按的停止,但它不该为此
// 反向依赖本包(两个都是叶子,加边就多一条要维护的方向)。代价是 -32013 写了两遍,
// 由这条断言兜住 —— 生产依赖图不变,漂移照样变红。
func TestAbortedCode_GivenTurnstateDeclaresItToo_WhenEitherMoves_ThenTheyAreCaught(t *testing.T) {
	t.Parallel()

	require.EqualValues(t, rpcerror.CodeRuntimeAborted, turnstate.AbortedCode)
}

type exportedCode struct {
	name  string
	value int32
}

// parseExportedCodes 从本包的非测试源码里读出全部导出的 Code* 常量。
//
// 只认字面量(含负号)形式的赋值:错误码是过线的稳定数字,写成表达式或引用别处的
// 常量会让「这个码到底是几」需要跑一遍才知道,而这正是守卫要机械回答的问题。
func parseExportedCodes(t *testing.T) []exportedCode {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	require.NotEmpty(t, names, "本包没有非测试源码?")

	fset := token.NewFileSet()
	var out []exportedCode
	for _, name := range names {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if !id.IsExported() || !strings.HasPrefix(id.Name, "Code") || i >= len(vs.Values) {
						continue
					}
					v, ok := intLiteral(vs.Values[i])
					require.Truef(t, ok, "%s 必须写成字面量数字,好让守卫机械读出它的值", id.Name)
					out = append(out, exportedCode{name: id.Name, value: v})
				}
			}
		}
	}
	return out
}

// intLiteral 读一条 `-32010` / `32010` 形态的表达式;形态不符返回 false。
func intLiteral(expr ast.Expr) (int32, bool) {
	neg := false
	if u, ok := expr.(*ast.UnaryExpr); ok {
		if u.Op != token.SUB {
			return 0, false
		}
		neg, expr = true, u.X
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.ParseInt(lit.Value, 0, 32)
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return int32(n), true
}
