package ctlcmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// resourcesPath 是执行者的资源接口（契约见 pkg/wire 的 ctl.proto）。
const resourcesPath = "/ctl/v1/resources"

// catalog 连着一个执行者，按类型缓存列表，并在客户端完成名字 / 路径解析。
type catalog struct {
	ep ctlclient.Endpoint
	// args 是本次调用的原始参数，歧义时据此拼出可直接改用的示例命令。
	args  []string
	lists map[agentrewire.CtlKind][]*agentrewire.CtlResource
}

func connect(s *sys, args []string) (*catalog, error) {
	ep, err := ctlclient.Resolve("", "", s.lookupEnv)
	if err != nil {
		return nil, err
	}
	return &catalog{ep: ep, args: args, lists: map[agentrewire.CtlKind][]*agentrewire.CtlResource{}}, nil
}

// caller 按 spec「Routing and approval」给调用方分类：会话级 token 在环境变量里 →
// 会话调用；否则用的是本机握手 token，stdin 是 TTY 为人工调用，不是则为外部调用。
func (c *catalog) caller(s *sys) agentrewire.CtlCaller {
	switch {
	case c.ep.TokenFromEnv:
		return agentrewire.CtlCaller_CTL_CALLER_SESSION
	case s.stdinIsTTY:
		return agentrewire.CtlCaller_CTL_CALLER_HUMAN
	default:
		return agentrewire.CtlCaller_CTL_CALLER_EXTERNAL
	}
}

func (c *catalog) call(req *agentrewire.CtlRequest) (*agentrewire.CtlResponse, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := c.ep.PostRaw(context.Background(), resourcesPath, body)
	if err != nil {
		var se *ctlclient.ServerError
		if errors.As(err, &se) {
			// 执行者的消息原样透出（spec：服务层的拒绝原样展示）。
			return nil, errors.New(se.Message)
		}
		return nil, err
	}
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

func (c *catalog) list(kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	if items, ok := c.lists[kind]; ok {
		return items, nil
	}
	resp, err := c.call(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_List{List: &agentrewire.CtlListRequest{Kind: kind}}})
	if err != nil {
		return nil, err
	}
	items := resp.GetList().GetItems()
	c.lists[kind] = items
	return items, nil
}

func (c *catalog) get(kind agentrewire.CtlKind, id int64) (*agentrewire.CtlResource, error) {
	resp, err := c.call(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Get{Get: &agentrewire.CtlGetRequest{Kind: kind, Id: id}}})
	if err != nil {
		return nil, err
	}
	return resp.GetGet().GetResource(), nil
}

func (c *catalog) write(req *agentrewire.CtlWriteRequest) (*agentrewire.CtlWriteResponse, error) {
	resp, err := c.call(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: req}})
	if err != nil {
		return nil, err
	}
	return resp.GetWrite(), nil
}

// byID 在已列出的条目里按 id 找；找不到返回 nil。
func (c *catalog) byID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource {
	if id == 0 {
		return nil
	}
	items, err := c.list(kind)
	if err != nil {
		return nil
	}
	for _, it := range items {
		if docOf(it).id == id {
			return it
		}
	}
	return nil
}

// path 是资源的完整定位：项目 / 部门是从根开始的 父/子 路径，模型是 提供方/模型key，
// 其它是名字。列表取不到时退回 #id。
func (c *catalog) path(kind agentrewire.CtlKind, id int64) string {
	it := c.byID(kind, id)
	if it == nil {
		if id == 0 {
			return ""
		}
		return "#" + strconv.FormatInt(id, 10)
	}
	d := docOf(it)
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_PROJECT, agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		segs := []string{d.name}
		seen := map[int64]bool{d.id: true}
		for p := d.parentID; p != 0 && !seen[p]; {
			seen[p] = true
			parent := c.byID(kind, p)
			if parent == nil {
				break
			}
			pd := docOf(parent)
			segs = append([]string{pd.name}, segs...)
			p = pd.parentID
		}
		return strings.Join(segs, "/")
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return c.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, d.providerID) + "/" + d.name
	default:
		return d.name
	}
}

// label 是资源在表格与结果行里的短名：模型是 提供方/模型key，其它是名字。
// 完整路径（path）只在需要消歧的地方用：JSON 视图、歧义提示。
func (c *catalog) label(kind agentrewire.CtlKind, id int64) string {
	if kind == agentrewire.CtlKind_CTL_KIND_MODEL {
		return c.path(kind, id)
	}
	it := c.byID(kind, id)
	switch {
	case it != nil:
		return docOf(it).name
	case id == 0:
		return ""
	default:
		return "#" + strconv.FormatInt(id, 10)
	}
}

// locate 把用户给的定位（数字 id、名字、父/子 路径、提供方/模型key）解析成一条资源。
// 找不到是执行失败；有多个匹配是用法错误，并给出候选与示例命令。
func (c *catalog) locate(spec *kindSpec, ref string) (*agentrewire.CtlResource, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, usageErrorf("empty %s reference", spec.name)
	}
	items, err := c.list(spec.kind)
	if err != nil {
		return nil, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		if it := c.byID(spec.kind, id); it != nil {
			return it, nil
		}
		return nil, fmt.Errorf("%s %q not found", spec.name, ref)
	}
	var matches []*agentrewire.CtlResource
	for _, it := range items {
		if c.matches(spec, it, ref) {
			matches = append(matches, it)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%s %q not found", spec.name, ref)
	case 1:
		return matches[0], nil
	default:
		return nil, c.ambiguous(spec, ref, matches)
	}
}

func (c *catalog) matches(spec *kindSpec, it *agentrewire.CtlResource, ref string) bool {
	d := docOf(it)
	switch spec.kind {
	case agentrewire.CtlKind_CTL_KIND_PROJECT, agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		// 路径按后缀匹配：docs、agentre/docs、root/agentre/docs 都能定位同一个项目。
		full := strings.Split(c.path(spec.kind, d.id), "/")
		want := strings.Split(ref, "/")
		if len(want) > len(full) {
			return false
		}
		for i := range want {
			if full[len(full)-len(want)+i] != want[i] {
				return false
			}
		}
		return true
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		if strings.Contains(ref, "/") {
			return c.path(spec.kind, d.id) == ref
		}
		return d.name == ref
	default:
		return d.name == ref
	}
}

func (c *catalog) ambiguous(spec *kindSpec, ref string, matches []*agentrewire.CtlResource) error {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s %q is ambiguous — %d matches:\n", spec.name, ref, len(matches))
	tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tPATH")
	for _, m := range matches {
		id := docOf(m).id
		_, _ = fmt.Fprintf(tw, "  %d\t%s\n", id, c.path(spec.kind, id))
	}
	_ = tw.Flush()
	first := docOf(matches[0]).id
	replacement := c.path(spec.kind, first)
	hint := "Use an ID or a parent/child path"
	if !spec.hierarchical && spec.kind != agentrewire.CtlKind_CTL_KIND_MODEL {
		replacement, hint = strconv.FormatInt(first, 10), "Use an ID"
	}
	_, _ = fmt.Fprintf(&b, "%s, e.g. %s", hint, commandLine(replaceRef(c.args, ref, replacement)))
	return usageErrorf("%s", b.String())
}

// replaceRef 把命令行里第一处等于 ref 的位置参数或 flag 值换成 replacement。
func replaceRef(args []string, ref, replacement string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == ref {
			out[i] = replacement
			return out
		}
		if strings.HasPrefix(a, "-") && strings.HasSuffix(a, "="+ref) {
			out[i] = strings.TrimSuffix(a, ref) + replacement
			return out
		}
	}
	return out
}

// doc 是各类资源文档的共同投影，供定位与展示使用。
type doc struct {
	id         int64
	name       string
	parentID   int64
	providerID int64
}

func docOf(r *agentrewire.CtlResource) doc {
	switch d := r.GetDoc().(type) {
	case *agentrewire.CtlResource_Agent:
		return doc{id: d.Agent.GetId(), name: d.Agent.GetName()}
	case *agentrewire.CtlResource_Department:
		return doc{id: d.Department.GetId(), name: d.Department.GetName(), parentID: d.Department.GetParentId()}
	case *agentrewire.CtlResource_Project:
		return doc{id: d.Project.GetId(), name: d.Project.GetName(), parentID: d.Project.GetParentId()}
	case *agentrewire.CtlResource_Provider:
		return doc{id: d.Provider.GetId(), name: d.Provider.GetName()}
	case *agentrewire.CtlResource_Model:
		return doc{id: d.Model.GetId(), name: d.Model.GetKey(), providerID: d.Model.GetProviderId()}
	case *agentrewire.CtlResource_Backend:
		return doc{id: d.Backend.GetId(), name: d.Backend.GetName()}
	}
	return doc{}
}
