package ctlcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// configField 是某个运行时类型在 --config 里接受的一个键。
type configField struct {
	key    string
	values string // help 里展示的取值说明
}

// engineType 是 help 与 --config 校验共用的运行时类型契约。键名与同步契约
// syncwire.AgentBackendConfig 的 JSON 键一致。
type engineType struct {
	name    string
	summary string
	config  []configField
}

var engineTypes = []engineType{
	{name: "builtin", summary: "Agentre's built-in agent loop; needs --provider"},
	{name: "claudecode", summary: "Claude Code CLI", config: []configField{
		{"modelRoutes", `{ "OPUS"|"SONNET"|"HAIKU": { "providerKey": "<key>", "modelKey": "<key>" } }`},
		{"defaultPermissionMode", "default | acceptEdits | plan | bypassPermissions"},
		{"defaultModel", "--model passed to claude when no provider is bound"},
	}},
	{name: "codex", summary: "Codex CLI", config: []configField{
		{"sandbox", "read-only | workspace-write | danger-full-access"},
		{"approval", "untrusted | on-request | never"},
	}},
	{name: "piagent", summary: "pi agent CLI; needs --provider and --model"},
	{name: "openclaw", summary: "OpenClaw gateway", config: []configField{
		{"openclawGatewayUrl", "ws://loopback… or wss://… (no credentials in the URL)"},
		{"openclawAgentId", "gateway agent id (empty = gateway default)"},
		{"openclawDefaultModel", "model id (empty = agent/session default)"},
		{"openclawSessionMode", "per-agentre-session"},
	}},
	{name: "hermes", summary: "a running `hermes serve`", config: []configField{
		{"hermesUrl", "http(s)://host:port of the serve"},
		{"hermesAuthProvider", "auth provider name of a gated serve, e.g. basic"},
	}},
	{name: "acp", summary: "any ACP v1 agent over stdio", config: []configField{
		{"acpCommand", "executable: absolute path or a name on PATH"},
		{"acpArgs", `["arg", …]`},
	}},
	{name: "antigravity", summary: "Antigravity CLI (agy acp); needs a gemini --provider"},
}

func lookupEngineType(name string) *engineType {
	for i := range engineTypes {
		if engineTypes[i].name == name {
			return &engineTypes[i]
		}
	}
	return nil
}

func engineTypeNames() string {
	names := make([]string, 0, len(engineTypes))
	for _, t := range engineTypes {
		names = append(names, t.name)
	}
	return strings.Join(names, " | ")
}

func initEngineFlags() {
	b := func(w *writeCtx) *agentrewire.CtlEngine { return w.doc.GetEngine() }
	common := []*flagDef{
		strField("name", "<name>", "name", "engine name (unique)", func(w *writeCtx, v string) { b(w).Name = v }),
		refField("provider", kindProvider, "providerId", "LLM provider (empty = the CLI's own login)", func(w *writeCtx, id int64) { b(w).ProviderId = id }),
		{name: "model", value: "<model>", field: "modelId", usage: "fixed model: a model id of the bound provider, <provider>/<model id> when none is bound, or a numeric id (empty = provider default)",
			apply: func(w *writeCtx, v string) error {
				id, err := w.engineModel(v)
				if err == nil {
					b(w).ModelId = id
				}
				return err
			}},
		strField("reasoning-effort", "<level>", "reasoningEffort", "low | medium | high | xhigh | max (empty = default)", func(w *writeCtx, v string) { b(w).ReasoningEffort = v }),
		{name: "env", value: "KEY=VAL", field: "env", repeat: true, usage: "extra environment variable; repeatable; replaces all",
			apply: func(w *writeCtx, v string) error {
				k, val, ok := strings.Cut(v, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return usageErrorf("--env wants KEY=VAL, got %q", v)
				}
				if b(w).Env == nil {
					b(w).Env = map[string]string{}
				}
				b(w).Env[k] = val
				return nil
			}},
		{name: "config", value: "<JSON>", field: "configJson", usage: "type-specific settings as a JSON object (see help engine <type>)",
			apply: func(w *writeCtx, v string) error { return w.setEngineConfig("--config", []byte(v)) }},
		{name: "config-file", value: "<file>", field: "configJson", usage: "same as --config, read from a file",
			apply: func(w *writeCtx, v string) error {
				raw, err := os.ReadFile(v) //nolint:gosec // 用户显式指定要读的文件。
				if err != nil {
					return usageErrorf("--config-file: %v", err)
				}
				return w.setEngineConfig("--config-file", raw)
			}},
	}
	typeFlag := &flagDef{name: "type", value: "<type>", field: "type", usage: engineTypeNames() + " (create only)",
		apply: func(w *writeCtx, v string) error {
			if lookupEngineType(v) == nil {
				return usageErrorf("unknown engine type %q (one of: %s)", v, engineTypeNames())
			}
			b(w).Type = v
			return nil
		}}
	kindEngine.createFlags = append([]*flagDef{typeFlag}, common...)
	kindEngine.updateFlags = common
	kindEngine.required = []string{"type", "name"}
	kindEngine.filters = []*flagDef{{name: "type", value: "<type>", usage: "only engines of this type"}}
	kindEngine.examples = []string{
		`agrctl create engine --type codex --name codex-main --config '{"sandbox":"workspace-write","approval":"on-request"}'`,
		"agrctl update engine codex-main --reasoning-effort high",
		"agrctl delete engine codex-main",
	}
}

// engineType 是本次写入针对的运行时类型：create 取 --type，update 取目标的类型。
func (w *writeCtx) engineType() *engineType {
	if w.target != nil {
		return lookupEngineType(w.target.GetEngine().GetType())
	}
	return lookupEngineType(w.doc.GetEngine().GetType())
}

// setEngineConfig 校验 --config 是 JSON 对象、且每个键都属于该运行时类型，然后原样写入。
func (w *writeCtx) setEngineConfig(flagName string, raw []byte) error {
	t := w.engineType()
	if t == nil {
		return usageErrorf("%s needs --type", flagName)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return usageErrorf("%s must be a JSON object", flagName)
	}
	allowed := map[string]bool{}
	for _, f := range t.config {
		allowed[f.key] = true
	}
	var unknown []string
	for k := range obj {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return usageErrorf("%s: %s not accepted by %s engines (see agrctl help engine %s)",
			flagName, strings.Join(unknown, ", "), t.name, t.name)
	}
	w.doc.GetEngine().ConfigJson = string(bytes.TrimSpace(raw))
	return nil
}

// engineModel 解析 --model：数字 id；已知绑定的提供方（本次 --provider 或目标已绑定的）
// 时是该提供方下的 ModelID；否则是 <provider>/<ModelID>。ModelID 自己可以带 /，所以
// 有提供方时不再按 / 切。
func (w *writeCtx) engineModel(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		return w.ref(kindModel, v)
	}
	providerID := w.target.GetEngine().GetProviderId()
	if w.fieldSet("providerId") {
		providerID = w.doc.GetEngine().GetProviderId()
	}
	if providerID == 0 {
		return w.ref(kindModel, v)
	}
	return w.ref(kindModel, w.cat.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, providerID)+"/"+v)
}
