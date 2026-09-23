package ctlcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// runList：`list <资源> [过滤 flag] [-o json]`。
func runList(args []string, s *sys) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErrorf("list needs a resource: %s", kindNames())
	}
	spec := lookupKind(args[0])
	if spec == nil {
		return usageErrorf("unknown resource %q (one of: %s)", args[0], kindNames())
	}
	p, err := parseArgs(args[1:], append(append([]*flagDef{}, spec.filters...), outputFlagNames...))
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usageErrorf("unexpected argument %q", p.positional[0])
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	cat, err := connect(s, append([]string{"list"}, args...))
	if err != nil {
		return err
	}
	items, err := cat.list(spec.kind)
	if err != nil {
		return err
	}
	keep, err := listFilter(cat, spec, p)
	if err != nil {
		return err
	}
	var views []any
	var rows [][]string
	for _, it := range items {
		if !keep(it) {
			continue
		}
		v := cat.view(it, false)
		views = append(views, v)
		rows = append(rows, cat.row(it))
	}
	if asJSON {
		if views == nil {
			views = []any{}
		}
		return printJSON(s.stdout, views)
	}
	return printTable(s.stdout, tableHeader(spec.kind), rows)
}

// listFilter 把过滤 flag 翻译成谓词；引用按名字 / 路径 / id 解析。
func listFilter(cat *catalog, spec *kindSpec, p parsedArgs) (func(*agentrewire.CtlResource) bool, error) {
	keep := func(*agentrewire.CtlResource) bool { return true }
	for _, o := range p.flags {
		switch {
		case spec == kindAgent && o.def.name == "department":
			id, err := refID(cat, kindDepartment, o.value)
			if err != nil {
				return nil, err
			}
			keep = func(r *agentrewire.CtlResource) bool { return r.GetAgent().GetDepartmentId() == id }
		case spec == kindProject && o.def.name == "parent":
			id, err := refID(cat, kindProject, o.value)
			if err != nil {
				return nil, err
			}
			keep = func(r *agentrewire.CtlResource) bool { return r.GetProject().GetParentId() == id }
		case spec == kindModel && o.def.name == "provider":
			id, err := refID(cat, kindProvider, o.value)
			if err != nil {
				return nil, err
			}
			keep = func(r *agentrewire.CtlResource) bool { return r.GetModel().GetProviderId() == id }
		case spec == kindBackend && o.def.name == "type":
			if lookupBackendType(o.value) == nil {
				return nil, usageErrorf("unknown backend type %q (one of: %s)", o.value, backendTypeNames())
			}
			t := o.value
			keep = func(r *agentrewire.CtlResource) bool { return r.GetBackend().GetType() == t }
		}
	}
	return keep, nil
}

func refID(cat *catalog, spec *kindSpec, v string) (int64, error) {
	it, err := cat.locate(spec, v)
	if err != nil {
		return 0, err
	}
	return docOf(it).id, nil
}

// runGet：`get <资源> <定位>`，输出 JSON 详情。
func runGet(args []string, s *sys) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErrorf("get needs a resource: %s", kindNames())
	}
	spec := lookupKind(args[0])
	if spec == nil {
		return usageErrorf("unknown resource %q (one of: %s)", args[0], kindNames())
	}
	p, err := parseArgs(args[1:], outputFlagNames)
	if err != nil {
		return err
	}
	if _, err := wantJSON(p); err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("get %s needs exactly one <id|name>", spec.name)
	}
	cat, err := connect(s, append([]string{"get"}, args...))
	if err != nil {
		return err
	}
	it, err := cat.locate(spec, p.positional[0])
	if err != nil {
		return err
	}
	detail, err := cat.get(spec.kind, docOf(it).id)
	if err != nil {
		return err
	}
	return printJSON(s.stdout, cat.view(detail, true))
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printTable(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			if c == "" {
				c = "-"
			}
			cells[i] = c
		}
		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func tableHeader(kind agentrewire.CtlKind) []string {
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		return []string{"ID", "NAME", "DEPARTMENT", "BACKENDS", "PINNED"}
	case agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		return []string{"ID", "NAME", "PARENT", "LEAD"}
	case agentrewire.CtlKind_CTL_KIND_PROJECT:
		return []string{"ID", "NAME", "PARENT", "PATH"}
	case agentrewire.CtlKind_CTL_KIND_PROVIDER:
		return []string{"ID", "NAME", "TYPE", "ENABLED", "MODELS", "DEFAULT", "API KEY"}
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return []string{"ID", "MODEL ID", "PROVIDER", "CONTEXT", "ENABLED", "DEFAULT"}
	default:
		return []string{"ID", "NAME", "TYPE", "DEVICE", "PROVIDER", "MODEL"}
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func mark(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

func itoa(n int64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// row 是资源在表格里的一行，列与 tableHeader 对应。
func (c *catalog) row(r *agentrewire.CtlResource) []string {
	id := strconv.FormatInt(docOf(r).id, 10)
	switch d := r.GetDoc().(type) {
	case *agentrewire.CtlResource_Agent:
		a := d.Agent
		return []string{id, a.GetName(), c.label(agentrewire.CtlKind_CTL_KIND_DEPARTMENT, a.GetDepartmentId()),
			strings.Join(c.labels(agentrewire.CtlKind_CTL_KIND_BACKEND, a.GetBackendIds()), ","), mark(a.GetPinned(), "yes")}
	case *agentrewire.CtlResource_Department:
		x := d.Department
		return []string{id, x.GetName(), c.label(agentrewire.CtlKind_CTL_KIND_DEPARTMENT, x.GetParentId()),
			c.label(agentrewire.CtlKind_CTL_KIND_AGENT, x.GetLeadAgentId())}
	case *agentrewire.CtlResource_Project:
		x := d.Project
		return []string{id, x.GetName(), c.label(agentrewire.CtlKind_CTL_KIND_PROJECT, x.GetParentId()), x.GetPath()}
	case *agentrewire.CtlResource_Provider:
		x := d.Provider
		return []string{id, x.GetName(), x.GetType(), yesNo(x.GetEnabled()), strconv.Itoa(len(c.modelsOf(x.GetId()))),
			c.defaultModelID(x), mark(x.GetApiKeySet(), "set")}
	case *agentrewire.CtlResource_Model:
		x := d.Model
		return []string{id, x.GetModelId(), c.label(agentrewire.CtlKind_CTL_KIND_PROVIDER, x.GetProviderId()),
			itoa(x.GetContextWindow()), yesNo(x.GetEnabled()), mark(x.GetIsDefault(), "*")}
	case *agentrewire.CtlResource_Backend:
		x := d.Backend
		return []string{id, x.GetName(), x.GetType(), x.GetDevice(), c.label(agentrewire.CtlKind_CTL_KIND_PROVIDER, x.GetProviderId()),
			c.label(agentrewire.CtlKind_CTL_KIND_MODEL, x.GetModelId())}
	}
	return []string{id}
}

func (c *catalog) paths(kind agentrewire.CtlKind, ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.path(kind, id))
	}
	return out
}

func (c *catalog) labels(kind agentrewire.CtlKind, ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.label(kind, id))
	}
	return out
}

func (c *catalog) modelsOf(providerID int64) []*agentrewire.CtlModel {
	items, err := c.list(agentrewire.CtlKind_CTL_KIND_MODEL)
	if err != nil {
		return nil
	}
	var out []*agentrewire.CtlModel
	for _, it := range items {
		if m := it.GetModel(); m.GetProviderId() == providerID {
			out = append(out, m)
		}
	}
	return out
}

// defaultModelID 是提供方默认模型的 ModelID（默认模型以 ModelKey 记在提供方上）。
func (c *catalog) defaultModelID(p *agentrewire.CtlProvider) string {
	for _, m := range c.modelsOf(p.GetId()) {
		if m.GetKey() == p.GetDefaultModelKey() {
			return m.GetModelId()
		}
	}
	return p.GetDefaultModelKey()
}

func (c *catalog) modelPath(id int64) string {
	if id == 0 {
		return ""
	}
	return c.path(agentrewire.CtlKind_CTL_KIND_MODEL, id)
}

// ─── JSON views：字段名与 create/update 的 flag 对应，引用显示为名字 / 路径 ───

type references struct {
	AgentBackends int32 `json:"agentBackends"`
}

type agentView struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Department  string   `json:"department"`
	Backends    []string `json:"backends"`
	Pinned      bool     `json:"pinned"`
	Color       string   `json:"color,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	System      string   `json:"system,omitempty"`
}

type departmentView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	Parent      string `json:"parent"`
	Lead        string `json:"lead"`
}

type locationView struct {
	Device string `json:"device"`
	Path   string `json:"path"`
}

type projectView struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Parent      string         `json:"parent"`
	Description string         `json:"description"`
	Icon        string         `json:"icon,omitempty"`
	Color       string         `json:"color,omitempty"`
	Path        string         `json:"path"`
	Members     []string       `json:"members"`
	Locations   []locationView `json:"locations,omitempty"`
}

type providerModelView struct {
	ModelID       string `json:"modelId"`
	ContextWindow int64  `json:"contextWindow,omitempty"`
	Enabled       bool   `json:"enabled"`
}

type providerView struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	BaseURL      string              `json:"baseURL"`
	Enabled      bool                `json:"enabled"`
	APIKey       string              `json:"apiKey"`
	DefaultModel string              `json:"defaultModel"`
	Models       []providerModelView `json:"models"`
	References   references          `json:"references"`
}

type modelView struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
	// Key 是服务生成的 ModelKey，只在 get 里展示。
	Key           string     `json:"key,omitempty"`
	Name          string     `json:"name,omitempty"`
	ContextWindow int64      `json:"contextWindow,omitempty"`
	MaxOutput     int64      `json:"maxOutput,omitempty"`
	Enabled       bool       `json:"enabled"`
	Default       bool       `json:"default"`
	References    references `json:"references"`
}

type backendView struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Device          string            `json:"device"`
	CLIPath         string            `json:"cliPath,omitempty"`
	Provider        string            `json:"provider"`
	Model           string            `json:"model"`
	ReasoningEffort string            `json:"reasoningEffort,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Config          json.RawMessage   `json:"config,omitempty"`
	// Token 只说是否已设置（set / unset），只对有 token 的类型出现。
	Token string `json:"token,omitempty"`
}

// view 是资源的 JSON 视图：list -o json 与 get 共用；get 的关联信息（成员路径、
// 引用计数）来自执行者的详情响应。detail 为 true（get）时才带模型的 ModelKey。
func (c *catalog) view(r *agentrewire.CtlResource, detail bool) any {
	switch d := r.GetDoc().(type) {
	case *agentrewire.CtlResource_Agent:
		a := d.Agent
		return agentView{ID: a.GetId(), Name: a.GetName(), Description: a.GetDescription(),
			Department: c.path(agentrewire.CtlKind_CTL_KIND_DEPARTMENT, a.GetDepartmentId()),
			Backends:   c.paths(agentrewire.CtlKind_CTL_KIND_BACKEND, a.GetBackendIds()), Pinned: a.GetPinned(),
			Color: a.GetAvatarColor(), Icon: a.GetAvatarIcon(), System: a.GetSystemBadge()}
	case *agentrewire.CtlResource_Department:
		x := d.Department
		return departmentView{ID: x.GetId(), Name: x.GetName(), Description: x.GetDescription(), Icon: x.GetIcon(),
			Color: x.GetAccentColor(), Parent: c.path(agentrewire.CtlKind_CTL_KIND_DEPARTMENT, x.GetParentId()),
			Lead: c.path(agentrewire.CtlKind_CTL_KIND_AGENT, x.GetLeadAgentId())}
	case *agentrewire.CtlResource_Project:
		x := d.Project
		v := projectView{ID: x.GetId(), Name: x.GetName(), Parent: c.path(agentrewire.CtlKind_CTL_KIND_PROJECT, x.GetParentId()),
			Description: x.GetDescription(), Icon: x.GetIcon(), Color: x.GetColor(), Path: x.GetPath(),
			Members: c.paths(agentrewire.CtlKind_CTL_KIND_AGENT, x.GetMemberAgentIds())}
		for _, l := range x.GetLocations() {
			dev := l.GetDeviceName()
			if dev == "" {
				dev = l.GetDeviceId()
			}
			v.Locations = append(v.Locations, locationView{Device: dev, Path: l.GetPath()})
		}
		return v
	case *agentrewire.CtlResource_Provider:
		x := d.Provider
		v := providerView{ID: x.GetId(), Name: x.GetName(), Type: x.GetType(), BaseURL: x.GetBaseUrl(), Enabled: x.GetEnabled(),
			APIKey: x.GetApiKey(), DefaultModel: c.defaultModelID(x), Models: []providerModelView{},
			References: references{AgentBackends: x.GetBackendRefs()}}
		for _, m := range c.modelsOf(x.GetId()) {
			v.Models = append(v.Models, providerModelView{ModelID: m.GetModelId(), ContextWindow: m.GetContextWindow(), Enabled: m.GetEnabled()})
		}
		return v
	case *agentrewire.CtlResource_Model:
		x := d.Model
		v := modelView{ID: x.GetId(), Provider: c.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, x.GetProviderId()),
			ModelID: x.GetModelId(), Name: x.GetName(), ContextWindow: x.GetContextWindow(), MaxOutput: x.GetMaxOutput(),
			Enabled: x.GetEnabled(), Default: x.GetIsDefault(), References: references{AgentBackends: x.GetBackendRefs()}}
		if detail {
			v.Key = x.GetKey()
		}
		return v
	case *agentrewire.CtlResource_Backend:
		x := d.Backend
		v := backendView{ID: x.GetId(), Name: x.GetName(), Type: x.GetType(), Device: x.GetDevice(), CLIPath: x.GetCliPath(),
			Provider: c.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, x.GetProviderId()), Model: c.modelPath(x.GetModelId()),
			ReasoningEffort: x.GetReasoningEffort(), Env: x.GetEnv()}
		if cfg := strings.TrimSpace(x.GetConfigJson()); cfg != "" && json.Valid([]byte(cfg)) {
			v.Config = json.RawMessage(cfg)
		}
		if t := lookupBackendType(x.GetType()); t != nil && t.token {
			v.Token = mark(x.GetTokenSet(), "set")
			if v.Token == "" {
				v.Token = "unset"
			}
		}
		return v
	}
	return nil
}
