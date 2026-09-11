package syncwire

import "reflect"

// 同步载荷的形状:一个 kind 一个类型,整个工作区只有这一份定义。
//
// server 同时是这些对象的一等写入方(CreateOrgObject / UpdateOrgObject / SetProjectLocation
// 与看板写入)。载荷类型要是只活在某一端的私有结构体里,另一端就只能拿字符串字面量读写
// 同一份 JSON —— 加一个字段要改好几处,漏一处不报错,只是那一列在某一端静默变空。所以
// 载荷类型归契约所有,两个宿主消费同一份定义。
//
// **JSON 标签是承重的,不是命名风格。** 这些载荷已经躺在真实账号的
// sync_objects.payload 里,接收端按键名取值:
//
//   - 改一个键名 = 一次静默的数据不兼容,老载荷里那个键从此没人认领;
//   - 改一个 omitempty = 改「这个字段没值」在线上的表示(键不在 / 键在且值为零),
//     那对接收端不是同一件事。
//
// 两者都不会有编译错误。payload_test.go 用零值与满值两份 golden JSON 把每个字段的
// 键名、次序与 omitempty 逐字钉住。
//
// ── 跨机引用规则 ────────────────────────────────────────────────────────────
//
// **载荷里不出现任何本地自增 ID。** 数值主键是某台机器的本地键,在别的机器上指向
// 完全不同的对象。跨机引用一律用三种字符串之一表达:
//
//   - 同步组里的另一个对象 → 它的**同步标识**(`*_sync_id`);
//   - 一台 agentred → 它的**设备指纹**,而且不在载荷里 —— 它走上行/下行项自己的
//     agentred_fingerprint 列(决策 14);
//   - 一个 LLM 供应商 → 它的 **provider_key**(决策 6:llm_providers 整表不出本机,
//     跨机只传这个稳定字符串键)。
//
// GuardPayload 强制执行这条边界:键名以 id 结尾且取值是数字的一律拒。
//
// 同理,**账号内自然键不在载荷里,在列上**:project_location 的(项目同步标识,
// agentred 指纹)与 agent_backend_cli 的(后端同步标识, agentred 指纹)走
// ScopeSyncID / AgentredFingerprint 两列,R4b 的合并与那个部分唯一索引认的都是列。
//
// 载荷也**不带本机独有状态**:项目的本机路径(决策 6、R9)、任务的会话与运行态、
// backend 的 cli_path(每设备覆盖,自成一个 kind)、头像正文(R16a,只传内容哈希)。

// ── 项目 ────────────────────────────────────────────────────────────────────

// ProjectPayload 是 kind=project 的载荷。
//
// **没有 path**:本机路径住在桌面端的 projects.path,只上报、不同步(决策 6、R9);
// 同步进来的项目在接收端是「未配置路径」状态(R10)。也没有 status —— 存活 / 墓碑
// 由上行/下行项的 DeletedAt 表达,本地 status 是它的本机投影。
type ProjectPayload struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	Description string `json:"description"`
	// ParentSyncID 为空即根项目。项目树的父引用,跨机用同步标识表达。
	ParentSyncID string `json:"parent_sync_id,omitempty"`
	// SortOrder 是同一层内的手工排序位次。
	SortOrder int `json:"sort_order"`
}

// ProjectAgentPayload 是 kind=project_agent 的载荷:项目 ↔ Agent 的成员关系。
// 关系表的主键是两个本地自增值,因此两端都只能用同步标识表达。
type ProjectAgentPayload struct {
	ProjectSyncID string `json:"project_sync_id"`
	AgentSyncID   string `json:"agent_sync_id"`
	// JoinedAt 是加入时刻(Unix 毫秒)。成员关系没有别的可改内容:它要么在、要么不在。
	JoinedAt int64 `json:"joined_at"`
}

// ProjectLocationPayload 是 kind=project_location 的载荷:某个项目在某台 agentred
// 上的路径。
//
// 只带路径正文。账号内自然键(项目同步标识, agentred 指纹)**在列上**——
// ScopeSyncID 装项目、AgentredFingerprint 装机器,server 据此按 R4b 合并。
type ProjectLocationPayload struct {
	Path string `json:"path"`
}

// ── 组织 ────────────────────────────────────────────────────────────────────

// DepartmentPayload 是 kind=department 的载荷。
type DepartmentPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	AccentColor string `json:"accent_color"`
	// ParentSyncID 为空即根部门。
	ParentSyncID string `json:"parent_sync_id,omitempty"`
	// LeadAgentSyncID 是部门负责人的同步标识。
	//
	// 它与 department ↔ agent 之间存在**引用环**:负责人必须是本部门成员,所以那个
	// Agent 的落地又等着这个部门。接收端因此不能把它当成阻塞引用 —— 两边都阻塞就是
	// 死锁,两行一起躺到过期(桌面端 sync_svc/adapter_org.go 的 refs 注释与
	// adapter_refcycle_test.go 是那道守卫)。
	LeadAgentSyncID string `json:"lead_agent_sync_id,omitempty"`
	SortOrder       int    `json:"sort_order"`
}

// AgentPayload 是 kind=agent 的载荷。
//
// 里面只有 AvatarHash 而没有头像正文:正文按内容哈希单独走一条路(R16a),
// 一律不进同步载荷(GuardPayload 挡住 avatar_data_url 这个键)。也没有 skills_json
// —— 技能授权下沉到执行目标行(R15e)。
type AgentPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarColor string `json:"avatar_color"`
	AvatarIcon  string `json:"avatar_icon"`
	// AvatarHash 是自定义头像正文的内容哈希;空串 = 没有自定义头像。正文按这个哈希
	// 单独取(R16a),取不到时接收端保留本机已有的那份,不阻塞这一行落地。
	AvatarHash string `json:"avatar_hash,omitempty"`
	// SystemBadge 非空即那一个系统 Agent(唯一合法的「既不属于部门也没有上级」)。
	SystemBadge string `json:"system_badge"`
	// DepartmentSyncID 与 ParentAgentSyncID 是归属的二选一,两个都要如实带出:
	// 接收端的归属选择器靠这两个键决定自己停在哪一组上,不能只带推导后的结果。
	DepartmentSyncID  string `json:"department_sync_id,omitempty"`
	ParentAgentSyncID string `json:"parent_agent_sync_id,omitempty"`
	SortOrder         int    `json:"sort_order"`
	// PromptJSON / ToolsJSON 是两份 JSON 字符串,形状归桌面端所有,契约只搬运不解析
	// —— 解析它就等于在两个宿主里各维护一份同一个结构。
	PromptJSON string `json:"prompt_json"`
	ToolsJSON  string `json:"tools_json"`
	Pinned     bool   `json:"pinned"`
}

// AgentBackendPayload 是 kind=agent_backend 的载荷:后端的**账号级身份**。
//
// 供应商配置自成一个对象(kind=llm_provider),这里只带 ProviderKey 这个稳定字符串
// 键。cli_path 是每设备覆盖,自成一个对象(kind=agent_backend_cli),**永不出现在
// 这里** —— GuardPayload 对 kind=agent_backend 明确挡住 cli_path 这个键。
//
// 运行设备同样不是载荷里的键:它走上行/下行项自己的 agentred_fingerprint 列,
// 后端在一台桌面端上配好之后,在每一端与 server 上指的都是同一台机器。
//
// **EnvJSON 是用户自填的透传环境变量表,按设计随后端上行,在账号下明文存放。**
// App 自管的那批保留键(ANTHROPIC_API_KEY 等)由桌面端的实体校验拒绝入库,但用户
// 自己往里放的任何别的密钥都会原样过机 —— GuardPayload 不是凭据扫描器,挡不住以
// JSON 字符串形式携带的正文(见它的文档)。
type AgentBackendPayload struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	ProviderKey string `json:"provider_key"`
	ModelKey    string `json:"model_key"`
	ModelRoutes string `json:"model_routes"`
	Sandbox     string `json:"sandbox"`
	Approval    string `json:"approval"`
	EnvJSON     string `json:"env_json"`

	ReasoningEffort       string `json:"reasoning_effort"`
	DefaultPermissionMode string `json:"default_permission_mode"`
	DefaultModel          string `json:"default_model"`

	OpenClawGatewayURL   string `json:"openclaw_gateway_url"`
	OpenClawAgentID      string `json:"openclaw_agent_id"`
	OpenClawDefaultModel string `json:"openclaw_default_model"`
	OpenClawSessionMode  string `json:"openclaw_session_mode"`
}

// AgentBackendCLIPayload 是 kind=agent_backend_cli 的载荷:某个后端在某台机器上的
// 可执行文件路径。
//
// 它与 project_location 同形 —— 只带正文,身份(后端同步标识, agentred 指纹)在
// ScopeSyncID / AgentredFingerprint 两列上,绝不进后端自己的身份载荷。这一条覆盖
// 缺席只意味着那台机器上走 PATH,账号级身份在每一端照常可用。
type AgentBackendCLIPayload struct {
	CLIPath string `json:"cli_path"`
}

// AgentExecTargetPayload 是 kind=agent_exec_target 的载荷:Agent 的一档执行目标。
type AgentExecTargetPayload struct {
	AgentSyncID   string `json:"agent_sync_id"`
	BackendSyncID string `json:"backend_sync_id"`
	// SortOrder 是这一档在链上的位次;0 基,越小越先用。
	SortOrder int `json:"sort_order"`
	// SkillsJSON 是这一档的技能授权(R15e:授权下沉到档,不在 Agent 行上)。
	SkillsJSON string `json:"skills_json"`
}

// ── LLM 供应商 ──────────────────────────────────────────────────────────────

// LLMProviderPayload 是 kind=llm_provider 的载荷,**唯一携带 API Key 的对象**
// (GuardPayload 只对这一个 kind 放行 api_key 这个键)。
//
// 它的同步标识就是 ProviderKey 本身:别的对象跨机引用供应商时只传这个稳定字符串键
// (决策 6),因此这个对象没有第二套标识。
//
// 模型行**内嵌**而不另起一个 kind:model_key 由此保持稳定,又不必为一张从属表再造
// 一整套同步对象。
type LLMProviderPayload struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	// DefaultModelKey 指向 Models 里某一项的 ModelKey。
	DefaultModelKey string             `json:"default_model_key"`
	Enabled         bool               `json:"enabled"`
	Models          []LLMProviderModel `json:"models"`
}

// LLMProviderModel 是内嵌在 LLMProviderPayload 里的一行模型。它**不是**一个 kind:
// 模型跟着供应商整行走。
type LLMProviderModel struct {
	// ModelKey 是账号内稳定的模型键,别的对象(后端、任务)按它引用模型。
	ModelKey string `json:"model_key"`
	// ModelID 是发给供应商 API 的那个字符串。它以 id 结尾但**是字符串**,因此不落进
	// GuardPayload 的本地自增 ID 那一条。
	ModelID string `json:"model_id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// ContextWindow / MaxOutput 为 0 = 未知,带 omitempty 一并缺席:线上「没填」与
	// 「填了 0」不该长成同一个样子。
	ContextWindow int `json:"context_window,omitempty"`
	MaxOutput     int `json:"max_output,omitempty"`
}

// ── 看板 ────────────────────────────────────────────────────────────────────

// LabelPayload 是 kind=label 的载荷。
//
// Status 在载荷里,而别的对象的存活 / 墓碑只靠 DeletedAt 表达:server 没有本地行,
// 它的看板读路径把 sync_objects 的载荷直接拼成响应,没有这一列就判不出一个标签还
// 在不在。桌面端落地时**不**读它 —— 一条活着的下行项按定义就是活的(决策 20)。
type LabelPayload struct {
	Name   string `json:"name"`
	Tone   string `json:"tone"`
	Status int    `json:"status"`
}

// IssuePayload 是 kind=issue 的载荷。
//
// 载荷里**没有运行态**:agent_status、session_id 与 source 是某一台机器上这一轮跑
// 成什么样,跨机没有意义,也不该让另一端的界面显示一个它并不持有的会话。也没有
// state —— 它完全由 Stage 推导(stage=done 即已完成),两端各自算。
//
// 执行归属的四个字段里,Agent 与后端是账号级对象、用同步标识表达;供应商与模型
// 本来就是稳定的字符串键。
type IssuePayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Stage       string `json:"stage"`
	// Position 是同一列内的排序位置,取浮点是为了在两张卡之间插入而不重排整列。
	Position           float64 `json:"position"`
	ProjectSyncID      string  `json:"project_sync_id,omitempty"`
	AgentSyncID        string  `json:"agent_sync_id,omitempty"`
	AgentBackendSyncID string  `json:"agent_backend_sync_id,omitempty"`
	LLMProviderKey     string  `json:"llm_provider_key"`
	LLMModelKey        string  `json:"llm_model_key"`
	// ClosedAt 是完成时刻(Unix 毫秒),0 = 未完成。
	ClosedAt int64 `json:"closed_at"`
}

// IssueLabelPayload 是 kind=issue_label 的载荷:任务 ↔ 标签的关联。
// 关联表的主键是 (issue_id, label_id) 两个本地自增值,在另一台机器上指向完全不同的
// 两行,因此两端都只能用同步标识表达。
type IssueLabelPayload struct {
	IssueSyncID string `json:"issue_sync_id"`
	LabelSyncID string `json:"label_sync_id"`
}

// ── kind ↔ 载荷类型 ─────────────────────────────────────────────────────────

// payloadTypes 是「哪种 kind 对应哪个载荷类型」这件事的唯一答案。
//
// kind 与载荷类型的对应要是只散落在各 adapter 的 kind() 方法里,sync_svc 之外就没有
// 任何地方说得出来。收进契约之后,新增一个 kind 却忘了给它载荷类型,payload_test.go
// 当场红 —— 而不是等到某一端解出一片空值。
var payloadTypes = map[string]reflect.Type{
	KindProject:         reflect.TypeOf(ProjectPayload{}),
	KindDepartment:      reflect.TypeOf(DepartmentPayload{}),
	KindAgent:           reflect.TypeOf(AgentPayload{}),
	KindAgentBackend:    reflect.TypeOf(AgentBackendPayload{}),
	KindAgentBackendCLI: reflect.TypeOf(AgentBackendCLIPayload{}),
	KindAgentExecTarget: reflect.TypeOf(AgentExecTargetPayload{}),
	KindProjectAgent:    reflect.TypeOf(ProjectAgentPayload{}),
	KindProjectLocation: reflect.TypeOf(ProjectLocationPayload{}),
	KindLLMProvider:     reflect.TypeOf(LLMProviderPayload{}),
	KindLabel:           reflect.TypeOf(LabelPayload{}),
	KindIssue:           reflect.TypeOf(IssuePayload{}),
	KindIssueLabel:      reflect.TypeOf(IssueLabelPayload{}),
}

// PayloadFor 按对象类型造一份新的空载荷,返回的是**指针**,可以直接交给
// json.Unmarshal。不认识的类型返回 (nil, false) —— 取值域与 Kinds 一样是闭合的。
//
// 知道自己在解什么的调用方直接用具体类型(`var p syncwire.IssuePayload`)更清楚;
// 这个口子是给按 kind 分发、拿不到静态类型的那几处用的。
//
// **它不是写路径的工具。** sync_objects 是整行 last-write-wins,把一份载荷解进
// 结构体再 marshal 回去,会把这个类型当下不认识的键(对端新版本刚加的)静默抹掉。
// 写一个键就走 map[string]any 往返,只覆盖这次真的涉及的键。
func PayloadFor(kind string) (any, bool) {
	t, ok := payloadTypes[kind]
	if !ok {
		return nil, false
	}
	return reflect.New(t).Interface(), true
}
