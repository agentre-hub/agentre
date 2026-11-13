package ctlcmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 记忆（spec 2026-11-11「Agent 读写记忆」）：在 Agentre 会话里，执行者只给出本会话看得到的
// 条目，写入不等审批；会话以外沿用现有规则（终端里的人直接执行，外部调用方在桌面端审批），
// 看得到所有层，新增时层要写明。记忆没有名字，按编号定位：m58 或 58。
//
// 写入的结果行是稳定格式，对话里的记忆卡片据此识别（改动要同步那边）：
//
//	memory m<id> <created|already exists|updated|archived> (<层>)
//	over budget: <层> is <N> characters over its memory budget; merge or shorten the memories there
//
// <层> 是 global，或 department <部门路径> / agent <名字> / project <项目路径>；第二行只在
// 写入后该层超出预算时出现。

// memoryLayers 是 --layer 接受的层。
var memoryLayers = []string{"agent", "department", "project", "global"}

const memoryNotes = `Layers (--layer; default: agent):
  agent        only you; --agent <agent> names an agent
  department   your department; --department <department> names one above it instead
  project      this session's project; --project <project> names a project
  global       every agent
You can only write to these layers. You can update or archive any memory you can see,
including ones the user wrote; the version before your change is kept and can be restored.
delete archives the memory; the user can restore it.

Inside an Agentre session these commands take effect at once, with no approval. A new
memory reaches new sessions, not this one. Each layer carries summaries into a session
only up to its budget; list shows the rest as CARRIED no. When a write leaves a layer over
budget, merge or shorten the memories there.

Outside an Agentre session the usual rules apply: a person at the terminal writes
directly, any other caller waits for approval in the Agentre desktop. Every layer is
visible, and create needs a named layer: --layer global, --department, --agent or --project.

Worth remembering: a way of working the user corrected, a lasting preference, a fact the
repository cannot tell you.
Do not remember: what the repository already says, what only matters for this task,
secrets. One fact per memory: a one-line summary, details in --detail.`

func initMemoryFlags() {
	m := func(w *writeCtx) *agentrewire.CtlMemory { return w.doc.GetMemory() }
	summary := strField("summary", "<text>", "summary", "one line, carried into new sessions", func(w *writeCtx, v string) { m(w).Summary = v })
	detail := strField("detail", "<text>", "detail", "optional detail, read on demand", func(w *writeCtx, v string) { m(w).Detail = v })
	layer := &flagDef{name: "layer", value: "<layer>", field: "layer", usage: "agent (default) | department | project | global (create only)",
		apply: func(w *writeCtx, v string) error {
			if !isMemoryLayer(v) {
				return usageErrorf("unknown memory layer %q (one of: %s)", v, strings.Join(memoryLayers, ", "))
			}
			m(w).Layer = v
			return nil
		}}
	// owner 生成「写到这一个部门 / agent / 项目」的 flag：定下层并写入所指的 id。
	owner := func(spec *kindSpec, layer, usage string) *flagDef {
		return &flagDef{name: spec.name, value: "<" + spec.name + ">", field: "ownerId", usage: usage,
			apply: func(w *writeCtx, v string) error {
				if w.fieldSet("layer") && m(w).GetLayer() != layer {
					return usageErrorf("--%s only goes with --layer %s", spec.name, layer)
				}
				id, err := w.memberRef(spec, v)
				if err != nil {
					return err
				}
				if !w.fieldSet("layer") {
					w.fieldOwner["layer"] = spec.name
					w.req.Fields = append(w.req.Fields, "layer")
				}
				m(w).Layer, m(w).OwnerId = layer, id
				return nil
			}}
	}
	department := owner(kindDepartment, "department", "write to this department (yours or one above it) instead of your own (create only)")
	agent := owner(kindAgent, "agent", "write to this agent's layer; inside a session only your own (create only)")
	project := owner(kindProject, "project", "write to this project's layer; inside a session only the session's (create only)")
	kindMemory.createFlags = []*flagDef{summary, detail, layer, department, agent, project}
	kindMemory.updateFlags = []*flagDef{summary, detail}
	kindMemory.required = []string{"summary"}
	kindMemory.filters = []*flagDef{
		{name: "layer", value: "<layer>", usage: "only this layer: " + strings.Join(memoryLayers, " | ")},
		{name: "query", value: "<text>", usage: "only memories whose summary or detail contains it (any case)"},
	}
	kindMemory.notes = memoryNotes
	kindMemory.examples = []string{
		`agrctl create memory --summary "Run lint before committing; CI does not"`,
		`agrctl create memory --summary "Release builds pull from the internal registry" --layer project`,
		"agrctl list memory --query lint",
		`agrctl update memory m58 --detail "CI only runs tests"`,
		"agrctl delete memory m58",
	}
}

func isMemoryLayer(v string) bool {
	for _, l := range memoryLayers {
		if v == l {
			return true
		}
	}
	return false
}

// memoryRef 是记忆在输出里的编号。
func memoryRef(id int64) string { return "m" + strconv.FormatInt(id, 10) }

// locateMemory 按编号（m58 或 58）在本会话看得到的记忆里找一条。
func (c *catalog) locateMemory(ref string) (*agentrewire.CtlResource, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(ref), "m"), 10, 64)
	if err != nil || id <= 0 {
		return nil, usageErrorf("a memory is located by its id, such as m58 or 58, got %q", ref)
	}
	if it := c.byID(agentrewire.CtlKind_CTL_KIND_MEMORY, id); it != nil {
		return it, nil
	}
	if _, err := c.list(agentrewire.CtlKind_CTL_KIND_MEMORY); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("memory %q not found", ref)
}

// memoryLayerLabel 是一条记忆所在的层：global，或 <层> <部门路径 / agent 名字 / 项目路径>。
func (c *catalog) memoryLayerLabel(m *agentrewire.CtlMemory) string {
	switch m.GetLayer() {
	case "department":
		return "department " + c.path(agentrewire.CtlKind_CTL_KIND_DEPARTMENT, m.GetOwnerId())
	case "agent":
		return "agent " + c.label(agentrewire.CtlKind_CTL_KIND_AGENT, m.GetOwnerId())
	case "project":
		return "project " + c.path(agentrewire.CtlKind_CTL_KIND_PROJECT, m.GetOwnerId())
	}
	return m.GetLayer()
}

// memoryFilter 是 list memory 的 --layer / --query。
func memoryFilter(p parsedArgs) (func(*agentrewire.CtlResource) bool, error) {
	var layer, query string
	for _, o := range p.flags {
		switch o.def.name {
		case "layer":
			if !isMemoryLayer(o.value) {
				return nil, usageErrorf("unknown memory layer %q (one of: %s)", o.value, strings.Join(memoryLayers, ", "))
			}
			layer = o.value
		case "query":
			query = strings.ToLower(o.value)
		}
	}
	return func(r *agentrewire.CtlResource) bool {
		m := r.GetMemory()
		if layer != "" && m.GetLayer() != layer {
			return false
		}
		return query == "" || strings.Contains(strings.ToLower(m.GetSummary()), query) ||
			strings.Contains(strings.ToLower(m.GetDetail()), query)
	}, nil
}

func memoryDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

func (c *catalog) memoryRow(m *agentrewire.CtlMemory) []string {
	return []string{memoryRef(m.GetId()), c.memoryLayerLabel(m), m.GetSummary(), mark(m.GetDetail() != "", "yes"),
		yesNo(!m.GetOverBudget()), memoryDate(m.GetChangedAt())}
}

type memoryView struct {
	ID      string `json:"id"`
	Layer   string `json:"layer"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
	Pinned  bool   `json:"pinned"`
	// Carried 为 false：该层超出预算，这一条没带进本会话。
	Carried bool   `json:"carried"`
	Updated string `json:"updated"`
}

func (c *catalog) memoryView(m *agentrewire.CtlMemory) memoryView {
	return memoryView{ID: memoryRef(m.GetId()), Layer: c.memoryLayerLabel(m), Summary: m.GetSummary(), Detail: m.GetDetail(),
		Pinned: m.GetPinned(), Carried: !m.GetOverBudget(), Updated: memoryDate(m.GetChangedAt())}
}

// printMemoryWrite 打印写入的结果行（格式见文件头）。
func (c *catalog) printMemoryWrite(w io.Writer, op agentrewire.CtlOp, resp *agentrewire.CtlWriteResponse) {
	verb := "created"
	switch {
	case op == agentrewire.CtlOp_CTL_OP_UPDATE:
		verb = "updated"
	case op == agentrewire.CtlOp_CTL_OP_DELETE:
		verb = "archived"
	case resp.GetExisted():
		verb = "already exists"
	}
	layer := c.memoryLayerLabel(resp.GetResource().GetMemory())
	_, _ = fmt.Fprintf(w, "memory %s %s (%s)\n", memoryRef(resp.GetId()), verb, layer)
	if n := resp.GetOverBudgetChars(); n > 0 {
		_, _ = fmt.Fprintf(w, "over budget: %s is %d characters over its memory budget; merge or shorten the memories there\n", layer, n)
	}
}
