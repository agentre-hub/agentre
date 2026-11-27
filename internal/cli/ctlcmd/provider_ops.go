package ctlcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 供应商测试、模型发现与导入（spec「供应商测试与模型导入」）。账号模式在一台在线设备上执行
// （base URL 可能只有设备访问得到），桌面与会话模式在本机桌面执行。导入是写：发现之后逐个
// 走与 `create model` 相同的写路径，审批规则不变。

const (
	providerTestUsage = `agrctl test provider <provider> [--model <model id>] [--device <device>]

Sends one real request with the saved configuration and prints success, or the upstream error
summary (never the API key). Account mode runs it on --device, by default the first online device
that has already received this provider; the desktop runs it locally. A provider created or changed
a moment ago may not have reached the device yet: try again shortly.`
	discoverModelsUsage = `agrctl discover models <provider> [--device <device>] [-o json]

Lists the models the provider's upstream offers and marks the ones already imported.`
	importModelsUsage = `agrctl import models <provider> (--all | <model id>...) [--device <device>]

Imports upstream models as models of the provider. Models that already exist are skipped and counted.
Needs a person at a terminal when logged in (or a personal token with resources:write).`
)

var (
	probeModelFlag  = &flagDef{name: "model", value: "<model id>", usage: "model to test (default: the provider's default model)", notEmpty: true}
	probeDeviceFlag = &flagDef{name: "device", value: "<device>", usage: "device to run on (account mode)", notEmpty: true}
	importAllFlag   = &flagDef{name: "all", usage: "import every upstream model"}
)

// providerOp 报告这条命令是不是供应商测试 / 发现 / 导入；rest 是动词之后的参数。
func providerOp(verb string, rest []string) bool {
	switch verb {
	case "test", "discover", "import":
		return true
	}
	return false
}

func providerOpHelp(verb string) string {
	switch verb {
	case "discover":
		return discoverModelsUsage
	case "import":
		return importModelsUsage
	}
	return providerTestUsage
}

func runProviderOp(verb string, args []string, s *sys) error {
	if wantsHelp(args) {
		_, _ = fmt.Fprintln(s.stdout, providerOpHelp(verb))
		return nil
	}
	resource := map[string][]string{"test": {"provider", "providers"}, "discover": {"models", "model"}, "import": {"models", "model"}}[verb]
	if len(args) == 0 || (args[0] != resource[0] && args[0] != resource[1]) {
		return usageErrorf("usage: agrctl %s %s <provider> …", verb, resource[0])
	}
	rest := args[1:]
	defs := []*flagDef{probeDeviceFlag}
	switch verb {
	case "test":
		defs = append(defs, probeModelFlag)
	case "discover":
		defs = append(defs, outputFlagNames...)
	default:
		defs = append(defs, importAllFlag)
	}
	p, err := parseArgs(rest, defs)
	if err != nil {
		return err
	}
	if len(p.positional) == 0 {
		return usageErrorf("%s %s needs a <provider>", verb, resource[0])
	}
	if verb != "import" && len(p.positional) > 1 {
		return usageErrorf("unexpected argument %q", p.positional[1])
	}
	asJSON := false
	if verb == "discover" {
		if asJSON, err = wantJSON(p); err != nil {
			return err
		}
	}
	var wanted []string
	if verb == "import" {
		wanted = dedupe(p.positional[1:])
		switch {
		case p.on("all") && len(wanted) > 0:
			return usageErrorf("import models: give --all or model ids, not both")
		case !p.on("all") && len(wanted) == 0:
			return usageErrorf("import models: give --all or at least one model id")
		}
	}

	cat, err := connect(s, append([]string{verb}, args...))
	if err != nil {
		return err
	}
	if p.has("device") && cat.x.kind != execAccount {
		return usageErrorf("--device needs an account — run 'agrctl login' (the desktop runs this on itself)")
	}
	if verb == "import" {
		if err := cat.x.refuseExternal(s, "import models"); err != nil {
			return err
		}
	}
	it, err := cat.from(p.positionalAt[0]).locate(kindProvider, p.positional[0])
	if err != nil {
		return err
	}
	provider := it.GetProvider()
	probe := &prober{s: s, cat: cat, provider: provider, device: last(p, "device"), args: args}
	defer probe.close()

	switch verb {
	case "test":
		modelKey := ""
		if id := last(p, "model"); id != "" {
			m, err := cat.locate(kindModel, cat.path(agentrewire.CtlKind_CTL_KIND_PROVIDER, provider.GetId())+"/"+id)
			if err != nil {
				return err
			}
			modelKey = m.GetModel().GetKey()
		}
		return probe.test(modelKey)
	case "discover":
		return probe.discover(asJSON)
	default:
		return probe.importModels(wanted)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// prober 把一次供应商探测交给执行者：账号走一台设备，桌面 / 会话走本机控制 API。
type prober struct {
	s        *sys
	cat      *catalog
	provider *agentrewire.CtlProvider
	device   string
	args     []string
	acct     *ctlaccount.Client
}

func (p *prober) close() {
	if p.acct != nil {
		p.acct.Close()
	}
}

func (p *prober) account() (*ctlaccount.Client, error) {
	if p.provider.GetSyncId() == "" {
		return nil, errors.New("this provider has no account identity yet — try again shortly")
	}
	if p.acct == nil {
		p.acct = newAccountChat(p.s, p.cat.x, p.args)
	}
	return p.acct, nil
}

func (p *prober) test(modelKey string) error {
	name := p.cat.label(agentrewire.CtlKind_CTL_KIND_PROVIDER, p.provider.GetId())
	ctx := context.Background()
	if p.cat.x.kind == execAccount {
		c, err := p.account()
		if err != nil {
			return err
		}
		res, err := c.TestProvider(ctx, ctlaccount.ProbeInput{ProviderKey: p.provider.GetSyncId(), ModelKey: modelKey, Device: p.device})
		if err != nil {
			return accountErr(err)
		}
		if !res.OK {
			return errors.New(failureMessage(res.Message))
		}
		_, _ = fmt.Fprintf(p.s.stdout, "provider %s: ok — %s (on %s, %d ms)\n", name, res.Message, res.Device, res.LatencyMs)
		return nil
	}
	var out struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := p.local("/ctl/v1/provider-test", map[string]any{"providerId": p.provider.GetId(), "modelKey": modelKey}, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New(failureMessage(out.Message))
	}
	_, _ = fmt.Fprintf(p.s.stdout, "provider %s: ok — %s\n", name, out.Message)
	return nil
}

func failureMessage(m string) string {
	if strings.TrimSpace(m) == "" {
		return "the provider test failed"
	}
	return m
}

// local 调桌面（或会话端点）的只读供应商探测。
func (p *prober) local(path string, in, out any) error {
	body, _ := json.Marshal(in)
	raw, err := p.cat.x.ep.PostRaw(context.Background(), path, body)
	if err != nil {
		return serverErr(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// upstream 取上游的模型列表。
func (p *prober) upstream() ([]ctlaccount.DiscoveredModel, error) {
	if p.cat.x.kind == execAccount {
		c, err := p.account()
		if err != nil {
			return nil, err
		}
		models, _, err := c.DiscoverModels(context.Background(), ctlaccount.ProbeInput{ProviderKey: p.provider.GetSyncId(), Device: p.device})
		return models, accountErr(err)
	}
	var out struct {
		Models []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"models"`
	}
	if err := p.local("/ctl/v1/provider-discover", map[string]any{"providerId": p.provider.GetId()}, &out); err != nil {
		return nil, err
	}
	models := make([]ctlaccount.DiscoveredModel, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, ctlaccount.DiscoveredModel{ModelID: m.ModelID, Name: m.Name})
	}
	return models, nil
}

// imported 是供应商下已有的 ModelID。
func (p *prober) imported() map[string]bool {
	have := map[string]bool{}
	for _, m := range p.cat.modelsOf(p.provider.GetId()) {
		have[m.GetModelId()] = true
	}
	return have
}

func (p *prober) discover(asJSON bool) error {
	models, err := p.upstream()
	if err != nil {
		return err
	}
	have := p.imported()
	type row struct {
		ModelID  string `json:"modelId"`
		Name     string `json:"name"`
		Imported bool   `json:"imported"`
	}
	rows := make([]row, 0, len(models))
	for _, m := range models {
		rows = append(rows, row{ModelID: m.ModelID, Name: m.Name, Imported: have[m.ModelID]})
	}
	if asJSON {
		return printJSON(p.s.stdout, rows)
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.ModelID, mark(r.Imported, "imported")})
	}
	return printTable(p.s.stdout, []string{"MODEL", "IMPORTED"}, table)
}

func (p *prober) importModels(wanted []string) error {
	models, err := p.upstream()
	if err != nil {
		return err
	}
	offered := map[string]ctlaccount.DiscoveredModel{}
	for _, m := range models {
		offered[m.ModelID] = m
	}
	selected := models
	if len(wanted) > 0 {
		selected = nil
		for _, id := range wanted {
			m, ok := offered[id]
			if !ok {
				return fmt.Errorf("model %q is not offered by the provider (see 'agrctl discover models')", id)
			}
			selected = append(selected, m)
		}
	}
	have := p.imported()
	var create []ctlaccount.DiscoveredModel
	skipped := 0
	for _, m := range selected {
		if have[m.ModelID] {
			skipped++
			continue
		}
		create = append(create, m)
	}
	for i, m := range create {
		if err := p.createModel(m); err != nil {
			return fmt.Errorf("imported %d of %d models before %q failed: %w", i, len(create), m.ModelID, err)
		}
	}
	_, _ = fmt.Fprintln(p.s.stdout, importSummary(len(create), skipped))
	return nil
}

func importSummary(created, skipped int) string {
	noun := "models"
	if created == 1 {
		noun = "model"
	}
	out := fmt.Sprintf("imported %d %s", created, noun)
	if skipped > 0 {
		out += fmt.Sprintf(", skipped %d that already exist", skipped)
	}
	return out
}

// createModel 与 `create model` 同一条写路径：调用方分类、审批与服务层校验都由执行者负责。
func (p *prober) createModel(m ctlaccount.DiscoveredModel) error {
	doc := &agentrewire.CtlModel{ProviderId: p.provider.GetId(), ModelId: m.ModelID}
	fields := []string{"providerId", "modelId"}
	if m.Name != "" && m.Name != m.ModelID {
		doc.Name = m.Name
		fields = append(fields, "name")
	}
	req := &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_CREATE, Kind: agentrewire.CtlKind_CTL_KIND_MODEL, Fields: fields,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Model{Model: doc}},
		Caller:   p.cat.caller(p.s), Command: commandLine(p.args),
	}
	if req.Caller == agentrewire.CtlCaller_CTL_CALLER_EXTERNAL {
		req.CallerInfo = reportCaller()
	}
	_, err := p.cat.write(req, p.s.stderr)
	return err
}
