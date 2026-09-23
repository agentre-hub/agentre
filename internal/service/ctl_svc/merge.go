package ctl_svc

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 写请求的字段合并：执行者只信字段集合里列出的字段，其余一律取当前数据（update）或
// 服务层默认值（create）。字段名是资源文档里字段的 JSON 名，与 agrctl 的约定一致。

// errBadRequest 是写请求本身不成立（字段不可写、文档与类型不符等）→ 400。
type errBadRequest string

func (e errBadRequest) Error() string { return string(e) }

// errNotReady 是某类资源的写网关没接线 → 503。
type errNotReady string

func (e errNotReady) Error() string { return string(e) }

const (
	writeOnCreate = 1 << iota
	writeOnUpdate
	writeAlways = writeOnCreate | writeOnUpdate
)

// writableFields 是每类资源可写的字段及其时机；不在表里的是只读字段（id、系统标识、引用数、
// 密钥是否已设置、CLI 路径覆盖等——spec 把 CLI 路径覆盖划在范围外）。项目成员在 update 时走 add/remove，不能整体写。
var writableFields = map[agentrewire.CtlKind]map[string]int{
	agentrewire.CtlKind_CTL_KIND_AGENT: {
		"name": writeAlways, "description": writeAlways, "departmentId": writeAlways, "backendIds": writeAlways,
		"pinned": writeAlways, "avatarColor": writeAlways, "avatarIcon": writeAlways,
	},
	agentrewire.CtlKind_CTL_KIND_DEPARTMENT: {
		"name": writeAlways, "description": writeAlways, "icon": writeAlways, "accentColor": writeAlways,
		"parentId": writeAlways, "leadAgentId": writeAlways,
	},
	agentrewire.CtlKind_CTL_KIND_PROJECT: {
		"name": writeAlways, "description": writeAlways, "icon": writeAlways, "color": writeAlways,
		"path": writeAlways, "parentId": writeAlways, "memberAgentIds": writeOnCreate,
	},
	agentrewire.CtlKind_CTL_KIND_PROVIDER: {
		"type": writeOnCreate, "name": writeAlways, "baseUrl": writeAlways, "apiKey": writeAlways,
		"enabled": writeAlways, "defaultModelKey": writeOnUpdate,
	},
	agentrewire.CtlKind_CTL_KIND_MODEL: {
		"providerId": writeOnCreate, "modelId": writeAlways, "name": writeAlways, "contextWindow": writeAlways,
		"maxOutput": writeAlways, "enabled": writeAlways, "isDefault": writeAlways,
	},
	agentrewire.CtlKind_CTL_KIND_BACKEND: {
		"type": writeOnCreate, "name": writeAlways, "device": writeAlways,
		"providerId": writeAlways, "modelId": writeAlways, "reasoningEffort": writeAlways, "env": writeAlways,
		"configJson": writeAlways, "token": writeAlways,
	},
}

// docMessage 取资源文档里具体那一类的消息；文档为空时返回 nil。
func docMessage(r *agentrewire.CtlResource) protoreflect.Message {
	if r == nil {
		return nil
	}
	m := r.ProtoReflect()
	fd := m.WhichOneof(m.Descriptor().Oneofs().ByName("doc"))
	if fd == nil {
		return nil
	}
	return m.Get(fd).Message()
}

// docKindOf 是文档所属的资源类型（oneof 分支名与 kindNames 一致）。
func docKindOf(r *agentrewire.CtlResource) agentrewire.CtlKind {
	if r == nil {
		return agentrewire.CtlKind_CTL_KIND_UNSPECIFIED
	}
	m := r.ProtoReflect()
	fd := m.WhichOneof(m.Descriptor().Oneofs().ByName("doc"))
	if fd == nil {
		return agentrewire.CtlKind_CTL_KIND_UNSPECIFIED
	}
	for kind, name := range kindNames {
		if string(fd.Name()) == name {
			return kind
		}
	}
	return agentrewire.CtlKind_CTL_KIND_UNSPECIFIED
}

// newDocOf 造一份某类的空文档。
func newDocOf(kind agentrewire.CtlKind) *agentrewire.CtlResource {
	r := &agentrewire.CtlResource{}
	m := r.ProtoReflect()
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(kindNames[kind]))
	m.Set(fd, m.NewField(fd))
	return r
}

// mergeDoc 算出写入后的完整文档：cur（create 时为空文档）叠加请求里 fields 列出的字段。
// configJson 只替换请求里出现的键（spec「Executors」/ ctl 契约）。密钥字段只有本次写入
// 时才保留值，否则清空——cur 里的是掩码，绝不能当成新值交下去。
func mergeDoc(kind agentrewire.CtlKind, op agentrewire.CtlOp, cur, req *agentrewire.CtlResource, fields []string) (*agentrewire.CtlResource, error) {
	allowed := writableFields[kind]
	when := writeOnUpdate
	if op == agentrewire.CtlOp_CTL_OP_CREATE {
		when = writeOnCreate
	}
	var next *agentrewire.CtlResource
	if cur != nil {
		next = proto.Clone(cur).(*agentrewire.CtlResource)
	} else {
		next = newDocOf(kind)
	}
	nm := docMessage(next)
	if len(fields) > 0 && docKindOf(req) != kind {
		return nil, errBadRequest(fmt.Sprintf("the request document is not a %s", kindNames[kind]))
	}
	rm := docMessage(req)
	set := map[string]bool{}
	for _, f := range fields {
		fd := nm.Descriptor().Fields().ByJSONName(f)
		if fd == nil || allowed[f]&when == 0 {
			return nil, errBadRequest(fmt.Sprintf("%s field %q cannot be written by %s", kindNames[kind], f, opName(op)))
		}
		set[f] = true
		if f == "configJson" {
			merged, err := mergeConfigJSON(nm.Get(fd).String(), rm.Get(fd).String())
			if err != nil {
				return nil, err
			}
			nm.Set(fd, protoreflect.ValueOfString(merged))
			continue
		}
		v := rm.Get(fd)
		if (fd.IsList() && v.List().Len() == 0) || (fd.IsMap() && v.Map().Len() == 0) {
			nm.Clear(fd)
			continue
		}
		nm.Set(fd, v)
	}
	if p := next.GetProvider(); p != nil && !set["apiKey"] {
		p.ApiKey = ""
	}
	if b := next.GetBackend(); b != nil && !set["token"] {
		b.Token = ""
	}
	return next, nil
}

// mergeConfigJSON 把请求里的键盖到当前的类型独占设置上。
func mergeConfigJSON(cur, req string) (string, error) {
	out := map[string]json.RawMessage{}
	if cur != "" {
		if err := json.Unmarshal([]byte(cur), &out); err != nil {
			return "", fmt.Errorf("current backend config is not a JSON object: %w", err)
		}
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req), &patch); err != nil || patch == nil {
		return "", errBadRequest("configJson must be a JSON object")
	}
	for k, v := range patch {
		out[k] = v
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// mergeMembers 是 update 时项目成员的增减：保持原顺序，去掉 remove，追加新的 add。
func mergeMembers(cur, add, remove []int64) []int64 {
	drop := map[int64]bool{}
	for _, id := range remove {
		drop[id] = true
	}
	var out []int64
	seen := map[int64]bool{}
	for _, id := range append(append([]int64(nil), cur...), add...) {
		if drop[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func opName(op agentrewire.CtlOp) string {
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return "create"
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "update"
	case agentrewire.CtlOp_CTL_OP_DELETE:
		return "delete"
	}
	return op.String()
}
