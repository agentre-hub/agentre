package ctlcmd

import "strings"

// flagDef 是一个 flag 的定义。它同时是解析器的输入和 help 的数据源，help 因此不会
// 与实际接受的 flag 分家。
type flagDef struct {
	name  string
	value string // help 里的值占位，如 "<name>"；空串 = 布尔 flag
	usage string
	// field 是写入资源文档的字段 JSON 名，会进 CtlWriteRequest.fields；空串 = 不写字段。
	field  string
	repeat bool
	// secret 为 true：裸写时在终端无回显读取，写成 --flag=<值> 时直接取值。
	secret bool
	// notEmpty 为 true：显式写空值（--flag=）是用法错误，在连接执行者之前就判。
	notEmpty bool
	// check 在读取密钥之前做与值无关的校验（例如该 flag 是否适用于这个后端类型）。
	check func(w *writeCtx) error
	// apply 把一次出现写进请求。
	apply func(w *writeCtx, v string) error
}

// secretFlagNames 是所有密钥 flag 的名字；命令行回显时它们的值一律替换为 …。
var secretFlagNames = []string{"api-key", "token"}

// occurrence 是一个 flag 在命令行上的一次出现。
type occurrence struct {
	def *flagDef
	// value 是 flag 的值；布尔 flag 裸写时为 "true"。
	value string
	// bare 表示密钥 flag 没有带 =<值>，需要从终端读取。
	bare bool
	// at 是值所在的参数下标（相对 parseArgs 的输入）；歧义提示据此只替换这一处。
	at int
}

// parsedArgs 是解析后的命令行：位置参数与按出现顺序排列的 flag。
type parsedArgs struct {
	positional []string
	// positionalAt 是每个位置参数的下标（相对 parseArgs 的输入）。
	positionalAt []int
	flags        []occurrence
}

// has 报告名为 name 的 flag 是否出现过。
func (p parsedArgs) has(name string) bool {
	return len(p.values(name)) > 0
}

// on 报告布尔 flag name 是否打开：出现过且值不是 =false（--f 与 --f=true 打开）。
func (p parsedArgs) on(name string) bool {
	vals := p.values(name)
	return len(vals) > 0 && vals[len(vals)-1] == "true"
}

// values 返回名为 name 的 flag 的全部取值，按出现顺序。
func (p parsedArgs) values(name string) []string {
	var out []string
	for _, o := range p.flags {
		if o.def.name == name {
			out = append(out, o.value)
		}
	}
	return out
}

// parseArgs 解析 flag 与位置参数，二者可以交错出现；`--` 之后全是位置参数。
//
// 值 flag 接受 `--f v` 与 `--f=v`；布尔 flag 接受 `--f` 与 `--f=true|false`；
// 密钥 flag 只接受裸写与 `--f=v` —— `--f v` 里的 v 会成为多余的位置参数而被拒绝，
// 以免把密钥当成定位参数误用。未知 flag、重复的非 repeat flag 都是用法错误。
func parseArgs(args []string, defs []*flagDef) (parsedArgs, error) {
	var p parsedArgs
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			for j := i + 1; j < len(args); j++ {
				p.positional, p.positionalAt = append(p.positional, args[j]), append(p.positionalAt, j)
			}
			break
		}
		if len(a) < 2 || a[0] != '-' {
			p.positional, p.positionalAt = append(p.positional, a), append(p.positionalAt, i)
			continue
		}
		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, value, hasValue = name[:eq], name[eq+1:], true
		}
		def := findDef(defs, name)
		if def == nil {
			return p, usageErrorf("unknown flag --%s", name)
		}
		if seen[def.name] && !def.repeat {
			return p, usageErrorf("flag --%s given more than once", def.name)
		}
		seen[def.name] = true
		occ := occurrence{def: def, at: i}
		switch {
		case def.secret:
			occ.value, occ.bare = value, !hasValue
		case def.value == "":
			occ.value = "true"
			if hasValue {
				if value != "true" && value != "false" {
					return p, usageErrorf("flag --%s takes no value (or =true/=false)", def.name)
				}
				occ.value = value
			}
		case hasValue:
			occ.value = value
		default:
			if i+1 >= len(args) {
				return p, usageErrorf("flag --%s needs a value", def.name)
			}
			i++
			occ.value, occ.at = args[i], i
		}
		p.flags = append(p.flags, occ)
	}
	return p, nil
}

func findDef(defs []*flagDef, name string) *flagDef {
	for _, d := range defs {
		if d.name == name {
			return d
		}
	}
	return nil
}

// outputFlag 是 list / get 共有的 -o/--output。
var (
	outputFlag      = &flagDef{name: "o", value: "json", usage: "print JSON instead of a table"}
	outputFlagLong  = &flagDef{name: "output", value: "json", usage: "same as -o"}
	outputFlagNames = []*flagDef{outputFlag, outputFlagLong}
)

// wantJSON 读 -o/--output；只接受 json。
func wantJSON(p parsedArgs) (bool, error) {
	vals := append(p.values("o"), p.values("output")...)
	if len(vals) == 0 {
		return false, nil
	}
	if len(vals) > 1 || vals[0] != "json" {
		return false, usageErrorf("unsupported output format %q (only -o json)", strings.Join(vals, ","))
	}
	return true, nil
}
