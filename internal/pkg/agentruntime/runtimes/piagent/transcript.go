package piagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

// 磁盘转录读取器住在本包,是为了复用 translate() 与既有的子代理 tracker
// (handleSubagentToolEvent / subagentTracker.consumeFinal) —— pi 的
// toolResult.details.messages 内联了子代理消息,线上路径本就吃这个字段,这里不
// 另写第二份解释。
func init() { transcriptimport.Register(transcriptSource{}) }

// transcriptSource 是 pi 在这台机器上的磁盘读取器。无状态,根目录每次现取
// (测试经 AGENTRE_PI_SESSIONS_DIR 指向 fixture)。
type transcriptSource struct{}

func (transcriptSource) Backend() agent_backend_entity.BackendType {
	return agent_backend_entity.TypePiAgent
}

// sessionsRoot 是 pi CLI 的默认会话根目录,可经 AGENTRE_PI_SESSIONS_DIR 覆盖。
func sessionsRoot() string {
	if env := strings.TrimSpace(os.Getenv("AGENTRE_PI_SESSIONS_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// Scan 只读元信息:目录项 + 文件头若干行 + 文件尾一段。不解全文。
func (s transcriptSource) Scan(ctx context.Context, f transcriptimport.Filter) ([]transcriptimport.Candidate, error) {
	root := sessionsRoot()
	if root == "" {
		return nil, nil
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []transcriptimport.Candidate
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			// 单个目录读不动不致命:跳过它,其余照出。
			continue
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			rel := filepath.Join(d.Name(), file.Name())
			cand, ok := scanCandidate(filepath.Join(root, rel), rel)
			if !ok || !f.Matches(cand) {
				continue
			}
			out = append(out, cand)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndedAt.After(out[j].EndedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// Open 把定位符变成一份可回放的转录。先做一趟索引算出线性链与元信息;正文留到
// Turns 时逐行再解。
func (s transcriptSource) Open(_ context.Context, loc transcriptimport.Locator) (transcriptimport.Transcript, error) {
	path, err := resolveLocator(loc)
	if err != nil {
		return nil, err
	}
	idx, err := indexTranscript(path)
	if err != nil {
		return nil, err
	}
	return &diskTranscript{path: path, index: idx}, nil
}

// resolveLocator 把定位符还原成 sessions 根内的绝对路径。根内解析与路径逃逸防护
// 是三个读取器共同的安全边界,判据在 transcriptimport;这里只补上本包的上下文。
func resolveLocator(loc transcriptimport.Locator) (string, error) {
	abs, err := transcriptimport.ResolveLocator(sessionsRoot(), loc)
	if err != nil {
		return "", fmt.Errorf("agentruntime/runtimes/piagent: %w", err)
	}
	return abs, nil
}

// scanCandidate 读文件头若干行 + 文件尾一段,拼出一行候选。ok=false 表示这个文件
// 里没有可用的会话记录(空文件 / 全是坏行 / 没有 session 头)。
func scanCandidate(path, rel string) (transcriptimport.Candidate, bool) {
	f, err := os.Open(path) // #nosec G304 -- path 由 sessionsRoot + 目录项拼出
	if err != nil {
		return transcriptimport.Candidate{}, false
	}
	defer func() { _ = f.Close() }()

	cand := transcriptimport.Candidate{
		Backend: agent_backend_entity.TypePiAgent,
		Locator: transcriptimport.Locator(filepath.ToSlash(rel)),
	}
	sc := transcriptimport.NewRecordScanner(f)
	for i := 0; i < transcriptimport.ScanHeadLines && sc.Scan(); i++ {
		var rec diskRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.isSessionHeader() {
			if cand.ProviderSessionID == "" {
				cand.ProviderSessionID = rec.ID
			}
			if cand.Cwd == "" {
				cand.Cwd = rec.Cwd
			}
			if cand.StartedAt.IsZero() {
				cand.StartedAt = rec.time()
			}
		}
		if cand.Title == "" && rec.isUserPrompt() {
			cand.Title = transcriptimport.FirstLine(rec.contentString())
		}
		if cand.Title != "" && cand.Cwd != "" && !cand.StartedAt.IsZero() {
			break
		}
	}
	// 没有 session 头(认不出这是一个 pi 会话文件)就不出这一行候选,不猜。
	if cand.StartedAt.IsZero() || cand.ProviderSessionID == "" {
		return transcriptimport.Candidate{}, false
	}
	cand.EndedAt = transcriptimport.LastRecordTime(f, cand.StartedAt, recordTime)
	return cand, true
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

// diskTranscript 是一份已索引的转录。索引里只有骨架(id / 时间 / 计数),正文每次
// Turns 时重新逐行读。
type diskTranscript struct {
	path  string
	index *transcriptIndex
}

func (t *diskTranscript) Meta() transcriptimport.Meta { return t.index.meta }

func (t *diskTranscript) Close() error { return nil }

func (t *diskTranscript) Turns(ctx context.Context, yield func(transcriptimport.Turn) error) error {
	f, err := os.Open(t.path) // #nosec G304 -- path 由 resolveLocator 校验过在 sessions 根内
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	r := &turnReader{
		TurnAccumulator: transcriptimport.NewTurnAccumulator(yield),
		fallback:        t.index.meta.Model,
		trackers:        make(map[string]*subagentTracker),
	}
	sc := transcriptimport.NewRecordScanner(f)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := sc.Bytes()
		var rec diskRecord
		if json.Unmarshal(line, &rec) != nil {
			continue // 坏行只丢那一行,已在 Open 时计入缺口
		}
		if rec.ID == "" {
			continue
		}
		if _, ok := t.index.chain[rec.ID]; !ok {
			continue // 不在链上:被抛弃的分支(实测未见,但防御性地照样挡住)
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

// turnReader 沿链把记录切成轮次。轮号/挂起/收尾这组与后端无关的状态由内嵌的
// TurnAccumulator 持有,本包留下的是 pi 方言:什么算一轮的起点、子代理 tracker。
type turnReader struct {
	transcriptimport.TurnAccumulator

	fallback string
	trackers map[string]*subagentTracker

	lastAssistID string
}

func (r *turnReader) consume(rec diskRecord) error {
	if rec.isUserPrompt() {
		if err := r.Flush(); err != nil {
			return err
		}
		r.Begin(transcriptimport.Turn{
			UserText:   rec.contentString(),
			StartedAt:  rec.time(),
			EndedAt:    rec.time(),
			ForkAnchor: r.lastAssistID,
			Model:      r.fallback,
		})
	}
	if rec.isAssistant() {
		r.lastAssistID = rec.ID
		if cur := r.Cur(); cur != nil && rec.Model != "" {
			cur.Model = rec.Model
		}
	}
	r.Touch(rec.time())
	for _, ev := range diskEventsFor(rec) {
		r.dispatch(ev)
	}
	return nil
}

// dispatch 把一条合成的 pkgpi.Event 走一遍与线上路径同一份分流:子代理相关的
// 先经 handleSubagentToolEvent(既有 tracker),其余落回 translate()。
//
// 收件方直接是 r.emitOne:回放是单线程的边推边落,子代理一次派遣能推出几十条事件
// (每条内部消息一到两条),中间夹一个有限容量的通道等于给自己设一个"推满就死锁"
// 的上限 —— 而那条死锁连 ctx 取消都救不回来(阻塞的是通道发送,不是 select)。
func (r *turnReader) dispatch(ev pkgpi.Event) {
	if handleSubagentToolEvent(ev, r.emitOne, r.trackers) {
		return
	}
	translated, usage, stopErr := translate(ev)
	r.Emit(translated...)
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

// emitOne 是给既有 tracker 用的单条投递口 —— handleSubagentToolEvent 收的是
// func(agentruntime.Event),而攒轮器的投递口是变参。
func (r *turnReader) emitOne(ev agentruntime.Event) { r.Emit(ev) }

// diskEventsFor 把一条磁盘记录翻成合成的 pkgpi.Event 序列。认不出的记录类型
// (model_change / thinking_level_change / custom_message / session 头)一律
// 跳过,不产出事件 —— 兼容性要求它们被静默忽略而不是报错。
func diskEventsFor(rec diskRecord) []pkgpi.Event {
	switch {
	case rec.isAssistant():
		return assistantEvents(rec)
	case rec.isToolResult():
		return []pkgpi.Event{{
			Kind: pkgpi.EventPostToolUse,
			Tool: pkgpi.ToolEvent{
				ID:      rec.ToolCallID,
				Name:    rec.ToolName,
				Content: rec.contentString(),
				IsError: rec.IsError,
				Details: rec.Details,
			},
		}}
	case rec.isCompaction():
		return []pkgpi.Event{{Kind: pkgpi.EventCompactBoundary}}
	default:
		return nil
	}
}

// assistantEvents 把一条 assistant 记录的内容块按序翻成事件:明文思维/正文/工具
// 调用先后进,该条记录的用量最后进 —— 与线上一条 assistant 消息的收尾顺序一致。
func assistantEvents(rec diskRecord) []pkgpi.Event {
	blocks, _ := rec.contentBlocks()
	events := make([]pkgpi.Event, 0, len(blocks)+1)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				events = append(events, pkgpi.Event{Kind: pkgpi.EventTextDelta, Text: b.Text})
			}
		case "thinking":
			// pi 的思维在磁盘上是明文;空的那些是真的空(缺口已在索引时计入),
			// 不是解析失败,这里原样交给 translate() —— 它本就只在非空时出事件。
			events = append(events, pkgpi.Event{Kind: pkgpi.EventThinkingDelta, Text: b.Thinking})
		case "toolCall":
			events = append(events, pkgpi.Event{
				Kind: pkgpi.EventPreToolUse,
				Tool: pkgpi.ToolEvent{ID: b.ID, Name: b.Name, Input: b.Arguments},
			})
		}
	}
	if rec.Usage != nil {
		events = append(events, pkgpi.Event{Kind: pkgpi.EventUsage, Usage: rec.Usage.toProviderUsage()})
	}
	return events
}

// diskUsage 是磁盘上 usage 对象的形状:{input, output, cacheRead, cacheWrite,
// reasoning, totalTokens, cost}。cost 不进 provider.Usage,不解。
type diskUsage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheWrite  int `json:"cacheWrite"`
	Reasoning   int `json:"reasoning"`
	TotalTokens int `json:"totalTokens"`
}

func (u *diskUsage) toProviderUsage() provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return provider.Usage{
		PromptTokens:        u.Input,
		CompletionTokens:    u.Output,
		ReasoningTokens:     u.Reasoning,
		CachedTokens:        u.CacheRead,
		CacheCreationTokens: u.CacheWrite,
		TotalTokens:         u.TotalTokens,
	}
}

// diskContentBlock 是 assistant content 数组里一个块的形状,与 pkg/piagent 的
// RPC contentBlock 几乎逐字段吻合(那是 decision 10 敢直接写这份磁盘解析器的
// 依据),但两个包各自维护一份,不跨包共享未导出类型。
type diskContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// diskRecord 是 ~/.pi/agent/sessions/<slug>/<ts>_<uuid>.jsonl 一行记录的骨架。
// 只解回放调度需要的字段;message 记录按 role 分流(assistant / user /
// toolResult 是三种不同形状,共用同一个信封)。
type diskRecord struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`

	// session 头
	Cwd string `json:"cwd"`

	// message 信封
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model"`
	Usage   *diskUsage      `json:"usage"`

	// message(role=toolResult)
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"isError"`
	Details    json.RawMessage `json:"details"`
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

func (r diskRecord) isSessionHeader() bool { return r.Type == "session" }
func (r diskRecord) isCompaction() bool    { return r.Type == "compaction" }
func (r diskRecord) isUserPrompt() bool    { return r.Type == "message" && r.Role == "user" }
func (r diskRecord) isAssistant() bool     { return r.Type == "message" && r.Role == "assistant" }
func (r diskRecord) isToolResult() bool    { return r.Type == "message" && r.Role == "toolResult" }

// contentString 取 user / toolResult 消息的正文 —— 这两种角色的 content 在磁盘上
// 就是一个纯字符串,不是 content block 数组。
func (r diskRecord) contentString() string {
	if len(r.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(r.Content, &s) != nil {
		return ""
	}
	return s
}

// contentBlocks 取 assistant 消息的 content block 数组。
func (r diskRecord) contentBlocks() ([]diskContentBlock, bool) {
	if len(r.Content) == 0 {
		return nil, false
	}
	var items []diskContentBlock
	if json.Unmarshal(r.Content, &items) != nil {
		return nil, false
	}
	return items, true
}

// transcriptIndex 是索引一趟的产物:链上成员集合 + 元信息。
type transcriptIndex struct {
	chain map[string]struct{}
	meta  transcriptimport.Meta
}

// recordSkeleton 是索引期为每条记录留下的东西:只留骨架与计数,不留正文。
type recordSkeleton struct {
	parent        string
	isUserPrompt  bool
	isCompaction  bool
	model         string
	ts            time.Time
	toolCallIDs   []string
	toolResultIDs []string
	emptyThinking int
}

// indexTranscript 走一趟文件,建立 id → 骨架 的索引,再沿 parentId 从文件里最后
// 一条记录回溯出链,最后只按链上的记录算元信息与缺口。
//
// pi 实测 200 个文件 0 条分支,但记录本就带 id/parentId,按行序信它不如按链信它 ——
// 与 claude 读取器同一个原则,只是 pi 没有显式的叶子指针,退回"文件最后一条"当
// 起点(claude 在指针缺失时也是这么兜底的)。
func indexTranscript(path string) (*transcriptIndex, error) {
	f, err := os.Open(path) // #nosec G304 -- path 由 resolveLocator 校验过在 sessions 根内
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		skeletons = map[string]*recordSkeleton{}
		order     []string
		badLines  int
		meta      = transcriptimport.Meta{Backend: agent_backend_entity.TypePiAgent}
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
		if rec.ID == "" {
			continue
		}
		if rec.isSessionHeader() {
			if meta.ProviderSessionID == "" {
				meta.ProviderSessionID = rec.ID
			}
			if meta.Cwd == "" {
				meta.Cwd = rec.Cwd
			}
		}
		if meta.Title == "" && rec.isUserPrompt() {
			meta.Title = transcriptimport.FirstLine(rec.contentString())
		}
		skeletons[rec.ID] = skeletonOf(rec)
		order = append(order, rec.ID)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var leaf string
	if len(order) > 0 {
		leaf = order[len(order)-1]
	}
	chainIDs := transcriptimport.WalkChain(skeletons, leaf, func(sk *recordSkeleton) string { return sk.parent })
	idx := &transcriptIndex{chain: make(map[string]struct{}, len(chainIDs)), meta: meta}
	for _, id := range chainIDs {
		idx.chain[id] = struct{}{}
	}
	summarize(idx, skeletons, chainIDs, badLines)
	return idx, nil
}

func skeletonOf(rec diskRecord) *recordSkeleton {
	sk := &recordSkeleton{
		parent:       rec.ParentID,
		isUserPrompt: rec.isUserPrompt(),
		isCompaction: rec.isCompaction(),
		ts:           rec.time(),
	}
	if rec.isAssistant() {
		sk.model = rec.Model
		blocks, _ := rec.contentBlocks()
		for _, b := range blocks {
			switch b.Type {
			case "toolCall":
				sk.toolCallIDs = append(sk.toolCallIDs, b.ID)
			case "thinking":
				if b.Thinking == "" {
					sk.emptyThinking++
				}
			}
		}
	}
	if rec.isToolResult() && rec.ToolCallID != "" {
		sk.toolResultIDs = append(sk.toolResultIDs, rec.ToolCallID)
	}
	return sk
}

// summarize 只按链上的记录算元信息与缺口。
func summarize(idx *transcriptIndex, skeletons map[string]*recordSkeleton, chain []string, badLines int) {
	var (
		openTools     = map[string]struct{}{}
		emptyThinking int
		toolCalls     int
	)
	for _, id := range chain {
		sk := skeletons[id]
		if sk.isUserPrompt {
			idx.meta.Turns++
		}
		if sk.isCompaction {
			idx.meta.Compactions++
		}
		if sk.model != "" {
			idx.meta.Model = sk.model
		}
		if !sk.ts.IsZero() {
			if idx.meta.StartedAt.IsZero() {
				idx.meta.StartedAt = sk.ts
			}
			idx.meta.EndedAt = sk.ts
		}
		toolCalls += len(sk.toolCallIDs)
		for _, tid := range sk.toolCallIDs {
			openTools[tid] = struct{}{}
		}
		for _, tid := range sk.toolResultIDs {
			delete(openTools, tid)
		}
		emptyThinking += sk.emptyThinking
	}
	idx.meta.ToolCalls = toolCalls
	// 子代理内部过程内联在 details.messages 里,不存在"子文件缺失"这回事,
	// 所以子代理缺口恒为 0。
	idx.meta.Gaps = transcriptimport.BuildGaps(emptyThinking, badLines, len(openTools), 0)
}

// 编译期确认契约实现完整。
var (
	_ transcriptimport.Source     = transcriptSource{}
	_ transcriptimport.Transcript = (*diskTranscript)(nil)
)
