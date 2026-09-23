package ctlcmd

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// writeCtx 是一次 create / update 在客户端组装请求时的状态。
type writeCtx struct {
	s    *sys
	cat  *catalog
	spec *kindSpec
	req  *agentrewire.CtlWriteRequest
	// doc 是 req.Resource，已按资源类型建好对应分支。
	doc *agentrewire.CtlResource
	// target 是 update 的目标（当前数据）；create 时为 nil。
	target *agentrewire.CtlResource
	// fieldOwner 记录每个字段由哪个 flag 写入，用来拒绝两个 flag 写同一字段。
	fieldOwner map[string]string
}

func (w *writeCtx) fieldSet(field string) bool {
	_, ok := w.fieldOwner[field]
	return ok
}

// ref 按名字 / 路径 / id 解析 flag 里对其它资源的引用；空串表示清空（id 0）。
func (w *writeCtx) ref(spec *kindSpec, v string) (int64, error) {
	if strings.TrimSpace(v) == "" {
		return 0, nil
	}
	it, err := w.cat.locate(spec, v)
	if err != nil {
		return 0, err
	}
	return docOf(it).id, nil
}

// secret 取密钥 flag 的值：带 = 的直接用；裸写则在终端无回显读取，没有终端就要人来。
func (w *writeCtx) secret(o occurrence) (string, error) {
	if !o.bare {
		return o.value, nil
	}
	if !w.s.stdinIsTTY {
		return "", needsTTY(o.def.name)
	}
	v, err := w.s.readSecret(secretPrompt(o.def.name))
	if err != nil {
		return "", fmt.Errorf("read --%s: %w", o.def.name, err)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", usageErrorf("--%s: no value entered", o.def.name)
	}
	return v, nil
}

func secretPrompt(flagName string) string {
	switch flagName {
	case "api-key":
		return "API key (input hidden): "
	default:
		return "Token (input hidden): "
	}
}

func newDoc(kind agentrewire.CtlKind) *agentrewire.CtlResource {
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		return agentDoc(&agentrewire.CtlAgent{})
	case agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Department{Department: &agentrewire.CtlDepartment{}}}
	case agentrewire.CtlKind_CTL_KIND_PROJECT:
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Project{Project: &agentrewire.CtlProject{}}}
	case agentrewire.CtlKind_CTL_KIND_PROVIDER:
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{}}}
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Model{Model: &agentrewire.CtlModel{}}}
	default:
		return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{}}}
	}
}

func agentDoc(a *agentrewire.CtlAgent) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Agent{Agent: a}}
}

// runWrite 处理 create / update / delete。args 含动词本身，用于还原命令行。
func runWrite(verb string, args []string, s *sys) error {
	rest := args[1:]
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return usageErrorf("%s needs a resource: %s", verb, kindNames())
	}
	spec := lookupKind(rest[0])
	if spec == nil {
		return usageErrorf("unknown resource %q (one of: %s)", rest[0], kindNames())
	}
	op := map[string]agentrewire.CtlOp{
		"create": agentrewire.CtlOp_CTL_OP_CREATE,
		"update": agentrewire.CtlOp_CTL_OP_UPDATE,
		"delete": agentrewire.CtlOp_CTL_OP_DELETE,
	}[verb]
	defs := spec.deleteFlags
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		defs = spec.createFlags
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		defs = spec.updateFlags
	}
	p, err := parseArgs(rest[1:], defs)
	if err != nil {
		return err
	}
	wantPos := 1
	if op == agentrewire.CtlOp_CTL_OP_CREATE {
		wantPos = 0
	}
	if len(p.positional) != wantPos {
		if wantPos == 0 {
			return usageErrorf("unexpected argument %q (create takes flags only; secret flags need --flag=<value>)", p.positional[0])
		}
		return usageErrorf("%s %s needs exactly one <id|name> (got %d arguments)", verb, spec.name, len(p.positional))
	}
	if op == agentrewire.CtlOp_CTL_OP_CREATE {
		for _, r := range spec.required {
			if !p.has(r) {
				return usageErrorf("create %s: --%s is required", spec.name, r)
			}
		}
	}
	if op == agentrewire.CtlOp_CTL_OP_UPDATE && len(p.flags) == 0 {
		return usageErrorf("update %s: nothing to update (see agrctl help %s)", spec.name, spec.name)
	}

	cat, err := connect(s, args)
	if err != nil {
		return err
	}
	w := &writeCtx{
		s: s, cat: cat, spec: spec, doc: newDoc(spec.kind), fieldOwner: map[string]string{},
		req: &agentrewire.CtlWriteRequest{Op: op, Kind: spec.kind, Caller: cat.caller(s), Command: commandLine(args)},
	}
	label := ""
	if op != agentrewire.CtlOp_CTL_OP_CREATE {
		w.target, err = cat.locate(spec, p.positional[0])
		if err != nil {
			return err
		}
		w.req.Id = docOf(w.target).id
		label = cat.label(spec.kind, w.req.Id)
	}
	if op == agentrewire.CtlOp_CTL_OP_DELETE {
		w.req.Cascade, w.req.Force = p.has("cascade"), p.has("force")
		if err := confirmDelete(s, w.req.Caller, spec, label); err != nil {
			return err
		}
	} else {
		if err := w.applyFlags(defs, p); err != nil {
			return err
		}
		w.req.Resource = w.doc
	}

	switch w.req.Caller {
	case agentrewire.CtlCaller_CTL_CALLER_SESSION:
		_, _ = fmt.Fprintln(s.stderr, "waiting for approval in this Agentre session …")
	case agentrewire.CtlCaller_CTL_CALLER_EXTERNAL:
		_, _ = fmt.Fprintln(s.stderr, "waiting for approval in the Agentre desktop …")
	}
	resp, err := cat.write(w.req)
	if err != nil {
		return err
	}
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		_, _ = fmt.Fprintf(s.stdout, "%s %s created (id %d)\n", spec.name, w.createdLabel(), resp.GetId())
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		if w.fieldSet("name") || w.fieldSet("key") {
			label = w.renamedLabel(label)
		}
		_, _ = fmt.Fprintf(s.stdout, "%s %s updated\n", spec.name, label)
	default:
		_, _ = fmt.Fprintf(s.stdout, "%s %s deleted\n", spec.name, label)
	}
	return nil
}

// applyFlags 按定义顺序（而非命令行顺序）应用 flag：被依赖的字段先落定，例如
// backend 的 --type 先于 --config、--provider 先于 --model。
func (w *writeCtx) applyFlags(defs []*flagDef, p parsedArgs) error {
	for _, def := range defs {
		for _, o := range p.flags {
			if o.def != def {
				continue
			}
			if def.field != "" {
				if owner, ok := w.fieldOwner[def.field]; ok && owner != def.name {
					return usageErrorf("--%s and --%s cannot be combined", owner, def.name)
				}
				if _, ok := w.fieldOwner[def.field]; !ok {
					w.fieldOwner[def.field] = def.name
					w.req.Fields = append(w.req.Fields, def.field)
				}
			}
			if def.check != nil {
				if err := def.check(w); err != nil {
					return err
				}
			}
			v := o.value
			if def.secret {
				var err error
				if v, err = w.secret(o); err != nil {
					return err
				}
			}
			if err := def.apply(w, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// createdLabel 是新资源在输出里的名字；模型写成 提供方/模型key。
func (w *writeCtx) createdLabel() string {
	if m := w.doc.GetModel(); m != nil {
		return w.cat.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, m.GetProviderId()) + "/" + m.GetKey()
	}
	return docOf(w.doc).name
}

// renamedLabel 是改名后的名字（模型的 提供方/模型key 只换最后一段）。
func (w *writeCtx) renamedLabel(old string) string {
	name := docOf(w.doc).name
	if i := strings.LastIndex(old, "/"); i >= 0 {
		return old[:i+1] + name
	}
	return name
}

// confirmDelete：人在终端里删除时再问一次 y/N（spec 决策 5）；其它调用方由执行者审批。
func confirmDelete(s *sys, caller agentrewire.CtlCaller, spec *kindSpec, label string) error {
	if caller != agentrewire.CtlCaller_CTL_CALLER_HUMAN {
		return nil
	}
	_, _ = fmt.Fprintf(s.stderr, "Delete %s %s? [y/N] ", spec.name, label)
	line, _ := bufio.NewReader(s.stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("delete of %s %s canceled", spec.name, label)
	}
}

func parseInt(v string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an integer: %q", v)
	}
	return n, nil
}
