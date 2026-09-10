package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cago-frame/agents/provider"
)

const (
	appServerCommand     = "app-server"
	appServerListenFlag  = "--listen"
	appServerListenStdio = "stdio://"
	appServerConfigFlag  = "--config"

	appMethodInitialize      = "initialize"
	appMethodInitialized     = "initialized"
	appMethodThreadStart     = "thread/start"
	appMethodThreadResume    = "thread/resume"
	appMethodThreadFork      = "thread/fork"
	appMethodThreadRevert    = "thread/revert"
	appMethodThreadTurnsList = "thread/turns/list"
	appMethodThreadCompact   = "thread/compact/start"
	appMethodThreadGoalSet   = "thread/goal/set"
	appMethodThreadGoalGet   = "thread/goal/get"
	appMethodThreadGoalClear = "thread/goal/clear"
	appMethodTurnStart       = "turn/start"
	appMethodTurnSteer       = "turn/steer"
	appMethodTurnInterrupt   = "turn/interrupt"
	appMethodTurnStarted     = "turn/started"
	appMethodTurnCompleted   = "turn/completed"
	appMethodSkillsList      = "skills/list"

	appMethodItemStarted                   = "item/started"
	appMethodItemCompleted                 = "item/completed"
	appMethodItemPlanDelta                 = "item/plan/delta"
	appMethodItemAgentMessageDelta         = "item/agentMessage/delta"
	appMethodItemReasoningTextDelta        = "item/reasoning/textDelta"
	appMethodItemReasoningSummaryTextDelta = "item/reasoning/summaryTextDelta"
	appMethodItemFileChangePatchUpdated    = "item/fileChange/patchUpdated"
	appMethodItemCommandApprovalRequest    = "item/commandExecution/requestApproval"
	appMethodItemFileApprovalRequest       = "item/fileChange/requestApproval"
	appMethodItemPermissionsRequest        = "item/permissions/requestApproval"
	appMethodItemToolRequestUserInput      = "item/tool/requestUserInput"
	appMethodItemToolCall                  = "item/tool/call"
	appMethodRawResponseItemCompleted      = "rawResponseItem/completed"
	appMethodThreadTokenUsageUpdated       = "thread/tokenUsage/updated"
	appMethodThreadCompacted               = "thread/compacted"
	appMethodTurnPlanUpdated               = "turn/plan/updated"
	appMethodServerRequestResolved         = "serverRequest/resolved"
	appMethodError                         = "error"

	appItemUserMessage       = "userMessage"
	appItemAgentMessage      = "agentMessage"
	appItemReasoning         = "reasoning"
	appItemPlan              = "plan"
	appItemCommandExecution  = "commandExecution"
	appItemFileChange        = "fileChange"
	appItemMCPToolCall       = "mcpToolCall"
	appItemDynamicToolCall   = "dynamicToolCall"
	appItemCollabAgentTool   = "collabAgentToolCall"
	appItemContextCompaction = "contextCompaction"
	appItemWebSearch         = "webSearch"
	appItemImageView         = "imageView"
	appItemSleep             = "sleep"
	appItemImageGeneration   = "imageGeneration"
	appItemSubAgentActivity  = "subAgentActivity"
	appItemHookPrompt        = "hookPrompt"
	appItemEnteredReviewMode = "enteredReviewMode"
	appItemExitedReviewMode  = "exitedReviewMode"

	appStatusCompleted   = "completed"
	appStatusInterrupted = "interrupted"
	appStatusFailed      = "failed"

	appToolCommandExecution = "command_execution"
	appToolFileChange       = "file_change"
	appToolUpdatePlan       = "update_plan"
)

type runSpec struct {
	resumeID          string
	cwd               string
	sandbox           SandboxMode
	approval          ApprovalPolicy
	collaborationMode CollaborationMode
}

type ImageDetail string

const (
	ImageDetailHigh     ImageDetail = "high"
	ImageDetailOriginal ImageDetail = "original"
)

type UserInput struct {
	Type         string      `json:"type"`
	Text         string      `json:"text,omitempty"`
	TextElements []any       `json:"text_elements"`
	URL          string      `json:"url,omitempty"`
	Path         string      `json:"path,omitempty"`
	Detail       ImageDetail `json:"detail,omitempty"`
}

func (in UserInput) MarshalJSON() ([]byte, error) {
	switch in.Type {
	case "text":
		return json.Marshal(struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			TextElements []any  `json:"text_elements"`
		}{Type: in.Type, Text: in.Text, TextElements: in.TextElements})
	case "image":
		return json.Marshal(struct {
			Type   string      `json:"type"`
			URL    string      `json:"url"`
			Detail ImageDetail `json:"detail,omitempty"`
		}{Type: in.Type, URL: in.URL, Detail: in.Detail})
	case "localImage":
		return json.Marshal(struct {
			Type   string      `json:"type"`
			Path   string      `json:"path"`
			Detail ImageDetail `json:"detail,omitempty"`
		}{Type: in.Type, Path: in.Path, Detail: in.Detail})
	default:
		type alias UserInput
		return json.Marshal(alias(in))
	}
}

func TextInput(text string) UserInput {
	return UserInput{Type: "text", Text: text, TextElements: []any{}}
}

func LocalImageInput(path string, detail ImageDetail) UserInput {
	return UserInput{Type: "localImage", Path: path, Detail: detail}
}

type appThread struct {
	ID           string `json:"id"`
	Cwd          string `json:"cwd"`
	ForkedFromID string `json:"forkedFromId"`
}

type appThreadResponse struct {
	Thread appThread `json:"thread"`
	Model  string    `json:"model"`
}

type appThreadStartResult struct {
	ThreadID string
	Model    string
}

type appTurnStartResponse struct {
	Turn appTurn `json:"turn"`
}

type appTurn struct {
	ID     string        `json:"id"`
	Status string        `json:"status"`
	Error  *appTurnError `json:"error"`
}

type appTurnError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails"`
}

type appNotification struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	WillRetry bool            `json:"willRetry"`
	Error     *appNotifyError `json:"error"`
	Item      *appThreadItem  `json:"item"`
	ItemID    string          `json:"itemId"`
	RequestID json.RawMessage `json:"requestId"`
	Delta     string          `json:"delta"`
	Usage     *appTokenUsage  `json:"tokenUsage"`
	Turn      *appTurn        `json:"turn"`
	Plan      []appPlanStep   `json:"plan"`
	Raw       json.RawMessage `json:"-"`
	Changes   []appFileChange `json:"changes"`
	// ModelContextWindow 在部分 codex 版本里跟 tokenUsage 平铺在 notification params 顶层；
	// 同时 appTokenUsage 内也兼容解析，二者取非零者。
	ModelContextWindow int `json:"modelContextWindow,omitempty"`
}

type appNotifyError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails"`
}

type appThreadItem struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Raw  json.RawMessage `json:"-"`

	Text string `json:"text,omitempty"`
	// Codex 0.131.0 represents userMessage text as content[] rather than a
	// top-level text field. Keep Text above for older app-server builds.
	Content appItemContent `json:"content,omitempty"`

	Command          string          `json:"command,omitempty"`
	Cwd              string          `json:"cwd,omitempty"`
	Status           string          `json:"status,omitempty"`
	AggregatedOutput string          `json:"aggregatedOutput,omitempty"`
	ExitCode         *int            `json:"exitCode,omitempty"`
	Server           string          `json:"server,omitempty"`
	Tool             string          `json:"tool,omitempty"`
	Namespace        string          `json:"namespace,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	Error            *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Changes []appFileChange `json:"changes,omitempty"`
}

func (i *appThreadItem) UnmarshalJSON(data []byte) error {
	type alias appThreadItem
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*i = appThreadItem(a)
	i.Raw = append(i.Raw[:0], data...)
	return nil
}

type appUserInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// appItemContent normalizes the two app-server content shapes at the protocol
// boundary: userMessage uses an input-item array, while compatible Responses
// providers can surface completed agentMessage content as a plain string.
type appItemContent []appUserInput

func (c *appItemContent) UnmarshalJSON(data []byte) error {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err == nil {
		items := make(appItemContent, 0, len(rawItems))
		for _, raw := range rawItems {
			var item appUserInput
			if err := json.Unmarshal(raw, &item); err == nil {
				if item.Type == "" && item.Text != "" {
					item.Type = "text"
				}
				items = append(items, item)
				continue
			}

			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return err
			}
			items = append(items, appUserInput{Type: "text", Text: text})
		}
		*c = items
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	*c = appItemContent{{Type: "text", Text: text}}
	return nil
}

type appFileChange struct {
	Path string `json:"path"`
	Kind any    `json:"kind"`
	Diff string `json:"diff"`
}

type appPlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type appToolRequestUserInputParams struct {
	ThreadID  string                            `json:"threadId"`
	TurnID    string                            `json:"turnId"`
	ItemID    string                            `json:"itemId"`
	Questions []appToolRequestUserInputQuestion `json:"questions"`
}

type appToolRequestUserInputQuestion struct {
	ID       string                          `json:"id"`
	Header   string                          `json:"header"`
	Question string                          `json:"question"`
	IsOther  bool                            `json:"isOther"`
	IsSecret bool                            `json:"isSecret"`
	Options  []appToolRequestUserInputOption `json:"options"`
}

type appToolRequestUserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type appTokenUsage struct {
	Total              appTokenBreakdown `json:"total"`
	Last               appTokenBreakdown `json:"last"`
	ModelContextWindow int               `json:"modelContextWindow,omitempty"`
}

type appTokenBreakdown struct {
	TotalTokens           int `json:"totalTokens"`
	InputTokens           int `json:"inputTokens"`
	CachedInputTokens     int `json:"cachedInputTokens"`
	OutputTokens          int `json:"outputTokens"`
	ReasoningOutputTokens int `json:"reasoningOutputTokens"`
}

func buildAppServerArgs(config, extra []string) []string {
	args := []string{appServerCommand, appServerListenFlag, appServerListenStdio}
	for _, c := range config {
		if c != "" {
			args = append(args, appServerConfigFlag, c)
		}
	}
	args = append(args, extra...)
	return args
}

func buildEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := os.Environ()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func threadParams(c *Client, spec runSpec) map[string]any {
	params := map[string]any{}
	if c.model != "" {
		params["model"] = c.model
	}
	if spec.cwd != "" {
		params["cwd"] = spec.cwd
	}
	if spec.sandbox != "" {
		params["sandbox"] = string(spec.sandbox)
	}
	if spec.approval != "" {
		params["approvalPolicy"] = string(spec.approval)
	}
	if strings.TrimSpace(c.systemPrompt) != "" {
		params["developerInstructions"] = c.systemPrompt
	}
	return params
}

func userInput(text string) []UserInput {
	return []UserInput{TextInput(text)}
}

func turnStartParams(thread appThreadStartResult, prompt string, mode CollaborationMode, fallbackModel string) (map[string]any, error) {
	return turnStartParamsInput(thread, userInput(prompt), mode, fallbackModel)
}

func turnStartParamsInput(thread appThreadStartResult, input []UserInput, mode CollaborationMode, fallbackModel string) (map[string]any, error) {
	if len(input) == 0 {
		input = userInput("")
	}
	params := map[string]any{
		"threadId": thread.ThreadID,
		"input":    input,
	}
	if mode == "" {
		return params, nil
	}
	if mode != CollaborationDefault && mode != CollaborationPlan {
		return nil, fmt.Errorf("codex: invalid collaboration mode %q", mode)
	}
	model := strings.TrimSpace(thread.Model)
	if model == "" {
		model = strings.TrimSpace(fallbackModel)
	}
	if model == "" {
		model = defaultModelID
	}
	params["collaborationMode"] = map[string]any{
		"mode": string(mode),
		"settings": map[string]any{
			"model": model,
		},
	}
	return params, nil
}

func textForItem(item *appThreadItem) string {
	if item == nil {
		return ""
	}
	if item.Text != "" {
		return item.Text
	}
	if len(item.Content) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range item.Content {
		if c.Type == "text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func parseNotification(params json.RawMessage) (appNotification, error) {
	var n appNotification
	if len(params) > 0 {
		if err := json.Unmarshal(params, &n); err != nil {
			return appNotification{}, err
		}
	}
	n.Raw = append(json.RawMessage(nil), params...)
	return n, nil
}

func parseRequestUserInputParams(params json.RawMessage) (RequestUserInputEvent, error) {
	var in appToolRequestUserInputParams
	if err := json.Unmarshal(params, &in); err != nil {
		return RequestUserInputEvent{}, err
	}
	out := RequestUserInputEvent{
		ThreadID: in.ThreadID,
		TurnID:   in.TurnID,
		ItemID:   in.ItemID,
	}
	out.Questions = make([]RequestUserInputQuestion, 0, len(in.Questions))
	for _, q := range in.Questions {
		opts := make([]RequestUserInputOption, 0, len(q.Options))
		for _, opt := range q.Options {
			opts = append(opts, RequestUserInputOption(opt))
		}
		out.Questions = append(out.Questions, RequestUserInputQuestion{
			ID:       q.ID,
			Header:   q.Header,
			Question: q.Question,
			IsOther:  q.IsOther,
			IsSecret: q.IsSecret,
			Options:  opts,
		})
	}
	return out, nil
}

// appContextWindow 从一次 token usage notification 中提取模型上下文窗口大小（tokens）。
// 不同 codex 版本字段位置不同：顶层 modelContextWindow 或嵌在 tokenUsage 里都兼容。
// 返回 0 表示本次 notification 没带窗口信息，调用方按 fallback 策略处理。
func appContextWindow(n appNotification) int {
	if n.ModelContextWindow > 0 {
		return n.ModelContextWindow
	}
	if n.Usage != nil && n.Usage.ModelContextWindow > 0 {
		return n.Usage.ModelContextWindow
	}
	return 0
}

func appUsageToProvider(u *appTokenUsage) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	last := u.Last
	return provider.Usage{
		PromptTokens:     last.InputTokens,
		CompletionTokens: last.OutputTokens,
		ReasoningTokens:  last.ReasoningOutputTokens,
		CachedTokens:     last.CachedInputTokens,
		TotalTokens:      last.TotalTokens,
	}
}

func appTurnErr(turn *appTurn) error {
	if turn == nil {
		return nil
	}
	if turn.Error == nil {
		if turn.Status == appStatusFailed {
			return errors.New("codex: turn failed without error details")
		}
		return nil
	}
	message := sanitizeDiagnostic(turn.Error.Message)
	details := sanitizeDiagnostic(turn.Error.AdditionalDetails)
	if turn.Error.AdditionalDetails != "" {
		return fmt.Errorf("codex: %s: %s", message, details)
	}
	return fmt.Errorf("codex: %s", message)
}

var appRetryCountRE = regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)

func appRetryEvent(n appNotification) *RetryEvent {
	retry := &RetryEvent{}
	if n.Error != nil {
		retry.Message = sanitizeDiagnostic(n.Error.Message)
		retry.AdditionalDetails = sanitizeDiagnostic(n.Error.AdditionalDetails)
	}
	if m := appRetryCountRE.FindStringSubmatch(retry.Message); len(m) == 3 {
		if attempt, err := strconv.Atoi(m[1]); err == nil {
			retry.Attempt = attempt
		}
		if maxAttempts, err := strconv.Atoi(m[2]); err == nil {
			retry.MaxAttempts = maxAttempts
		}
	}
	return retry
}

func toolNameForItem(item *appThreadItem) string {
	if item == nil {
		return ""
	}
	switch item.Type {
	case appItemCommandExecution:
		return appToolCommandExecution
	case appItemFileChange:
		return appToolFileChange
	case appItemMCPToolCall:
		if item.Server == "" {
			return item.Tool
		}
		return "mcp." + item.Server + "." + item.Tool
	case appItemDynamicToolCall:
		if item.Namespace == "" {
			return item.Tool
		}
		return item.Namespace + "." + item.Tool
	case appItemCollabAgentTool:
		return "subagent." + item.Tool
	default:
		return item.Type
	}
}

func toolSourceForItem(item *appThreadItem) ToolSource {
	if item == nil {
		return ToolSourceUnknown
	}
	if item.Type == appItemMCPToolCall {
		return ToolSourceMCP
	}
	switch item.Type {
	case appItemCommandExecution, appItemFileChange, appItemDynamicToolCall, appItemCollabAgentTool:
		return ToolSourceBuiltin
	default:
		return ToolSourceUnknown
	}
}

func toolInputForItem(item *appThreadItem) json.RawMessage {
	if item == nil {
		return nil
	}
	switch item.Type {
	case appItemCommandExecution:
		return mustRaw(map[string]any{"command": item.Command, "cwd": item.Cwd})
	case appItemMCPToolCall, appItemDynamicToolCall:
		return item.Arguments
	default:
		if len(item.Arguments) > 0 {
			return item.Arguments
		}
		if len(item.Raw) > 0 {
			return item.Raw
		}
		return mustRaw(item)
	}
}

func toolResponseForItem(item *appThreadItem) json.RawMessage {
	if item == nil {
		return nil
	}
	switch item.Type {
	case appItemCommandExecution:
		out := map[string]any{"output": item.AggregatedOutput, "status": item.Status}
		if item.ExitCode != nil {
			out["exitCode"] = *item.ExitCode
		}
		return mustRaw(out)
	case appItemMCPToolCall, appItemDynamicToolCall:
		if len(item.Result) > 0 {
			return item.Result
		}
	case appItemFileChange:
		return mustRaw(map[string]any{"status": item.Status, "changes": item.Changes})
	}
	if len(item.Result) > 0 {
		return item.Result
	}
	if len(item.Raw) > 0 {
		return item.Raw
	}
	return mustRaw(item)
}

func toolErrForItem(item *appThreadItem) error {
	if item == nil {
		return nil
	}
	if item.Error != nil && item.Error.Message != "" {
		return fmt.Errorf("codex tool error: %s", item.Error.Message)
	}
	if item.Status == appStatusFailed {
		return fmt.Errorf("codex tool failed")
	}
	return nil
}

func mustRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
