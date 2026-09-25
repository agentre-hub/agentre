package ctlcmd

import (
	"strings"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// kindSpec 描述一类资源在命令行上的样子：名字、写入 flag、删除 flag、列表过滤 flag。
type kindSpec struct {
	kind   agentrewire.CtlKind
	name   string   // 单数，用于 get/create/update/delete 与输出
	plural string   // list 用的复数
	short  []string // 短名，如 dept、proj
	// hierarchical 为 true：名字只在同级内唯一，可用 父/子 路径定位。
	hierarchical bool
	// createFlags / updateFlags 是写入 flag；它们的 field 进 CtlWriteRequest.fields。
	createFlags []*flagDef
	updateFlags []*flagDef
	// required 是 create 必须给出的 flag。
	required []string
	// deleteFlags 是 delete 接受的 flag（--cascade / --force）。
	deleteFlags []*flagDef
	// filters 是 list 的过滤 flag。
	filters []*flagDef
	// examples 进 help。
	examples []string
	// secrets 描述该资源的密钥 flag（help 用）。
	secretHelp string
}

var (
	kindAgent      = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_AGENT, name: "agent", plural: "agents"}
	kindDepartment = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_DEPARTMENT, name: "department", plural: "departments", short: []string{"dept", "depts"}, hierarchical: true}
	kindProject    = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_PROJECT, name: "project", plural: "projects", short: []string{"proj", "projs"}, hierarchical: true}
	kindProvider   = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, name: "provider", plural: "providers"}
	kindModel      = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_MODEL, name: "model", plural: "models"}
	kindBackend    = &kindSpec{kind: agentrewire.CtlKind_CTL_KIND_BACKEND, name: "backend", plural: "backends"}

	allKinds = []*kindSpec{kindAgent, kindDepartment, kindProject, kindProvider, kindModel, kindBackend}
)

// lookupKind 接受单数、复数与短名。
func lookupKind(word string) *kindSpec {
	w := strings.ToLower(word)
	for _, k := range allKinds {
		if w == k.name || w == k.plural {
			return k
		}
		for _, s := range k.short {
			if w == s {
				return k
			}
		}
	}
	return nil
}

func kindNames() string {
	names := make([]string, 0, len(allKinds))
	for _, k := range allKinds {
		names = append(names, k.name)
	}
	return strings.Join(names, ", ")
}

func init() {
	initAgentFlags()
	initDepartmentFlags()
	initProjectFlags()
	initProviderFlags()
	initModelFlags()
	initBackendFlags()
}

// strField 生成一个写字符串字段的 flag。
func strField(name, value, field, usage string, set func(w *writeCtx, v string)) *flagDef {
	return &flagDef{name: name, value: value, field: field, usage: usage, apply: func(w *writeCtx, v string) error {
		set(w, v)
		return nil
	}}
}

// refField 生成一个按名字或 id 引用其它资源的 flag；空值表示清空引用。
func refField(name string, target *kindSpec, field, usage string, set func(w *writeCtx, id int64)) *flagDef {
	return &flagDef{name: name, value: "<" + target.name + ">", field: field, usage: usage, apply: func(w *writeCtx, v string) error {
		id, err := w.ref(target, v)
		if err != nil {
			return err
		}
		set(w, id)
		return nil
	}}
}

// boolField 生成一个把布尔字段置为 val 的开关 flag（--enable / --disable 这类）。
func boolField(name, field, usage string, val bool, set func(w *writeCtx, v bool)) *flagDef {
	return &flagDef{name: name, field: field, usage: usage, apply: func(w *writeCtx, v string) error {
		set(w, (v == "true") == val)
		return nil
	}}
}

func initAgentFlags() {
	a := func(w *writeCtx) *agentrewire.CtlAgent { return w.doc.GetAgent() }
	common := []*flagDef{
		strField("name", "<name>", "name", "agent name (unique)", func(w *writeCtx, v string) { a(w).Name = v }),
		strField("description", "<text>", "description", "what the agent does", func(w *writeCtx, v string) { a(w).Description = v }),
		refField("department", kindDepartment, "departmentId", "department (name, path or id; empty = none)", func(w *writeCtx, id int64) { a(w).DepartmentId = id }),
		{name: "backend", value: "<backend>", field: "backendIds", repeat: true,
			usage: "execution target; repeat for several, in order; replaces all targets",
			apply: func(w *writeCtx, v string) error {
				id, err := w.memberRef(kindBackend, v)
				if err == nil {
					a(w).BackendIds = append(a(w).BackendIds, id)
				}
				return err
			}},
		boolField("pinned", "pinned", "pin in the sidebar (--pinned=false to unpin)", true, func(w *writeCtx, v bool) { a(w).Pinned = v }),
		strField("color", "<color>", "avatarColor", "avatar color", func(w *writeCtx, v string) { a(w).AvatarColor = v }),
		strField("icon", "<icon>", "avatarIcon", "avatar icon", func(w *writeCtx, v string) { a(w).AvatarIcon = v }),
	}
	kindAgent.createFlags, kindAgent.updateFlags = common, common
	kindAgent.required = []string{"name"}
	kindAgent.filters = []*flagDef{{name: "department", value: "<department>", usage: "only agents of this department"}}
	kindAgent.examples = []string{
		"agrctl create agent --name reviewer --department 研发部 --backend claude-local --description \"reviews diffs\"",
		"agrctl update agent reviewer --department 质量部",
		"agrctl delete agent reviewer",
	}
}

func initDepartmentFlags() {
	d := func(w *writeCtx) *agentrewire.CtlDepartment { return w.doc.GetDepartment() }
	common := []*flagDef{
		strField("name", "<name>", "name", "department name (unique among siblings)", func(w *writeCtx, v string) { d(w).Name = v }),
		strField("description", "<text>", "description", "description", func(w *writeCtx, v string) { d(w).Description = v }),
		strField("icon", "<icon>", "icon", "icon", func(w *writeCtx, v string) { d(w).Icon = v }),
		strField("color", "<color>", "accentColor", "accent color: agent-1 … agent-16, or neutral", func(w *writeCtx, v string) { d(w).AccentColor = v }),
		refField("parent", kindDepartment, "parentId", "parent department (empty = top level)", func(w *writeCtx, id int64) { d(w).ParentId = id }),
		refField("lead", kindAgent, "leadAgentId", "lead agent (empty = none)", func(w *writeCtx, id int64) { d(w).LeadAgentId = id }),
	}
	kindDepartment.createFlags, kindDepartment.updateFlags = common, common
	kindDepartment.required = []string{"name"}
	kindDepartment.deleteFlags = []*flagDef{{name: "cascade", usage: "also delete sub-departments and their agents (default: move them up to the parent)"}}
	kindDepartment.examples = []string{
		"agrctl create department --name 研发部 --parent 总部 --lead architect",
		"agrctl update department 总部/研发部 --color agent-3",
		"agrctl delete department 临时小组 --cascade",
	}
}

func initProjectFlags() {
	p := func(w *writeCtx) *agentrewire.CtlProject { return w.doc.GetProject() }
	common := []*flagDef{
		strField("name", "<name>", "name", "project name (unique among siblings)", func(w *writeCtx, v string) { p(w).Name = v }),
		strField("description", "<text>", "description", "description", func(w *writeCtx, v string) { p(w).Description = v }),
		strField("icon", "<icon>", "icon", "icon", func(w *writeCtx, v string) { p(w).Icon = v }),
		strField("color", "<color>", "color", "color: agent-1 … agent-16, or neutral", func(w *writeCtx, v string) { p(w).Color = v }),
		strField("path", "<dir>", "path", "directory on the executor's machine", func(w *writeCtx, v string) { p(w).Path = v }),
		refField("parent", kindProject, "parentId", "parent project (empty = top level)", func(w *writeCtx, id int64) { p(w).ParentId = id }),
	}
	addMemberCreate := &flagDef{name: "add-member", value: "<agent>", field: "memberAgentIds", repeat: true, usage: "add a member agent; repeatable",
		apply: func(w *writeCtx, v string) error {
			id, err := w.memberRef(kindAgent, v)
			if err == nil {
				p(w).MemberAgentIds = append(p(w).MemberAgentIds, id)
			}
			return err
		}}
	addMemberUpdate := &flagDef{name: "add-member", value: "<agent>", repeat: true, usage: "add a member agent; repeatable",
		apply: func(w *writeCtx, v string) error {
			id, err := w.memberRef(kindAgent, v)
			if err == nil {
				w.req.AddMemberAgentIds = append(w.req.AddMemberAgentIds, id)
			}
			return err
		}}
	removeMember := &flagDef{name: "remove-member", value: "<agent>", repeat: true, usage: "remove a member agent; repeatable",
		apply: func(w *writeCtx, v string) error {
			id, err := w.memberRef(kindAgent, v)
			if err == nil {
				w.req.RemoveMemberAgentIds = append(w.req.RemoveMemberAgentIds, id)
			}
			return err
		}}
	kindProject.createFlags = append(append([]*flagDef{}, common...), addMemberCreate)
	kindProject.updateFlags = append(append([]*flagDef{}, common...), addMemberUpdate, removeMember)
	kindProject.required = []string{"name"}
	kindProject.filters = []*flagDef{{name: "parent", value: "<project>", usage: "only direct children of this project"}}
	kindProject.examples = []string{
		"agrctl create project --name docs --parent agentre --path ~/Code/agentre/docs",
		"agrctl update project agentre --add-member reviewer --color agent-3",
		"agrctl delete project agentre/docs",
	}
}

func initProviderFlags() {
	p := func(w *writeCtx) *agentrewire.CtlProvider { return w.doc.GetProvider() }
	common := []*flagDef{
		strField("name", "<name>", "name", "provider name (unique)", func(w *writeCtx, v string) { p(w).Name = v }),
		strField("base-url", "<url>", "baseUrl", "API base URL", func(w *writeCtx, v string) { p(w).BaseUrl = v }),
		// API key 不能置空（spec「Executors」）；不写这个 flag 才是保留原值。
		{name: "api-key", secret: true, notEmpty: true, field: "apiKey", usage: "API key (secret, see below)",
			apply: func(w *writeCtx, v string) error {
				p(w).ApiKey = v
				return nil
			}},
		boolField("enable", "enabled", "enable the provider", true, func(w *writeCtx, v bool) { p(w).Enabled = v }),
		boolField("disable", "enabled", "disable the provider", false, func(w *writeCtx, v bool) { p(w).Enabled = v }),
	}
	defaultModel := &flagDef{name: "default-model", value: "<model id>", field: "defaultModelKey", usage: "default model, by its model id under this provider (or its numeric id)",
		apply: func(w *writeCtx, v string) error {
			key, err := w.providerModelKey(v)
			if err == nil {
				p(w).DefaultModelKey = key
			}
			return err
		}}
	typeFlag := strField("type", "<type>", "type", "anthropic | openai-chat | openai-response (create only)", func(w *writeCtx, v string) { p(w).Type = v })
	kindProvider.createFlags = append([]*flagDef{typeFlag}, common...)
	kindProvider.updateFlags = append(append([]*flagDef{}, common...), defaultModel)
	kindProvider.required = []string{"name", "type"}
	kindProvider.deleteFlags = []*flagDef{forceFlag("provider")}
	kindProvider.secretHelp = "--api-key        bare: typed in without echo (needs a terminal).\n" +
		"                 --api-key=<value> also works, but the key stays in your shell history."
	kindProvider.examples = []string{
		"agrctl create provider --type openai-chat --name openrouter --base-url https://openrouter.ai/api/v1 --api-key",
		"agrctl update provider openrouter --api-key",
		"agrctl delete provider openrouter --force",
	}
}

func forceFlag(what string) *flagDef {
	return &flagDef{name: "force", usage: "delete even though backends still reference this " + what}
}

func initModelFlags() {
	m := func(w *writeCtx) *agentrewire.CtlModel { return w.doc.GetModel() }
	common := []*flagDef{
		strField("model-id", "<id>", "modelId", "model id sent to the API; locates the model as <provider>/<model id>", func(w *writeCtx, v string) { m(w).ModelId = v }),
		strField("name", "<name>", "name", "display name", func(w *writeCtx, v string) { m(w).Name = v }),
		intField("context-window", "contextWindow", "context window in tokens", func(w *writeCtx, v int64) { m(w).ContextWindow = v }),
		intField("max-output", "maxOutput", "max output tokens", func(w *writeCtx, v int64) { m(w).MaxOutput = v }),
		boolField("enable", "enabled", "enable the model", true, func(w *writeCtx, v bool) { m(w).Enabled = v }),
		boolField("disable", "enabled", "disable the model", false, func(w *writeCtx, v bool) { m(w).Enabled = v }),
		boolField("default", "isDefault", "make it the provider's default model", true, func(w *writeCtx, v bool) { m(w).IsDefault = v }),
	}
	providerFlag := refField("provider", kindProvider, "providerId", "owning provider (create only)", func(w *writeCtx, id int64) { m(w).ProviderId = id })
	kindModel.createFlags = append([]*flagDef{providerFlag}, common...)
	kindModel.updateFlags = common
	kindModel.required = []string{"provider", "model-id"}
	kindModel.deleteFlags = []*flagDef{forceFlag("model")}
	kindModel.filters = []*flagDef{{name: "provider", value: "<provider>", usage: "only models of this provider"}}
	kindModel.examples = []string{
		"agrctl create model --provider openrouter --model-id qwen/qwen3-coder",
		"agrctl update model openrouter/qwen/qwen3-coder --default",
		"agrctl delete model openrouter/qwen/qwen3-coder",
	}
}

func intField(name, field, usage string, set func(w *writeCtx, v int64)) *flagDef {
	return &flagDef{name: name, value: "<n>", field: field, usage: usage, apply: func(w *writeCtx, v string) error {
		n, err := parseInt(v)
		if err != nil {
			return usageErrorf("--%s: %v", name, err)
		}
		set(w, n)
		return nil
	}}
}
