package ctl_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 变更清单由执行者对照当前数据自己算（spec 决策 7）：客户端给的只是「要改成什么」。
// 引用其它资源的字段写成对方的名字；密钥只标记「已写入」，从不带值（Hard invariant 1）。

// refFields 是各类资源里引用其它资源的字段。
var refFields = map[agentrewire.CtlKind]map[string]agentrewire.CtlKind{
	agentrewire.CtlKind_CTL_KIND_AGENT: {
		"departmentId": agentrewire.CtlKind_CTL_KIND_DEPARTMENT, "backendIds": agentrewire.CtlKind_CTL_KIND_BACKEND,
	},
	agentrewire.CtlKind_CTL_KIND_DEPARTMENT: {
		"parentId": agentrewire.CtlKind_CTL_KIND_DEPARTMENT, "leadAgentId": agentrewire.CtlKind_CTL_KIND_AGENT,
	},
	agentrewire.CtlKind_CTL_KIND_PROJECT: {
		"parentId": agentrewire.CtlKind_CTL_KIND_PROJECT, "memberAgentIds": agentrewire.CtlKind_CTL_KIND_AGENT,
	},
	agentrewire.CtlKind_CTL_KIND_MODEL: {"providerId": agentrewire.CtlKind_CTL_KIND_PROVIDER},
	agentrewire.CtlKind_CTL_KIND_BACKEND: {
		"providerId": agentrewire.CtlKind_CTL_KIND_PROVIDER, "modelId": agentrewire.CtlKind_CTL_KIND_MODEL,
	},
}

// secretFields 是只写的密钥字段。
var secretFields = map[string]bool{"apiKey": true, "token": true}

// namer 按 id 取资源的显示名，按类型惰性列一次。
type namer struct {
	h     *ctlHandler
	ctx   context.Context
	lists map[agentrewire.CtlKind][]*agentrewire.CtlResource
}

func (h *ctlHandler) newNamer(ctx context.Context) *namer {
	return &namer{h: h, ctx: ctx, lists: map[agentrewire.CtlKind][]*agentrewire.CtlResource{}}
}

func (n *namer) find(kind agentrewire.CtlKind, match func(*agentrewire.CtlResource) bool) *agentrewire.CtlResource {
	items, ok := n.lists[kind]
	if !ok {
		items, _ = n.h.listResources(n.ctx, kind) // 取不到名字时退回 #id，不挡写入
		n.lists[kind] = items
	}
	for _, it := range items {
		if match(it) {
			return it
		}
	}
	return nil
}

// refName 是被引用资源的名字：模型用 ModelID，其它用名字；找不到写 #id。
func (n *namer) refName(kind agentrewire.CtlKind, id int64) string {
	it := n.find(kind, func(r *agentrewire.CtlResource) bool { return resourceID(r) == id })
	if it == nil {
		return "#" + strconv.FormatInt(id, 10)
	}
	if m := it.GetModel(); m != nil {
		return m.GetModelId()
	}
	return docMessage(it).Get(docMessage(it).Descriptor().Fields().ByName("name")).String()
}

// label 是资源在变更清单里的名字；模型写成 提供方/ModelID（spec 决策 9）。
func (n *namer) label(r *agentrewire.CtlResource) string {
	if m := r.GetModel(); m != nil {
		return n.refName(agentrewire.CtlKind_CTL_KIND_PROVIDER, m.GetProviderId()) + "/" + m.GetModelId()
	}
	m := docMessage(r)
	return m.Get(m.Descriptor().Fields().ByName("name")).String()
}

// modelIDByKey 把提供方的默认模型 key 写成 ModelID。
func (n *namer) modelIDByKey(key string) string {
	it := n.find(agentrewire.CtlKind_CTL_KIND_MODEL, func(r *agentrewire.CtlResource) bool { return r.GetModel().GetKey() == key })
	if it == nil {
		return key
	}
	return it.GetModel().GetModelId()
}

// format 把文档里一个字段写成展示值；nil 表示「没有」（空串、0 引用、空集合）。
func (n *namer) format(kind agentrewire.CtlKind, m protoreflect.Message, fd protoreflect.FieldDescriptor) *string {
	name := fd.JSONName()
	v := m.Get(fd)
	if ref, ok := refFields[kind][name]; ok {
		var ids []int64
		if fd.IsList() {
			for i := 0; i < v.List().Len(); i++ {
				ids = append(ids, v.List().Get(i).Int())
			}
		} else if v.Int() != 0 {
			ids = []int64{v.Int()}
		}
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			names = append(names, n.refName(ref, id))
		}
		return nonEmpty(strings.Join(names, ", "))
	}
	if kind == agentrewire.CtlKind_CTL_KIND_PROVIDER && name == "defaultModelKey" {
		if v.String() == "" {
			return nil
		}
		s := n.modelIDByKey(v.String())
		return &s
	}
	switch {
	case fd.IsMap():
		var pairs []string
		v.Map().Range(func(k protoreflect.MapKey, val protoreflect.Value) bool {
			pairs = append(pairs, k.String()+"="+val.String())
			return true
		})
		sort.Strings(pairs)
		return nonEmpty(strings.Join(pairs, ", "))
	case fd.Kind() == protoreflect.BoolKind:
		s := strconv.FormatBool(v.Bool())
		return &s
	case fd.Kind() == protoreflect.StringKind:
		return nonEmpty(v.String())
	case fd.Kind() == protoreflect.Int64Kind || fd.Kind() == protoreflect.Int32Kind:
		if v.Int() == 0 {
			return nil
		}
		s := strconv.FormatInt(v.Int(), 10)
		return &s
	}
	s := fmt.Sprint(v.Interface())
	return &s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sameValue(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// fieldChanges 算 create / update 的字段前后值；update 时没变的字段不列。
func (n *namer) fieldChanges(kind agentrewire.CtlKind, cur, next *agentrewire.CtlResource, fields []string) ([]*agentrewire.CtlFieldChange, error) {
	nm := docMessage(next)
	var cm protoreflect.Message
	if cur != nil {
		cm = docMessage(cur)
	}
	var out []*agentrewire.CtlFieldChange
	for _, f := range fields {
		fd := nm.Descriptor().Fields().ByJSONName(f)
		switch {
		case secretFields[f]:
			if c := secretChange(f, cur, nm.Get(fd).String()); c != nil {
				out = append(out, c)
			}
		case f == "configJson":
			before := ""
			if cm != nil {
				before = cm.Get(fd).String()
			}
			changes, err := configChanges(before, nm.Get(fd).String())
			if err != nil {
				return nil, err
			}
			out = append(out, changes...)
		default:
			var before *string
			if cm != nil {
				before = n.format(kind, cm, fd)
			}
			after := n.format(kind, nm, fd)
			if cm != nil && sameValue(before, after) {
				continue
			}
			out = append(out, &agentrewire.CtlFieldChange{Field: f, Before: before, After: after})
		}
	}
	return out, nil
}

// secretChange：写入了值就标记 secret；空的 api key 按服务层规则是「沿用原值」，不算变更；
// 空的 token 是清除 token，同样只标记 secret——只有确知未设置时才不算变更（状态未知也要出这一行）。
func secretChange(field string, cur *agentrewire.CtlResource, value string) *agentrewire.CtlFieldChange {
	if value == "" && (field == "apiKey" || cur.GetBackend().GetTokenState() == agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNSET) {
		return nil
	}
	return &agentrewire.CtlFieldChange{Field: field, Secret: true}
}

// configChanges 按键比较后端类型独占设置，字段名写成 config.<键>。
func configChanges(before, after string) ([]*agentrewire.CtlFieldChange, error) {
	b, err := configValues(before)
	if err != nil {
		return nil, err
	}
	a, err := configValues(after)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var out []*agentrewire.CtlFieldChange
	for _, k := range sorted {
		if sameValue(b[k], a[k]) {
			continue
		}
		out = append(out, &agentrewire.CtlFieldChange{Field: "config." + k, Before: b[k], After: a[k]})
	}
	return out, nil
}

// configValues 把设置对象展开成键 → 展示值：字符串去引号，其它保持紧凑 JSON；空值算「没有」。
func configValues(raw string) (map[string]*string, error) {
	out := map[string]*string{}
	if raw == "" {
		return out, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, errBadRequest("configJson must be a JSON object")
	}
	for k, v := range obj {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out[k] = nonEmpty(s)
			continue
		}
		if t := strings.TrimSpace(string(v)); t != "null" {
			out[k] = &t
		}
	}
	return out, nil
}

// approvalInput 把变更清单转成审批卡的 ToolInput。
func approvalInput(command string, c *agentrewire.CtlChange, cascade *blocks.CtlApprovalCascade) blocks.CtlApprovalInput {
	ch := blocks.CtlApprovalChange{
		Op: opName(c.GetOp()), Kind: kindNames[c.GetKind()], ID: c.GetId(), Name: c.GetName(), Cascade: cascade,
	}
	for _, f := range c.GetFields() {
		ch.Fields = append(ch.Fields, blocks.CtlApprovalField{Field: f.GetField(), Before: f.Before, After: f.After, Secret: f.GetSecret()})
	}
	return blocks.CtlApprovalInput{Command: command, Changes: []blocks.CtlApprovalChange{ch}}
}
