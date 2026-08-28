package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/pkg/codex"
)

// 磁盘转录读取器住在本包,是为了复用 translate() —— 那是 codex.Event →
// agentruntime.Event 的唯一一份翻译。导入路径与线上路径共用它,工具卡与 canonical
// 文件变更块因此只有一套语义。
//
// codex 的磁盘方言(rollout 的 response_item / event_msg)与 codex app-server 的
// 实时 JSON-RPC 协议(pkg/codex/stream.go 的 appThreadItem 等)是两套完全不同的
// 序列化——不像 claude 磁盘 JSONL 与 CLI stdout 帧同形——所以这里没有可复用的
// pkg/codex 解码器,方言知识就地写在本文件。
func init() { transcriptimport.Register(transcriptSource{}) }

// transcriptSource 是 codex 在这台机器上的磁盘读取器。无状态,根目录每次现取
// (测试经 AGENTRE_CODEX_HOME_DIR 指向 fixture)。
type transcriptSource struct{}

func (transcriptSource) Backend() agent_backend_entity.BackendType {
	return agent_backend_entity.TypeCodex
}

// codexHome 是 codex CLI 的默认主目录,可用 AGENTRE_CODEX_HOME_DIR 覆盖。
func codexHome() string {
	if env := strings.TrimSpace(os.Getenv("AGENTRE_CODEX_HOME_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func sessionsRoot() string { return filepath.Join(codexHome(), "sessions") }

func sessionIndexPath() string { return filepath.Join(codexHome(), "session_index.jsonl") }

// sessionIndexEntry 是 ~/.codex/session_index.jsonl 一行:codex 自带的现成扫描索引。
// decision 19:扫描只读它 + rollout 文件的首行 session_meta,不解全文。
type sessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

// loadSessionIndex 读一遍 session_index.jsonl。缺失不致命(视为空索引,标题与结束
// 时间退回逐文件的兜底扫描)。
func loadSessionIndex() (map[string]sessionIndexEntry, error) {
	f, err := os.Open(sessionIndexPath()) // #nosec G304 -- path 由 codexHome 拼出
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]sessionIndexEntry{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[string]sessionIndexEntry{}
	sc := transcriptimport.NewRecordScanner(f)
	for sc.Scan() {
		var e sessionIndexEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == "" {
			continue
		}
		out[e.ID] = e
	}
	return out, sc.Err()
}

// Scan 只读元信息:文件名给 provider session id、session_index.jsonl 给标题与最后
// 活动时间、rollout 文件首行 session_meta 给 cwd/来源标记。不解全文 —— 本机 3 000
// 多个 rollout 文件,逐个解全文连对话框都打不开。
func (s transcriptSource) Scan(ctx context.Context, f transcriptimport.Filter) ([]transcriptimport.Candidate, error) {
	home := codexHome()
	if home == "" {
		return nil, nil
	}
	idx, err := loadSessionIndex()
	if err != nil {
		return nil, err
	}

	var out []transcriptimport.Candidate
	walkErr := filepath.WalkDir(sessionsRoot(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if cErr := ctx.Err(); cErr != nil {
			return cErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		id, ok := parseRolloutFilename(d.Name())
		if !ok {
			return nil
		}
		// 定位符是**相对 codex home 的**路径(resolveLocator 按它还原并挡越界)。
		// 遍历是从 sessionsRoot() 起步的,所以 path 恒在 home 之下;真落在外面
		// (符号链接之类)就跳过这一个文件,给不出定位符的候选点进去也打不开。
		rel, inHome := strings.CutPrefix(path, home+string(filepath.Separator))
		if !inHome {
			return nil
		}
		cand, ok := scanCandidate(path, id, filepath.ToSlash(rel), idx)
		if !ok || !f.Matches(cand) {
			return nil
		}
		out = append(out, cand)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndedAt.After(out[j].EndedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// parseRolloutFilename 从 rollout-<ts>-<uuid>.jsonl 里取出 uuid。时间戳前缀长度
// 不固定,但 uuid 固定 36 个字符,从尾部数更稳。
func parseRolloutFilename(name string) (string, bool) {
	base := strings.TrimSuffix(name, ".jsonl")
	if !strings.HasPrefix(base, "rollout-") || len(base) < 36 {
		return "", false
	}
	id := base[len(base)-36:]
	if strings.Count(id, "-") != 4 {
		return "", false
	}
	return id, true
}

// scanCandidate 只读文件首行(session_meta),标题与结束时间优先取 session_index;
// 索引没有这条时才退回读文件头若干行 / 回读文件尾(与 claude 读取器同一档次的
// 兜底,不是常规路径)。
func scanCandidate(path, id, rel string, idx map[string]sessionIndexEntry) (transcriptimport.Candidate, bool) {
	f, err := os.Open(path) // #nosec G304 -- path 由 sessionsRoot 遍历 + 目录项拼出
	if err != nil {
		return transcriptimport.Candidate{}, false
	}
	defer func() { _ = f.Close() }()

	sc := transcriptimport.NewRecordScanner(f)
	if !sc.Scan() {
		return transcriptimport.Candidate{}, false
	}
	var rec diskRecord
	if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "session_meta" {
		return transcriptimport.Candidate{}, false
	}
	var meta sessionMetaPayload
	_ = json.Unmarshal(rec.Payload, &meta)

	cand := transcriptimport.Candidate{
		Backend:           agent_backend_entity.TypeCodex,
		ProviderSessionID: id,
		Cwd:               meta.Cwd,
		Origin:            codexOrigin(meta.Originator),
		Locator:           transcriptimport.Locator(rel),
		StartedAt:         rec.time(),
	}
	if entry, ok := idx[id]; ok {
		if entry.ThreadName != "" {
			cand.Title = entry.ThreadName
		}
		if ts, err := time.Parse(time.RFC3339Nano, entry.UpdatedAt); err == nil {
			cand.EndedAt = ts
		}
	}
	if cand.EndedAt.IsZero() {
		cand.EndedAt = transcriptimport.LastRecordTime(f, cand.StartedAt, recordTime)
	}
	if cand.Title == "" {
		cand.Title = scanFallbackTitle(f)
	}
	return cand, true
}

// scanFallbackTitle 只在 session_index 没有这条候选时才会走到:读文件头若干行找
// 第一条 user_message。
func scanFallbackTitle(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	sc := transcriptimport.NewRecordScanner(f)
	for i := 0; i < transcriptimport.ScanHeadLines && sc.Scan(); i++ {
		var rec diskRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "event_msg" || rec.payloadType() != "user_message" {
			continue
		}
		var p userMessagePayload
		if json.Unmarshal(rec.Payload, &p) == nil && p.Message != "" {
			return transcriptimport.FirstLine(p.Message)
		}
	}
	return ""
}

// recordTime 是本包方言的"这一行的时间戳":回读文件尾时交给
// transcriptimport.LastRecordTime,解不开的行按没有时间戳处理。
func recordTime(line []byte) time.Time {
	var rec diskRecord
	if json.Unmarshal(line, &rec) != nil {
		return time.Time{}
	}
	return rec.time()
}

// codexOrigin 把 codex 的 originator 翻成来源标记。只用于展示 —— 判重一律以库里的
// provider_session_id 为准(decision 18)。
func codexOrigin(originator string) transcriptimport.Origin {
	switch strings.TrimSpace(originator) {
	case "":
		return transcriptimport.OriginUnknown
	case "agentre":
		return transcriptimport.OriginAgentre
	default:
		return transcriptimport.OriginTerminal
	}
}

// Open 把定位符变成一份可回放的转录。先走一趟索引(计数与缺口),正文留到 Turns
// 时逐行再解。
func (s transcriptSource) Open(_ context.Context, loc transcriptimport.Locator) (transcriptimport.Transcript, error) {
	path, err := resolveLocator(loc)
	if err != nil {
		return nil, err
	}
	idx, err := loadSessionIndex()
	if err != nil {
		return nil, err
	}
	meta, err := indexTranscript(path, idx)
	if err != nil {
		return nil, err
	}
	return &diskTranscript{path: path, meta: meta}, nil
}

// resolveLocator 把定位符还原成 codex home 内的绝对路径。根内解析与路径逃逸防护
// 是三个读取器共同的安全边界,判据在 transcriptimport;这里只补上本包的上下文。
//
// home 取不到时由共享判据先行拒绝 —— 本包此前的副本没有这道防护,靠 Open 另行
// 检查兜着,现在两处合成一处。
func resolveLocator(loc transcriptimport.Locator) (string, error) {
	abs, err := transcriptimport.ResolveLocator(codexHome(), loc)
	if err != nil {
		return "", fmt.Errorf("agentruntime/runtimes/codex: %w", err)
	}
	return abs, nil
}

// diskTranscript 是一份已索引的转录。Meta 在 Open 时就算好;正文每次 Turns 时重新
// 逐行读 —— 一份 42 轮 / 402 次工具调用的会话不该整个躺在内存里。
type diskTranscript struct {
	path string
	meta transcriptimport.Meta
}

func (t *diskTranscript) Meta() transcriptimport.Meta { return t.meta }

func (t *diskTranscript) Close() error { return nil }

func (t *diskTranscript) Turns(ctx context.Context, yield func(transcriptimport.Turn) error) error {
	f, err := os.Open(t.path) // #nosec G304 -- path 由 resolveLocator 校验过在 codex home 内
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	r := &turnReader{
		TurnAccumulator: transcriptimport.NewTurnAccumulator(yield),
		fallback:        t.meta.Model,
		consumedPatches: map[string]struct{}{},
	}
	sc := transcriptimport.NewRecordScanner(f)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec diskRecord
		if json.Unmarshal(line, &rec) != nil {
			continue // 坏行只丢那一行,已在 Open 时计入缺口
		}
		if err := r.consume(rec); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return r.Flush()
}

// diskRecord 是 rollout 文件一行记录的骨架:{timestamp,type,payload}。payload 的
// 具体形状按 type / payload.type 再解一层,不在这里重复一套。
type diskRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

func (r diskRecord) time() time.Time {
	if r.Timestamp == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// payloadType 只探 payload.type,不管 event_msg 与 response_item 各自其余字段。
// session_meta / turn_context / compacted 顶层 type 已经够用,没有这一层。
func (r diskRecord) payloadType() string {
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	return p.Type
}

type sessionMetaPayload struct {
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	Originator string `json:"originator"`
}

type turnContextPayload struct {
	TurnID string `json:"turn_id"`
	Model  string `json:"model"`
}

type taskStartedPayload struct {
	TurnID string `json:"turn_id"`
}

type turnAbortedPayload struct {
	Reason string `json:"reason"`
}

type userMessagePayload struct {
	Message string `json:"message"`
}

type agentMessagePayload struct {
	Message string `json:"message"`
}

// reasoningPayload 只取 summary 文本;summary 为空 + 只剩 encrypted_content 是
// Anthropic/OpenAI 加密思维链的常态,是缺口不是解析失败(decision 11)。
type reasoningPayload struct {
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

func (p reasoningPayload) text() string {
	var parts []string
	for _, s := range p.Summary {
		if strings.TrimSpace(s.Text) != "" {
			parts = append(parts, s.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type functionCallPayload struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type functionCallOutputPayload struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type customToolCallPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}

type customToolCallOutputPayload struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// patchApplyEndPayload 是 event_msg/patch_apply_end:文件补丁的落地结果,直接带
// unified_diff(update)或整文件 content(add/delete),能落到既有 canonical 文件
// 变更块上(decision 7)。
type patchApplyEndPayload struct {
	CallID  string                     `json:"call_id"`
	Success bool                       `json:"success"`
	Status  string                     `json:"status"`
	Changes map[string]patchFileChange `json:"changes"`
}

type patchFileChange struct {
	Type        string `json:"type"`
	UnifiedDiff string `json:"unified_diff"`
	Content     string `json:"content"`
}

type tokenCountPayload struct {
	Info struct {
		LastTokenUsage tokenUsage `json:"last_token_usage"`
	} `json:"info"`
}

type tokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

func (u tokenUsage) toProvider() provider.Usage {
	return provider.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		ReasoningTokens:  u.ReasoningOutputTokens,
		CachedTokens:     u.CachedInputTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// indexTranscript 走一趟文件,只留计数与缺口(不留正文)。codex 的 rollout 是线性
// 的(没有 claude 那种分支/leafUuid 问题),所以不需要先建链再筛选,顺序累计即可。
func indexTranscript(path string, sessionIndex map[string]sessionIndexEntry) (transcriptimport.Meta, error) {
	f, err := os.Open(path) // #nosec G304 -- path 由 resolveLocator 校验过在 codex home 内
	if err != nil {
		return transcriptimport.Meta{}, err
	}
	defer func() { _ = f.Close() }()

	var (
		meta = transcriptimport.Meta{Backend: agent_backend_entity.TypeCodex}

		badLines       int
		emptyThinking  int
		subagentSpawns int
		firstUserText  string
		openToolCalls  = map[string]struct{}{}
	)

	sc := transcriptimport.NewRecordScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec diskRecord
		if json.Unmarshal(line, &rec) != nil {
			badLines++
			continue
		}

		switch rec.Type {
		case "session_meta":
			var p sessionMetaPayload
			if json.Unmarshal(rec.Payload, &p) == nil {
				meta.ProviderSessionID = p.ID
				meta.Cwd = p.Cwd
				meta.Origin = codexOrigin(p.Originator)
			}
		case "turn_context":
			var p turnContextPayload
			if json.Unmarshal(rec.Payload, &p) == nil && p.Model != "" {
				meta.Model = p.Model
			}
		case "compacted":
			meta.Compactions++
		case "event_msg":
			switch rec.payloadType() {
			case "task_started":
				meta.Turns++
				if meta.StartedAt.IsZero() {
					meta.StartedAt = rec.time()
				}
			case "user_message":
				if firstUserText == "" {
					var p userMessagePayload
					if json.Unmarshal(rec.Payload, &p) == nil {
						firstUserText = transcriptimport.FirstLine(p.Message)
					}
				}
			case "patch_apply_end":
				var p patchApplyEndPayload
				if json.Unmarshal(rec.Payload, &p) == nil {
					meta.ToolCalls++
					delete(openToolCalls, p.CallID)
				}
			}
		case "response_item":
			switch rec.payloadType() {
			case "reasoning":
				var p reasoningPayload
				if json.Unmarshal(rec.Payload, &p) == nil && p.text() == "" {
					emptyThinking++
				}
			case "function_call":
				var p functionCallPayload
				if json.Unmarshal(rec.Payload, &p) == nil {
					meta.ToolCalls++
					openToolCalls[p.CallID] = struct{}{}
					if p.Name == "spawn_agent" {
						// codex 子代理有时另起自己的 rollout 文件,本任务不追进去,
						// 只声明缺口(见 spec 决策边界)。
						subagentSpawns++
					}
				}
			case "function_call_output":
				var p functionCallOutputPayload
				if json.Unmarshal(rec.Payload, &p) == nil {
					delete(openToolCalls, p.CallID)
				}
			case "custom_tool_call":
				var p customToolCallPayload
				// apply_patch 由 patch_apply_end 落地(decision 7),不在这里计数,
				// 避免和 patch_apply_end 重复计一次 ToolCalls。
				if json.Unmarshal(rec.Payload, &p) == nil && p.Name != "apply_patch" {
					meta.ToolCalls++
					openToolCalls[p.CallID] = struct{}{}
				}
			case "custom_tool_call_output":
				var p customToolCallOutputPayload
				if json.Unmarshal(rec.Payload, &p) == nil {
					delete(openToolCalls, p.CallID)
				}
			}
		}
		if ts := rec.time(); !ts.IsZero() {
			meta.EndedAt = ts
		}
	}
	if err := sc.Err(); err != nil {
		return transcriptimport.Meta{}, err
	}

	if entry, ok := sessionIndex[meta.ProviderSessionID]; ok && entry.ThreadName != "" {
		meta.Title = entry.ThreadName
	} else {
		meta.Title = firstUserText
	}
	meta.Gaps = transcriptimport.BuildGaps(emptyThinking, badLines, len(openToolCalls), subagentSpawns)
	return meta, nil
}

// turnReader 沿 turn_id 把记录切成轮次。轮号/挂起/收尾这组与后端无关的状态由内嵌的
// TurnAccumulator 持有,本包留下的是 codex 方言:什么算一轮的起点、补丁去重。
type turnReader struct {
	transcriptimport.TurnAccumulator

	// fallback 是这份转录的模型名(索引阶段从 turn_context 里读出来的那个)。
	// 建轮时先落它,轮内再读到 turn_context 就覆盖 —— 与 claude / pi 两个读取器
	// 同一条口径:turn_context 在有些 rollout 里写在 task_started **之前**
	// (会话级只写一次),轮内再也读不到它,不回落这一轮就没有模型名了。
	fallback string

	// consumedPatches 记录已经被 patch_apply_end 落成 canonical 文件变更块的
	// call_id;随后到达的 custom_tool_call_output 只是同一次调用的原始回执,
	// 跳过它避免同一次文件改动被落两遍(与 decision 8 的去重同一个道理)。
	consumedPatches map[string]struct{}
}

func (r *turnReader) consume(rec diskRecord) error {
	switch rec.Type {
	case "turn_context":
		var p turnContextPayload
		if json.Unmarshal(rec.Payload, &p) == nil && p.Model != "" {
			// 轮外读到的同样记下:它就是接下来那几轮的模型名。
			r.fallback = p.Model
			if cur := r.Cur(); cur != nil {
				cur.Model = p.Model
			}
		}
		return nil
	case "event_msg":
		return r.consumeEventMsg(rec)
	case "response_item":
		return r.consumeResponseItem(rec)
	default:
		// session_meta 只在 Open 的索引阶段用得上;compacted 的压缩次数同样只
		// 反映在 Meta 里 —— 磁盘上没有压缩前后的 token 数,伪造一条
		// CompactBoundary 事件不如干脆不生成。
		return nil
	}
}

func (r *turnReader) consumeEventMsg(rec diskRecord) error {
	switch rec.payloadType() {
	case "task_started":
		if err := r.Flush(); err != nil {
			return err
		}
		var p taskStartedPayload
		_ = json.Unmarshal(rec.Payload, &p)
		r.Begin(transcriptimport.Turn{
			ForkAnchor: p.TurnID,
			StartedAt:  rec.time(),
			EndedAt:    rec.time(),
			Model:      r.fallback,
		})
	case "user_message":
		var p userMessagePayload
		if json.Unmarshal(rec.Payload, &p) == nil && p.Message != "" {
			if cur := r.Cur(); cur != nil {
				if cur.UserText == "" {
					cur.UserText = p.Message
				} else {
					cur.UserText += "\n" + p.Message
				}
			}
		}
	case "agent_message":
		var p agentMessagePayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			r.emitEvent(codex.Event{Kind: codex.EventTextDelta, Text: p.Message})
		}
	case "token_count":
		var p tokenCountPayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			r.emitEvent(codex.Event{Kind: codex.EventUsage, Usage: p.Info.LastTokenUsage.toProvider()})
		}
	case "patch_apply_end":
		var p patchApplyEndPayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			r.consumedPatches[p.CallID] = struct{}{}
			r.emitPatch(p)
		}
	case "task_complete":
		r.Touch(rec.time())
		return r.Flush()
	case "turn_aborted":
		var p turnAbortedPayload
		_ = json.Unmarshal(rec.Payload, &p)
		if cur := r.Cur(); cur != nil {
			cur.ErrorText = p.Reason
			r.Touch(rec.time())
		}
		return r.Flush()
	}
	r.Touch(rec.time())
	return nil
}

func (r *turnReader) consumeResponseItem(rec diskRecord) error {
	switch rec.payloadType() {
	case "message":
		// decision 8:assistant 正文(以及用户话的回声)只认 event_msg 那条,
		// response_item/message 是同一段话的第二次记录,原样丢弃。
		return nil
	case "reasoning":
		var p reasoningPayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			if text := p.text(); text != "" {
				r.emitEvent(codex.Event{Kind: codex.EventThinkingDelta, Text: text})
			}
			// summary 为空、只剩 encrypted_content 的情形已经在 Open 的索引阶段
			// 计入 GapThinkingUnavailable,这里不重复计数。
		}
	case "function_call":
		var p functionCallPayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			r.emitEvent(codex.Event{Kind: codex.EventPreToolUse, Tool: &codex.ToolEvent{
				ID: p.CallID, Name: p.Name, Input: rawJSONOrWrap(p.Arguments),
			}})
		}
	case "function_call_output":
		var p functionCallOutputPayload
		if json.Unmarshal(rec.Payload, &p) == nil {
			r.emitEvent(codex.Event{Kind: codex.EventPostToolUse, Tool: &codex.ToolEvent{
				ID: p.CallID, Response: rawJSONOrWrap(p.Output),
			}})
		}
	case "custom_tool_call":
		var p customToolCallPayload
		if json.Unmarshal(rec.Payload, &p) == nil && p.Name != "apply_patch" {
			r.emitEvent(codex.Event{Kind: codex.EventPreToolUse, Tool: &codex.ToolEvent{
				ID: p.CallID, Name: p.Name, Input: rawJSONOrWrap(p.Input),
			}})
		}
	case "custom_tool_call_output":
		var p customToolCallOutputPayload
		// 与上面几支同一个写法:解得开才往下走,解不开这一条记录就当没有 ——
		// 坏行已经在 Open 的索引阶段计入缺口。
		if json.Unmarshal(rec.Payload, &p) == nil {
			if _, done := r.consumedPatches[p.CallID]; done {
				// 这次文件改动已经由 patch_apply_end 落成 canonical 变更块,
				// 原始回执跳过,免得同一次改动出现两遍。
				delete(r.consumedPatches, p.CallID)
				return nil
			}
			r.emitEvent(codex.Event{Kind: codex.EventPostToolUse, Tool: &codex.ToolEvent{
				ID: p.CallID, Response: rawJSONOrWrap(p.Output),
			}})
		}
	}
	return nil
}

// emitPatch 把一条 patch_apply_end 落成一对 ToolCall(Name="file_change",走
// translate() 的 canonical 识别)+ ToolResult。没有可用的文件改动就什么都不吐 ——
// 不伪造一个空的文件变更块。
func (r *turnReader) emitPatch(p patchApplyEndPayload) {
	input := fileChangeInput(p.Changes)
	if input == nil {
		return
	}
	r.emitEvent(codex.Event{Kind: codex.EventPreToolUse, Tool: &codex.ToolEvent{
		ID: p.CallID, Name: "file_change", Input: input,
	}})
	var callErr error
	if !p.Success {
		callErr = fmt.Errorf("codex: patch %s", p.Status)
	}
	r.emitEvent(codex.Event{Kind: codex.EventPostToolUse, Tool: &codex.ToolEvent{
		ID: p.CallID, Response: mustJSON(map[string]any{"status": p.Status, "success": p.Success}), Err: callErr,
	}})
}

// fileChangeInput 把 patch_apply_end.changes(绝对路径 → {type,unified_diff|content})
// 规整成 diff.FromFileChange 认识的 {"changes":[{"path","kind","diff"}]} 形状 ——
// 与 translator.go 的 recognizeCanonical("file_change", ...) 复用同一份识别。
func fileChangeInput(changes map[string]patchFileChange) json.RawMessage {
	if len(changes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(changes))
	for p := range changes {
		paths = append(paths, p)
	}
	sort.Strings(paths) // map 遍历无序,排序换取可重现的输出

	type change struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
		Diff string `json:"diff"`
	}
	list := make([]change, 0, len(paths))
	for _, p := range paths {
		c := changes[p]
		diffText := c.UnifiedDiff
		if diffText == "" {
			diffText = c.Content // add/delete 用整文件内容,不是 unified diff
		}
		list = append(list, change{Path: p, Kind: c.Type, Diff: diffText})
	}
	return mustJSON(map[string]any{"changes": list})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// rawJSONOrWrap 把磁盘上的字符串字段规整成合法 JSON:本来就是合法 JSON(比如
// function_call.arguments 那种序列化过的参数对象)就原样透传;不是(比如
// custom_tool_call.input 那种模型直接写的自由文本 / 代码)就包成 JSON 字符串,
// 保证 ToolCall.Input / ToolResult.Content 永远是合法 JSON 字节。
func rawJSONOrWrap(s string) json.RawMessage {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return mustJSON(s)
}

func (r *turnReader) emitEvent(ev codex.Event) {
	events, usage, stopErr := translate(ev)
	r.Emit(events...)
	cur := r.Cur()
	if cur == nil {
		return
	}
	if usage != nil {
		cur.Usage = usage
	}
	if stopErr != nil {
		cur.ErrorText = stopErr.Error()
	}
}

// 编译期确认契约实现完整。
var (
	_ transcriptimport.Source     = transcriptSource{}
	_ transcriptimport.Transcript = (*diskTranscript)(nil)
)
