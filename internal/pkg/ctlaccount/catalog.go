package ctlaccount

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Catalog 是账号会话逻辑对 Ctl* 资源目录的窄依赖：按类型列出、按 id 取、拼完整路径、把用户
// 给的定位解析成一条资源。agrctl 用它自己的目录（歧义时给出可照抄的示例命令），agentred 直接
// 用 ResourceCatalog。
type Catalog interface {
	List(kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error)
	ByID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource
	Path(kind agentrewire.CtlKind, id int64) string
	Locate(kind agentrewire.CtlKind, ref string) (*agentrewire.CtlResource, error)
}

// ResourceCatalog 连着一个 Ctl* 资源执行者（post 交出 protojson 的 CtlRequest，收回
// CtlResponse 的原文），按类型缓存列表，并在客户端完成名字 / 路径解析。
type ResourceCatalog struct {
	post  func(ctx context.Context, body []byte) ([]byte, error)
	lists map[agentrewire.CtlKind][]*agentrewire.CtlResource
}

// NewResourceCatalog 造一个空目录；列表在第一次用到时才取。
func NewResourceCatalog(post func(ctx context.Context, body []byte) ([]byte, error)) *ResourceCatalog {
	return &ResourceCatalog{post: post, lists: map[agentrewire.CtlKind][]*agentrewire.CtlResource{}}
}

// Call 发一次 Ctl* 请求。执行者的拒绝消息原样透出（spec：服务层的拒绝原样展示）。
func (c *ResourceCatalog) Call(ctx context.Context, req *agentrewire.CtlRequest) (*agentrewire.CtlResponse, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		var se *ctlclient.ServerError
		if errors.As(err, &se) {
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

// List 列出一类资源（每类只取一次）。
func (c *ResourceCatalog) List(kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	if items, ok := c.lists[kind]; ok {
		return items, nil
	}
	resp, err := c.Call(context.Background(), &agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_List{List: &agentrewire.CtlListRequest{Kind: kind}}})
	if err != nil {
		return nil, err
	}
	items := resp.GetList().GetItems()
	c.lists[kind] = items
	return items, nil
}

// ByID 在已列出的条目里按 id 找；找不到返回 nil。
func (c *ResourceCatalog) ByID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource {
	if id == 0 {
		return nil
	}
	items, err := c.List(kind)
	if err != nil {
		return nil
	}
	for _, it := range items {
		if DocOf(it).ID == id {
			return it
		}
	}
	return nil
}

// Path 是资源的完整定位：项目 / 部门是从根开始的 父/子 路径，模型是 提供方/ModelID，
// 其它是名字。列表取不到时退回 #id。
func (c *ResourceCatalog) Path(kind agentrewire.CtlKind, id int64) string {
	it := c.ByID(kind, id)
	if it == nil {
		if id == 0 {
			return ""
		}
		return "#" + strconv.FormatInt(id, 10)
	}
	d := DocOf(it)
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_PROJECT, agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		segs := []string{d.Name}
		seen := map[int64]bool{d.ID: true}
		for p := d.ParentID; p != 0 && !seen[p]; {
			seen[p] = true
			parent := c.ByID(kind, p)
			if parent == nil {
				break
			}
			pd := DocOf(parent)
			segs = append([]string{pd.Name}, segs...)
			p = pd.ParentID
		}
		return strings.Join(segs, "/")
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return c.Path(agentrewire.CtlKind_CTL_KIND_PROVIDER, d.ProviderID) + "/" + d.Name
	default:
		return d.Name
	}
}

// Matches 报告 ref（名字、父/子 路径、提供方/ModelID）是否指的是 it。
func (c *ResourceCatalog) Matches(kind agentrewire.CtlKind, it *agentrewire.CtlResource, ref string) bool {
	d := DocOf(it)
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_PROJECT, agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		// 路径按后缀匹配：docs、agentre/docs、root/agentre/docs 都能定位同一个项目。
		full := strings.Split(c.Path(kind, d.ID), "/")
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
		// 按第一个 / 切：前面是提供方，后面整段是 ModelID（ModelID 自己可以带 /）。
		provider, modelID, _ := strings.Cut(ref, "/")
		return d.Name == modelID && c.Path(agentrewire.CtlKind_CTL_KIND_PROVIDER, d.ProviderID) == provider
	default:
		return d.Name == ref
	}
}

// Locate 把定位（数字 id、名字、父/子 路径、提供方/ModelID）解析成一条资源。找不到是执行
// 失败；多个匹配交回 *AmbiguousError。
func (c *ResourceCatalog) Locate(kind agentrewire.CtlKind, ref string) (*agentrewire.CtlResource, error) {
	ref = strings.TrimSpace(ref)
	name := KindName(kind)
	if ref == "" {
		return nil, Usagef("empty %s reference", name)
	}
	items, err := c.List(kind)
	if err != nil {
		return nil, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		if it := c.ByID(kind, id); it != nil {
			return it, nil
		}
	}
	var matches []*agentrewire.CtlResource
	for _, it := range items {
		if c.Matches(kind, it, ref) {
			matches = append(matches, it)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%s %q not found", name, ref)
	case 1:
		return matches[0], nil
	default:
		return nil, &AmbiguousError{Err: Usagef("%s %q is ambiguous — %d matches; use an id", name, ref, len(matches))}
	}
}

// KindName 是资源类型在消息里的名字（与 agrctl 的资源名一致）。
func KindName(kind agentrewire.CtlKind) string {
	switch kind {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		return "agent"
	case agentrewire.CtlKind_CTL_KIND_DEPARTMENT:
		return "department"
	case agentrewire.CtlKind_CTL_KIND_PROJECT:
		return "project"
	case agentrewire.CtlKind_CTL_KIND_PROVIDER:
		return "provider"
	case agentrewire.CtlKind_CTL_KIND_MODEL:
		return "model"
	case agentrewire.CtlKind_CTL_KIND_BACKEND:
		return "backend"
	case agentrewire.CtlKind_CTL_KIND_ENGINE:
		return "engine"
	case agentrewire.CtlKind_CTL_KIND_MEMORY:
		return "memory"
	}
	return "resource"
}

// Doc 是各类资源文档的共同投影，供定位与展示使用。
type Doc struct {
	ID         int64
	Name       string
	ParentID   int64
	ProviderID int64
}

// DocOf 取资源文档的共同投影。
func DocOf(r *agentrewire.CtlResource) Doc {
	switch d := r.GetDoc().(type) {
	case *agentrewire.CtlResource_Agent:
		return Doc{ID: d.Agent.GetId(), Name: d.Agent.GetName()}
	case *agentrewire.CtlResource_Department:
		return Doc{ID: d.Department.GetId(), Name: d.Department.GetName(), ParentID: d.Department.GetParentId()}
	case *agentrewire.CtlResource_Project:
		return Doc{ID: d.Project.GetId(), Name: d.Project.GetName(), ParentID: d.Project.GetParentId()}
	case *agentrewire.CtlResource_Provider:
		return Doc{ID: d.Provider.GetId(), Name: d.Provider.GetName()}
	case *agentrewire.CtlResource_Model:
		// 模型对用户的名字是 ModelID；Key 是服务生成的 ModelKey（UUID），不参与定位。
		return Doc{ID: d.Model.GetId(), Name: d.Model.GetModelId(), ProviderID: d.Model.GetProviderId()}
	case *agentrewire.CtlResource_Backend:
		return Doc{ID: d.Backend.GetId(), Name: d.Backend.GetName()}
	case *agentrewire.CtlResource_Engine:
		return Doc{ID: d.Engine.GetId(), Name: d.Engine.GetName()}
	case *agentrewire.CtlResource_Memory:
		// 记忆没有名字：按编号定位，名字一栏放摘要只供展示。
		return Doc{ID: d.Memory.GetId(), Name: d.Memory.GetSummary()}
	}
	return Doc{}
}
