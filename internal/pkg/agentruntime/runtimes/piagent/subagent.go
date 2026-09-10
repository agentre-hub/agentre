package piagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

type subagentMode string

const (
	subagentModeSingle   subagentMode = "single"
	subagentModeParallel subagentMode = "parallel"
	subagentModeChain    subagentMode = "chain"
)

type envelopeKind string

const (
	envelopePending  envelopeKind = "single-envelope-pending"
	envelopeOfficial envelopeKind = "official"
	envelopeFlat     envelopeKind = "flat-single"
)

type invocationRun struct {
	ID             string
	Index          int
	Agent          string
	Profile        string
	Task           string
	RequestedModel string
}

type subagentInvocation struct {
	Mode                  subagentMode
	Runs                  []invocationRun
	Envelope              envelopeKind
	AgentScope            string
	ConfirmProjectAgents  bool
	projectConfirmEnabled bool
}

type subagentSelector struct {
	classify   func([]byte) (subagentInvocation, bool)
	newTracker func(string, subagentInvocation) *subagentTracker
}

var defaultSubagentSelector = subagentSelector{
	classify:   classifySubagentInvocation,
	newTracker: newSubagentTracker,
}

func (s subagentSelector) selectCandidate(name, outerToolCallID string, input []byte) (*subagentTracker, *canonical.AgentSpawn) {
	if !strings.Contains(strings.ToLower(name), "subagent") {
		return nil, nil
	}
	inv, ok := s.classify(input)
	if !ok {
		return nil, nil
	}
	tracker := s.newTracker(outerToolCallID, inv)
	spawn := tracker.spawn()
	return tracker, &spawn
}

func classifySubagentInvocation(input []byte) (subagentInvocation, bool) {
	var fields map[string]json.RawMessage
	if len(bytes.TrimSpace(input)) == 0 || json.Unmarshal(input, &fields) != nil || fields == nil {
		return subagentInvocation{}, false
	}

	agent, agentPresent, ok := readOptionalString(fields, "agent")
	if !ok {
		return subagentInvocation{}, false
	}
	task, taskPresent, ok := readOptionalString(fields, "task")
	if !ok {
		return subagentInvocation{}, false
	}
	profile, profilePresent, ok := readOptionalString(fields, "profile")
	if !ok {
		return subagentInvocation{}, false
	}
	model, _, ok := readOptionalString(fields, "model")
	if !ok {
		return subagentInvocation{}, false
	}
	if _, _, ok = readOptionalString(fields, "thinking"); !ok {
		return subagentInvocation{}, false
	}
	if _, _, ok = readOptionalString(fields, "cwd"); !ok {
		return subagentInvocation{}, false
	}

	tasks, tasksPresent, ok := readInvocationRuns(fields, "tasks", 8)
	if !ok {
		return subagentInvocation{}, false
	}
	chain, chainPresent, ok := readInvocationRuns(fields, "chain", 0)
	if !ok {
		return subagentInvocation{}, false
	}

	agentScope, scopePresent, ok := readOptionalString(fields, "agentScope")
	if !ok {
		return subagentInvocation{}, false
	}
	if scopePresent && agentScope != "user" && agentScope != "project" && agentScope != "both" {
		return subagentInvocation{}, false
	}
	confirmProjectAgents := true
	confirmPresent := false
	if raw, exists := fields["confirmProjectAgents"]; exists {
		confirmPresent = true
		if isJSONNull(raw) || json.Unmarshal(raw, &confirmProjectAgents) != nil {
			return subagentInvocation{}, false
		}
	}

	agent = strings.TrimSpace(agent)
	task = strings.TrimSpace(task)
	profile = strings.TrimSpace(profile)
	model = strings.TrimSpace(model)
	agentActive := agentPresent && taskPresent && agent != "" && task != ""
	parallelActive := tasksPresent && len(tasks) > 0
	chainActive := chainPresent && len(chain) > 0
	activeOfficial := 0
	for _, active := range []bool{agentActive, parallelActive, chainActive} {
		if active {
			activeOfficial++
		}
	}
	if activeOfficial > 1 {
		return subagentInvocation{}, false
	}

	inv := subagentInvocation{
		AgentScope:            agentScope,
		ConfirmProjectAgents:  confirmProjectAgents,
		projectConfirmEnabled: (agentScope == "project" || agentScope == "both") && (!confirmPresent || confirmProjectAgents),
	}
	switch {
	case agentActive:
		inv.Mode = subagentModeSingle
		inv.Envelope = envelopePending
		inv.Runs = []invocationRun{{ID: "run-0", Agent: agent, Task: task, RequestedModel: model}}
	case parallelActive:
		inv.Mode = subagentModeParallel
		inv.Envelope = envelopeOfficial
		inv.Runs = tasks
	case chainActive:
		inv.Mode = subagentModeChain
		inv.Envelope = envelopeOfficial
		inv.Runs = chain
	case taskPresent && profilePresent && task != "" && profile != "":
		inv.Mode = subagentModeSingle
		inv.Envelope = envelopeFlat
		inv.Runs = []invocationRun{{ID: "run-0", Profile: profile, Task: task, RequestedModel: model}}
	default:
		return subagentInvocation{}, false
	}
	return inv, true
}

func readOptionalString(fields map[string]json.RawMessage, key string) (string, bool, bool) {
	raw, exists := fields[key]
	if !exists {
		return "", false, true
	}
	var value string
	if isJSONNull(raw) || json.Unmarshal(raw, &value) != nil {
		return "", true, false
	}
	return value, true, true
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func readInvocationRuns(fields map[string]json.RawMessage, key string, limit int) ([]invocationRun, bool, bool) {
	raw, exists := fields[key]
	if !exists {
		return nil, false, true
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, true, false
	}
	if limit > 0 && len(items) > limit {
		return nil, true, false
	}
	runs := make([]invocationRun, 0, len(items))
	for index, item := range items {
		var entry map[string]json.RawMessage
		if json.Unmarshal(item, &entry) != nil || entry == nil {
			return nil, true, false
		}
		agent, presentAgent, ok := readOptionalString(entry, "agent")
		if !ok {
			return nil, true, false
		}
		task, presentTask, ok := readOptionalString(entry, "task")
		if !ok {
			return nil, true, false
		}
		model, _, modelOK := readOptionalString(entry, "model")
		if !modelOK {
			return nil, true, false
		}
		if _, _, thinkingOK := readOptionalString(entry, "thinking"); !thinkingOK {
			return nil, true, false
		}
		if _, _, cwdOK := readOptionalString(entry, "cwd"); !cwdOK {
			return nil, true, false
		}
		agent = strings.TrimSpace(agent)
		task = strings.TrimSpace(task)
		model = strings.TrimSpace(model)
		if !presentAgent || !presentTask || agent == "" || task == "" {
			return nil, true, false
		}
		runs = append(runs, invocationRun{
			ID: fmt.Sprintf("run-%d", index), Index: index,
			Agent: agent, Task: task, RequestedModel: model,
		})
	}
	return runs, true, true
}

type trackedRun struct {
	info              agentruntime.SubagentRun
	emittedCalls      map[string]string
	emittedResults    map[string]bool
	pendingResults    map[string]trackedResult
	lastAssistantText string
	activity          bool
	totalTokens       int
	// attemptError 标记 Status/ErrorMessage 当前来自快照里一条「本次模型请求失败」的
	// assistant 消息（stopReason=error / errorMessage）。pi 记完这条就会自动重试，所以
	// 它是可撤销的：见 clearSupersededAttemptError。
	attemptError bool
}

type trackedResult struct {
	content string
	isError bool
}

type subagentTracker struct {
	outerToolCallID   string
	invocation        subagentInvocation
	envelope          envelopeKind
	runs              []trackedRun
	activity          bool
	aggregateOverride string
}

func newSubagentTracker(outerToolCallID string, inv subagentInvocation) *subagentTracker {
	t := &subagentTracker{outerToolCallID: outerToolCallID, invocation: inv, envelope: inv.Envelope}
	t.runs = make([]trackedRun, len(inv.Runs))
	for i, run := range inv.Runs {
		status := "running"
		if inv.Mode == subagentModeParallel || inv.Mode == subagentModeChain && i > 0 {
			status = "waiting"
		}
		t.runs[i] = trackedRun{
			info: agentruntime.SubagentRun{
				ID: run.ID, Index: run.Index, Agent: run.Agent, Profile: run.Profile,
				Task: run.Task, RequestedModel: run.RequestedModel, Status: status,
			},
			emittedCalls:   make(map[string]string),
			emittedResults: make(map[string]bool),
			pendingResults: make(map[string]trackedResult),
		}
	}
	return t
}

func (t *subagentTracker) spawn() canonical.AgentSpawn {
	runs := make([]canonical.AgentSpawnRun, len(t.invocation.Runs))
	for i, run := range t.invocation.Runs {
		runs[i] = canonical.AgentSpawnRun{
			ID: run.ID, Index: run.Index, Agent: run.Agent, Profile: run.Profile,
			Task: run.Task, RequestedModel: run.RequestedModel,
		}
	}
	first := t.invocation.Runs[0]
	return canonical.AgentSpawn{
		SubagentType:    first.Agent,
		TaskDescription: first.Task,
		Prompt:          first.Task,
		Model:           first.RequestedModel,
		Mode:            string(t.invocation.Mode),
		Runs:            runs,
		Status:          "running",
	}
}

func (t *subagentTracker) info() agentruntime.SubagentInfo {
	runs := make([]agentruntime.SubagentRun, len(t.runs))
	toolUses := 0
	totalTokens := 0
	lastToolName := ""
	for i := range t.runs {
		runs[i] = t.runs[i].info
		toolUses += runs[i].ToolUses
		totalTokens += t.runs[i].totalTokens
		if runs[i].LastToolName != "" {
			lastToolName = runs[i].LastToolName
		}
	}
	first := runs[0]
	status := t.aggregateStatus(runs)
	if t.aggregateOverride != "" {
		status = t.aggregateOverride
	}
	return agentruntime.SubagentInfo{
		SubagentType:    first.Agent,
		TaskDescription: first.Task,
		Prompt:          first.Task,
		LastToolName:    lastToolName,
		ToolUses:        toolUses,
		TotalTokens:     totalTokens,
		Status:          status,
		Mode:            string(t.invocation.Mode),
		Runs:            runs,
	}
}

func (t *subagentTracker) aggregateStatus(runs []agentruntime.SubagentRun) string {
	if len(runs) == 0 {
		return "unknown"
	}
	if len(runs) == 1 {
		return runs[0].Status
	}
	for _, run := range runs {
		if !isTerminalStatus(run.Status) {
			return "running"
		}
	}

	has := func(status string) bool {
		for _, run := range runs {
			if run.Status == status {
				return true
			}
		}
		return false
	}
	all := func(status string) bool {
		for _, run := range runs {
			if run.Status != status {
				return false
			}
		}
		return true
	}

	switch t.invocation.Mode {
	case subagentModeParallel:
		switch {
		case has("canceled"):
			return "canceled"
		case has("unknown"):
			return "unknown"
		case all("completed"):
			return "completed"
		case all("failed"):
			return "failed"
		case has("completed") && has("failed"):
			return "partial"
		default:
			return "unknown"
		}
	case subagentModeChain:
		switch {
		case has("canceled"):
			return "canceled"
		case has("failed"):
			return "failed"
		case has("unknown"):
			return "unknown"
		case all("completed"):
			return "completed"
		default:
			return "unknown"
		}
	default:
		return runs[0].Status
	}
}

func (t *subagentTracker) consumeUpdate(partialResult []byte) ([]agentruntime.Event, bool) {
	details, ok := unwrapPartialDetails(partialResult)
	if !ok {
		return nil, false
	}
	return t.consumeDetails(details, false, false)
}

func (t *subagentTracker) consumeFinal(details []byte, outerError bool, _ string) ([]agentruntime.Event, bool) {
	return t.consumeDetails(details, true, outerError)
}

func (t *subagentTracker) abort() bool {
	before := t.info()
	canceled := false
	for index := range t.runs {
		if !isTerminalStatus(t.runs[index].info.Status) {
			t.runs[index].info.Status = "canceled"
			canceled = true
		}
	}
	if canceled {
		t.aggregateOverride = "canceled"
	}
	return !reflect.DeepEqual(before, t.info())
}

func (t *subagentTracker) finishIncomplete(turnFailed bool) bool {
	before := t.info()
	incomplete := false
	for index := range t.runs {
		if !isTerminalStatus(t.runs[index].info.Status) {
			t.runs[index].info.Status = "unknown"
			incomplete = true
		}
	}
	if turnFailed && incomplete {
		t.aggregateOverride = "failed"
	}
	return !reflect.DeepEqual(before, t.info())
}

func unwrapPartialDetails(partial []byte) ([]byte, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(partial, &object) != nil || object == nil {
		return nil, false
	}
	details, ok := object["details"]
	return details, ok
}

type decodedSnapshot struct {
	messages      []json.RawMessage
	exitCode      *int
	stopReason    string
	status        string
	model         string
	agentSource   string
	errorMessage  string
	summary       string
	totalTokens   int
	emptyOfficial bool
}

func (t *subagentTracker) consumeDetails(details []byte, final, outerError bool) ([]agentruntime.Event, bool) {
	before := t.info()
	snapshots, usable := t.decode(details)
	events := make([]agentruntime.Event, 0)

	for _, message := range orderedSnapshotMessages(snapshots, len(t.runs)) {
		run := &t.runs[message.runIndex]
		run.clearSupersededAttemptError()
		messageEvents, activity := t.applyMessage(run, message.raw, final)
		if activity {
			t.markRunActive(run)
			t.activity = true
		}
		events = append(events, messageEvents...)
	}
	for index, snapshot := range snapshots {
		if index >= len(t.runs) {
			break
		}
		t.applySnapshotMetadata(index, snapshot, final)
	}

	if final {
		t.finalize(snapshots, usable, outerError)
	}
	after := t.info()
	return events, !reflect.DeepEqual(before, after)
}

type orderedSnapshotMessage struct {
	runIndex     int
	messageIndex int
	raw          json.RawMessage
	timestamp    float64
	hasTimestamp bool
}

func orderedSnapshotMessages(snapshots []decodedSnapshot, runCount int) []orderedSnapshotMessage {
	limit := min(len(snapshots), runCount)
	queues := make([][]orderedSnapshotMessage, limit)
	for runIndex := 0; runIndex < limit; runIndex++ {
		queues[runIndex] = make([]orderedSnapshotMessage, len(snapshots[runIndex].messages))
		for messageIndex, raw := range snapshots[runIndex].messages {
			timestamp, ok := messageTimestamp(raw)
			queues[runIndex][messageIndex] = orderedSnapshotMessage{
				runIndex: runIndex, messageIndex: messageIndex, raw: raw,
				timestamp: timestamp, hasTimestamp: ok,
			}
		}
	}

	cursors := make([]int, limit)
	ordered := make([]orderedSnapshotMessage, 0)
	for {
		chosen := -1
		for runIndex := range queues {
			if cursors[runIndex] >= len(queues[runIndex]) {
				continue
			}
			if chosen < 0 || snapshotMessageBefore(queues[runIndex][cursors[runIndex]], queues[chosen][cursors[chosen]]) {
				chosen = runIndex
			}
		}
		if chosen < 0 {
			return ordered
		}
		ordered = append(ordered, queues[chosen][cursors[chosen]])
		cursors[chosen]++
	}
}

func snapshotMessageBefore(left, right orderedSnapshotMessage) bool {
	if left.hasTimestamp && right.hasTimestamp && left.timestamp != right.timestamp {
		return left.timestamp < right.timestamp
	}
	if left.runIndex != right.runIndex {
		return left.runIndex < right.runIndex
	}
	return left.messageIndex < right.messageIndex
}

func messageTimestamp(raw json.RawMessage) (float64, bool) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || message == nil {
		return 0, false
	}
	rawTimestamp := message["timestamp"]
	var number json.Number
	if json.Unmarshal(rawTimestamp, &number) == nil {
		value, err := strconv.ParseFloat(number.String(), 64)
		return value, err == nil
	}
	var text string
	if json.Unmarshal(rawTimestamp, &text) != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

func (t *subagentTracker) decode(details []byte) ([]decodedSnapshot, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(details, &object) != nil || object == nil {
		return nil, false
	}

	resultsRaw, resultsPresent := object["results"]
	messagesRaw, messagesPresent := object["messages"]
	results, resultsOK := rawArray(resultsRaw)
	messages, messagesOK := rawArray(messagesRaw)
	mode, modePresent, modeOK := readSnapshotMode(object)

	if t.envelope == envelopePending {
		switch {
		case resultsPresent && !messagesPresent && resultsOK && modeOK && (!modePresent || mode == "single"):
			t.envelope = envelopeOfficial
		case messagesPresent && !resultsPresent && messagesOK:
			t.envelope = envelopeFlat
		default:
			return nil, false
		}
	}

	switch t.envelope {
	case envelopeOfficial:
		if !resultsOK || !modeOK || modePresent && mode != string(t.invocation.Mode) {
			return nil, false
		}
		if len(results) == 0 {
			return []decodedSnapshot{{emptyOfficial: true}}, true
		}
		out := make([]decodedSnapshot, 0, len(results))
		for _, raw := range results {
			out = append(out, decodeSnapshotObject(raw, "messages"))
		}
		return out, true
	case envelopeFlat:
		if !messagesOK {
			return nil, false
		}
		snapshot := decodeSnapshotMap(object, messages)
		return []decodedSnapshot{snapshot}, true
	default:
		return nil, false
	}
}

func readSnapshotMode(object map[string]json.RawMessage) (string, bool, bool) {
	raw, exists := object["mode"]
	if !exists {
		return "", false, true
	}
	var mode string
	if json.Unmarshal(raw, &mode) != nil {
		return "", true, false
	}
	return mode, true, true
}

func rawArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	return values, true
}

func decodeSnapshotObject(raw json.RawMessage, messagesKey string) decodedSnapshot {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return decodedSnapshot{}
	}
	messages, _ := rawArray(object[messagesKey])
	return decodeSnapshotMap(object, messages)
}

func decodeSnapshotMap(object map[string]json.RawMessage, messages []json.RawMessage) decodedSnapshot {
	snapshot := decodedSnapshot{messages: messages}
	snapshot.exitCode = readOptionalInt(object["exitCode"])
	snapshot.stopReason = readTrimmedString(object["stopReason"])
	snapshot.status = readTrimmedString(object["status"])
	snapshot.model = readTrimmedString(object["model"])
	snapshot.agentSource = readAgentSource(object["agentSource"])
	snapshot.errorMessage = firstNonEmpty(
		readTrimmedString(object["errorMessage"]),
		readTrimmedString(object["error"]),
	)
	snapshot.summary = firstNonEmpty(
		readTrimmedString(object["summary"]),
		readTrimmedString(object["output"]),
	)
	snapshot.totalTokens = readUsageTotalTokens(object["usage"])
	return snapshot
}

func readOptionalInt(raw json.RawMessage) *int {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil
	}
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

func readTrimmedString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func readAgentSource(raw json.RawMessage) string {
	switch source := readTrimmedString(raw); source {
	case "user", "project":
		return source
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// readUsageTotalTokens 从子代理 details 的 usage 对象里提取已消耗的上下文大小。
// pi 侧的 subagent 工具（如 dev-kit）把每轮 message_end 的 totalTokens（= 该轮
// 语境窗口大小，随累计历史增长）记录为 usage.contextTokens，取最新一帧的值即
// 子代理当前消耗的上下文大小。不累加 input/output/cache*：那些是逐调用 token
// 量，跨轮求和会把每轮重发的历史重复计数，数值虚高（真实卡片应像 Claude 一样
// 显示已消耗的上下文量级）。缺省/畸形字段按 0 处理，由调用方决定是否覆盖。
func readUsageTotalTokens(raw json.RawMessage) int {
	if len(raw) == 0 || isJSONNull(raw) {
		return 0
	}
	var usage struct {
		ContextTokens int `json:"contextTokens"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return 0
	}
	return usage.ContextTokens
}

func (t *subagentTracker) applySnapshotMetadata(index int, snapshot decodedSnapshot, final bool) {
	run := &t.runs[index]
	if snapshotHasActivity(snapshot) {
		t.markRunActive(run)
		t.activity = true
	}
	if snapshot.agentSource != "" && run.info.AgentSource == "" {
		run.info.AgentSource = snapshot.agentSource
	}
	if snapshot.model != "" && run.info.Model == "" {
		run.info.Model = snapshot.model
	}
	// usage 快照单调递增，但缺失/异常帧会解成 0；0 不覆盖已记录的累计值（与
	// chat_svc SubagentProgress/Done 的 R4 守卫同一约定）。
	if snapshot.totalTokens > 0 {
		run.totalTokens = snapshot.totalTokens
	}
	t.applyStopReason(run, snapshot.stopReason)
	if snapshot.stopReason == "" && (snapshot.status == "failed" || snapshot.status == "canceled") {
		run.info.Status = snapshot.status
	}
	if snapshot.errorMessage != "" {
		run.info.ErrorMessage = snapshot.errorMessage
		if final {
			run.info.Status = "failed"
		}
	}
	if final {
		if snapshot.summary != "" {
			run.info.Summary = snapshot.summary
		} else if run.lastAssistantText != "" {
			run.info.Summary = run.lastAssistantText
		}
	}
}

func snapshotHasActivity(snapshot decodedSnapshot) bool {
	if snapshot.emptyOfficial || len(snapshot.messages) > 0 {
		return len(snapshot.messages) > 0
	}
	statusActivity := snapshot.status != "" && snapshot.status != "waiting" && snapshot.status != "pending"
	if snapshot.model != "" || snapshot.stopReason != "" || statusActivity ||
		snapshot.errorMessage != "" || snapshot.summary != "" {
		return true
	}
	return snapshot.exitCode != nil && *snapshot.exitCode != -1
}

// recordAttemptError 收下快照里一条「本次模型请求失败」的 assistant 消息。pi 记完它就会
// 自动重试，所以在外层 subagent 工具真正收口(final)之前它只是诊断信息，不能落成 run 终态
// ——否则 "failed" 进了 isTerminalStatus，后续 stopReason=toolUse 的恢复路径被挡死，卡片
// 会一直显示 FAILED + 该错误文案，底下却还在持续调工具。
func (r *trackedRun) recordAttemptError(errorMessage string, final bool) {
	if errorMessage != "" {
		r.info.ErrorMessage = errorMessage
	}
	r.attemptError = true
	if final {
		r.info.Status = "failed"
	}
}

// clearSupersededAttemptError 在应用同一 run 的下一条消息前撤销上一条尝试错误。快照每帧
// 全量重放，所以「错误消息后面还有本 run 的消息」就等价于「pi 已经重试并继续下去了」，
// 这条错误既不该定状态，也不该继续占着卡片摘要。
func (r *trackedRun) clearSupersededAttemptError() {
	if !r.attemptError {
		return
	}
	r.attemptError = false
	r.info.ErrorMessage = ""
	if r.info.Status == "failed" {
		r.info.Status = "running"
	}
}

func (t *subagentTracker) markRunActive(run *trackedRun) {
	run.activity = true
	if run.info.Status == "waiting" {
		run.info.Status = "running"
	}
}

func (t *subagentTracker) applyMessage(run *trackedRun, raw json.RawMessage, final bool) ([]agentruntime.Event, bool) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || message == nil {
		return nil, false
	}
	role := readTrimmedString(message["role"])
	switch role {
	case "assistant":
		observedModel := readTrimmedString(message["model"])
		if observedModel != "" && run.info.Model == "" {
			run.info.Model = observedModel
		}
		stopReason := readTrimmedString(message["stopReason"])
		errorMessage := readTrimmedString(message["errorMessage"])
		content, ok := rawArray(message["content"])
		if !ok {
			content = nil
		}
		var events []agentruntime.Event
		var texts []string
		for _, rawContent := range content {
			var item map[string]json.RawMessage
			if json.Unmarshal(rawContent, &item) != nil || item == nil {
				continue
			}
			switch readTrimmedString(item["type"]) {
			case "toolCall":
				id := readTrimmedString(item["id"])
				name := readTrimmedString(item["name"])
				if id == "" || name == "" {
					continue
				}
				input := objectRaw(item["arguments"])
				callEvents := t.emitCall(run, id, name, input)
				events = append(events, callEvents...)
			case "text":
				if text := readRawString(item["text"]); text != "" {
					texts = append(texts, text)
				}
			}
		}
		if len(texts) > 0 {
			run.lastAssistantText = strings.Join(texts, "")
			if final {
				run.info.Summary = run.lastAssistantText
			}
		}
		if errorMessage != "" || stopReason == "error" {
			run.recordAttemptError(errorMessage, final)
		} else {
			t.applyStopReason(run, stopReason)
		}
		return events, len(content) > 0 || stopReason != "" || observedModel != "" || errorMessage != ""
	case "toolResult":
		id := readTrimmedString(message["toolCallId"])
		if id == "" {
			return nil, false
		}
		content, valid := toolResultContent(message["content"])
		if !valid {
			return nil, false
		}
		var isError bool
		_ = json.Unmarshal(message["isError"], &isError)
		return t.emitOrHoldResult(run, id, trackedResult{content: content, isError: isError}), true
	default:
		return nil, false
	}
}

func readRawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func objectRaw(raw json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func toolResultContent(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || isJSONNull(raw) {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	items, ok := rawArray(raw)
	if !ok {
		return "", false
	}
	parts := make([]string, 0, len(items))
	for _, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil || item == nil || readTrimmedString(item["type"]) != "text" {
			continue
		}
		parts = append(parts, readRawString(item["text"]))
	}
	return strings.Join(parts, ""), true
}

func (t *subagentTracker) emitCall(run *trackedRun, innerID, name string, input json.RawMessage) []agentruntime.Event {
	if _, exists := run.emittedCalls[innerID]; exists {
		return nil
	}
	runtimeID := nestedToolCallID(t.outerToolCallID, run.info.ID, innerID)
	run.emittedCalls[innerID] = runtimeID
	run.info.ToolUses++
	run.info.LastToolName = name
	call := agentruntime.ToolCall{
		ID: runtimeID, Name: name, Input: input,
		Canonical:        recognizeCanonical(name, input),
		ParentToolCallID: t.outerToolCallID, SubagentRunID: run.info.ID,
	}
	events := []agentruntime.Event{call}
	if pending, ok := run.pendingResults[innerID]; ok {
		delete(run.pendingResults, innerID)
		events = append(events, t.emitResult(run, innerID, pending)...)
	}
	return events
}

func (t *subagentTracker) emitOrHoldResult(run *trackedRun, innerID string, result trackedResult) []agentruntime.Event {
	if run.emittedResults[innerID] {
		return nil
	}
	if _, exists := run.emittedCalls[innerID]; !exists {
		run.pendingResults[innerID] = result
		return nil
	}
	return t.emitResult(run, innerID, result)
}

func (t *subagentTracker) emitResult(run *trackedRun, innerID string, result trackedResult) []agentruntime.Event {
	if run.emittedResults[innerID] {
		return nil
	}
	run.emittedResults[innerID] = true
	return []agentruntime.Event{agentruntime.ToolResult{
		ToolCallID: run.emittedCalls[innerID], Content: result.content, IsError: result.isError,
		ParentToolCallID: t.outerToolCallID, SubagentRunID: run.info.ID,
	}}
}

func nestedToolCallID(outerToolCallID, runID, innerToolCallID string) string {
	return fmt.Sprintf("pi-subagent:%d:%s:%d:%s:%d:%s", len(outerToolCallID), outerToolCallID, len(runID), runID, len(innerToolCallID), innerToolCallID)
}

func (t *subagentTracker) applyStopReason(run *trackedRun, reason string) {
	switch reason {
	case "toolUse":
		if !isTerminalStatus(run.info.Status) {
			run.info.Status = "running"
		}
	case "stop", "length":
		run.info.Status = "completed"
	case "error":
		run.info.Status = "failed"
	case "aborted":
		if t.envelope == envelopeOfficial {
			run.info.Status = "failed"
		} else {
			run.info.Status = "canceled"
		}
	}
}

func (t *subagentTracker) finalize(snapshots []decodedSnapshot, usable, outerError bool) {
	if t.invocation.projectConfirmEnabled && t.envelope == envelopeOfficial && !t.activity && len(snapshots) == 1 && snapshots[0].emptyOfficial {
		for i := range t.runs {
			t.runs[i].info.Status = "canceled"
		}
		return
	}

	if t.invocation.Mode != subagentModeSingle {
		t.finalizeGrouped(snapshots, usable, outerError)
		return
	}

	for index := range t.runs {
		run := &t.runs[index]
		if !usable {
			if outerError {
				run.info.Status = "failed"
			} else {
				run.info.Status = "completed"
			}
			continue
		}
		var snapshot decodedSnapshot
		if index < len(snapshots) {
			snapshot = snapshots[index]
		}
		if terminal := finalSnapshotTerminalStatus(t.envelope, snapshot, run.info.Status); terminal != "" {
			run.info.Status = terminal
			continue
		}
		if isTerminalStatus(run.info.Status) {
			continue
		}
		if outerError {
			run.info.Status = "failed"
		} else {
			run.info.Status = "unknown"
		}
	}
}

func (t *subagentTracker) finalizeGrouped(snapshots []decodedSnapshot, usable, outerError bool) {
	incomplete := !usable
	for index := range t.runs {
		run := &t.runs[index]
		if usable && index < len(snapshots) {
			if terminal := finalSnapshotTerminalStatus(t.envelope, snapshots[index], run.info.Status); terminal != "" {
				run.info.Status = terminal
				continue
			}
		}
		if isTerminalStatus(run.info.Status) {
			continue
		}
		incomplete = true
	}

	if t.invocation.Mode == subagentModeChain {
		failedIndex := -1
		for index := range t.runs {
			if t.runs[index].info.Status == "failed" {
				failedIndex = index
				break
			}
		}
		if failedIndex >= 0 {
			for index := failedIndex + 1; index < len(t.runs); index++ {
				run := &t.runs[index]
				if !run.activity && !isTerminalStatus(run.info.Status) {
					run.info.Status = "skipped"
				}
			}
		}
	}

	for index := range t.runs {
		if !isTerminalStatus(t.runs[index].info.Status) {
			t.runs[index].info.Status = "unknown"
		}
	}
	if outerError && incomplete {
		t.aggregateOverride = "failed"
	}
}

func finalSnapshotTerminalStatus(envelope envelopeKind, snapshot decodedSnapshot, current string) string {
	status := terminalStatus(envelope, snapshot)
	if status != "completed" || snapshot.stopReason != "" || snapshot.status != "" || snapshot.errorMessage != "" ||
		snapshot.exitCode == nil || *snapshot.exitCode != 0 {
		return status
	}
	if current == "failed" || current == "canceled" {
		return ""
	}
	return status
}

func terminalStatus(envelope envelopeKind, snapshot decodedSnapshot) string {
	switch snapshot.stopReason {
	case "error":
		return "failed"
	case "aborted":
		if envelope == envelopeOfficial {
			return "failed"
		}
		return "canceled"
	}
	if snapshot.errorMessage != "" {
		return "failed"
	}
	switch snapshot.status {
	case "failed", "canceled":
		return snapshot.status
	}
	if snapshot.exitCode != nil && *snapshot.exitCode != 0 && *snapshot.exitCode != -1 {
		return "failed"
	}
	switch snapshot.stopReason {
	case "stop", "length":
		return "completed"
	}
	if snapshot.status == "completed" {
		return "completed"
	}
	if snapshot.exitCode != nil && *snapshot.exitCode == 0 {
		return "completed"
	}
	return ""
}

func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled", "skipped", "unknown":
		return true
	default:
		return false
	}
}
