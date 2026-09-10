package claudecode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// 磁盘转录读取器住在本包,是为了复用 translate() —— 那是 claudecode.Event →
// agentruntime.Event 的唯一一份翻译。线上路径与导入路径共用它,转录里的工具卡、
// 子代理块、压缩边界块因此只有一套语义。
func init() { transcriptimport.Register(transcriptSource{}) }

// transcriptSource 是 claude 在这台机器上的磁盘读取器。无状态,根目录每次现取
// (测试经 AGENTRE_CLAUDE_PROJECTS_DIR 指向 fixture)。
type transcriptSource struct{}

func (transcriptSource) Backend() agent_backend_entity.BackendType {
	return agent_backend_entity.TypeClaudeCode
}

// Scan 只读元信息:目录项 + 文件头若干行 + 文件尾一段。不解全文 —— 本机 1 000 多个
// 会话文件,逐个解全文连对话框都打不开。
func (s transcriptSource) Scan(ctx context.Context, f transcriptimport.Filter) ([]transcriptimport.Candidate, error) {
	root := projectsRoot()
	if root == "" {
		return nil, nil
	}
	projects, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []transcriptimport.Candidate
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, p.Name()))
		if err != nil {
			// 单个项目目录读不动不致命:跳过它,其余照出。
			continue
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			rel := filepath.Join(p.Name(), file.Name())
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

// Open 把定位符变成一份可回放的转录。先做一趟索引(只留每条记录的骨架与计数,不留
// 正文),据此算出叶子链与元信息;正文留到 Turns 时逐行再解。
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

// resolveLocator 把定位符还原成 projects 根内的绝对路径。根内解析与路径逃逸防护
// 是三个读取器共同的安全边界,判据在 transcriptimport;这里只补上本包的上下文。
func resolveLocator(loc transcriptimport.Locator) (string, error) {
	abs, err := transcriptimport.ResolveLocator(projectsRoot(), loc)
	if err != nil {
		return "", fmt.Errorf("agentruntime/runtimes/claudecode: %w", err)
	}
	return abs, nil
}

// scanCandidate 读文件头若干行 + 文件尾一段,拼出一行候选。
// ok=false 表示这个文件里没有可用的会话记录(空文件 / 全是坏行)。
func scanCandidate(path, rel string) (transcriptimport.Candidate, bool) {
	f, err := os.Open(path) // #nosec G304 -- path 由 projectsRoot + 目录项拼出
	if err != nil {
		return transcriptimport.Candidate{}, false
	}
	defer func() { _ = f.Close() }()

	cand := transcriptimport.Candidate{
		Backend:           agent_backend_entity.TypeClaudeCode,
		ProviderSessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Locator:           transcriptimport.Locator(filepath.ToSlash(rel)),
	}
	sc := transcriptimport.NewRecordScanner(f)
	for i := 0; i < transcriptimport.ScanHeadLines && sc.Scan(); i++ {
		var rec diskRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if cand.Cwd == "" {
			cand.Cwd = rec.Cwd
		}
		if cand.Origin == transcriptimport.OriginUnknown {
			cand.Origin = originOf(rec.Entrypoint)
		}
		if cand.StartedAt.IsZero() {
			cand.StartedAt = rec.time()
		}
		if cand.Title == "" && rec.isUserPrompt() {
			text, _ := rec.userContent()
			cand.Title = transcriptimport.FirstLine(text)
		}
		if cand.Title != "" && cand.Cwd != "" && !cand.StartedAt.IsZero() {
			break
		}
	}
	if cand.StartedAt.IsZero() {
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

// originOf 把 claude 的 entrypoint 翻成来源标记。只用于展示 —— 判重一律以库里的
// provider_session_id 为准。
func originOf(entrypoint string) transcriptimport.Origin {
	switch {
	case entrypoint == "":
		return transcriptimport.OriginUnknown
	case strings.Contains(entrypoint, "sdk"):
		return transcriptimport.OriginAgentre
	default:
		return transcriptimport.OriginTerminal
	}
}

// diskTranscript 是一份已索引的转录。索引里只有骨架(uuid / 时间 / 计数),正文每次
// Turns 时重新逐行读 —— 一份 42 轮 / 402 次工具调用的会话不该整个躺在内存里。
type diskTranscript struct {
	path  string
	index *transcriptIndex
}

func (t *diskTranscript) Meta() transcriptimport.Meta { return t.index.meta }

func (t *diskTranscript) Close() error { return nil }

func (t *diskTranscript) Turns(ctx context.Context, yield func(transcriptimport.Turn) error) error {
	f, err := os.Open(t.path) // #nosec G304 -- path 由 resolveLocator 校验过在 projects 根内
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	r := &turnReader{
		TurnAccumulator: transcriptimport.NewTurnAccumulator(yield),
		dir:             filepath.Dir(t.path),
		sessionID:       strings.TrimSuffix(filepath.Base(t.path), ".jsonl"),
		fallback:        t.index.meta.Model,
		dec:             claudecode.NewRecordDecoder(),
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
		if _, ok := t.index.chain[rec.UUID]; !ok {
			continue // 不在叶子链上:被抛弃的分支,一个字都不放行
		}
		if err := r.consume(rec, line); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return r.Flush()
}

// turnReader 沿叶子链把记录切成轮次。轮号/挂起/收尾这组与后端无关的状态由内嵌的
// TurnAccumulator 持有,本包留下的是 claude 方言:什么算一轮的起点、子代理怎么展开。
type turnReader struct {
	transcriptimport.TurnAccumulator

	dir       string
	sessionID string
	fallback  string
	dec       *claudecode.RecordDecoder

	lastAssistID string
}

func (r *turnReader) consume(rec diskRecord, line []byte) error {
	if rec.isUserPrompt() {
		if err := r.Flush(); err != nil {
			return err
		}
		text, images := rec.userContent()
		r.Begin(transcriptimport.Turn{
			UserText:   text,
			UserImages: images,
			StartedAt:  rec.time(),
			EndedAt:    rec.time(),
			ForkAnchor: r.lastAssistID,
			Model:      r.fallback,
		})
	}
	if rec.Type == "assistant" {
		r.lastAssistID = rec.UUID
		if m := rec.model(); m != "" {
			if cur := r.Cur(); cur != nil {
				cur.Model = m
			}
		}
	}
	r.Touch(rec.time())

	events, ok := r.dec.Decode(line)
	if !ok {
		return nil
	}
	for _, ev := range events {
		r.appendEvents(rec, ev)
	}
	return nil
}

// appendEvents 把一条 claudecode.Event 落到当前轮:子代理的内部过程在它的
// tool_result 之前展开,随后照常走 translate。
func (r *turnReader) appendEvents(rec diskRecord, ev claudecode.Event) {
	if ev.Kind == claudecode.EventPostToolUse && ev.Tool != nil {
		if meta, ok := parseSubagentResult(rec.ToolUseResult); ok {
			r.Emit(subagentEvents(r.dir, r.sessionID, ev.Tool.ID, meta)...)
		}
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
	if ev.Kind == claudecode.EventUsage {
		u := ev.Usage
		cur.Usage = &u
	}
	if stopErr != nil {
		cur.ErrorText = stopErr.Error()
	}
}

// subagentResult 是父侧 tool_result 记录顶层 toolUseResult 里与子代理有关的那几项。
// 子代理的内部过程不在主文件里(主文件零条 isSidechain),靠 agentId 关联到
// <project>/<sid>/subagents/agent-<agentId>.jsonl。
type subagentResult struct {
	AgentID       string `json:"agentId"`
	AgentType     string `json:"agentType"`
	ResolvedModel string `json:"resolvedModel"`
	Status        string `json:"status"`
	Prompt        string `json:"prompt"`
	TotalTokens   int    `json:"totalTokens"`
	DurationMs    int    `json:"totalDurationMs"`
	ToolUseCount  int    `json:"totalToolUseCount"`
}

func parseSubagentResult(raw json.RawMessage) (subagentResult, bool) {
	if len(raw) == 0 {
		return subagentResult{}, false
	}
	var out subagentResult
	if err := json.Unmarshal(raw, &out); err != nil || out.AgentID == "" {
		return subagentResult{}, false
	}
	return out, true
}

func (m subagentResult) info() agentruntime.SubagentInfo {
	return agentruntime.SubagentInfo{
		TaskID:       m.AgentID,
		SubagentType: m.AgentType,
		Kind:         "local_agent",
		Prompt:       m.Prompt,
		ToolUses:     m.ToolUseCount,
		TotalTokens:  m.TotalTokens,
		DurationMs:   m.DurationMs,
		Status:       m.Status,
	}
}

func subagentPath(dir, sessionID, agentID string) string {
	return filepath.Join(dir, sessionID, "subagents", "agent-"+agentID+".jsonl")
}

// subagentEvents 展开一次子代理派遣:起止状态块 + 实际模型 + 子文件里的工具步骤。
// 子文件不在了就只出状态块 —— 缺口已在 Open 时声明,这里不再猜。
func subagentEvents(dir, sessionID, toolCallID string, meta subagentResult) []agentruntime.Event {
	out := []agentruntime.Event{agentruntime.SubagentStarted{ToolCallID: toolCallID, Info: meta.info()}}
	if meta.ResolvedModel != "" {
		out = append(out, agentruntime.SubagentModel{ToolCallID: toolCallID, Model: meta.ResolvedModel})
	}
	out = append(out, subagentInnerEvents(subagentPath(dir, sessionID, meta.AgentID), toolCallID)...)
	return append(out, agentruntime.SubagentDone{ToolCallID: toolCallID, Info: meta.info()})
}

// subagentInnerEvents 读一个子代理文件。子文件是"一次派遣"的量级(本机中位数百行),
// 逐个读、读完即弃,不会把整份转录攒进内存。
//
// 子文件的记录不带 parent_tool_use_id(它们靠文件名与顶层 agentId 归属),所以这里
// 补上外层 tool_use_id:translate 据此把工具事件挂进派遣卡,并丢掉子代理旁白 ——
// 与线上路径同一条规则。
func subagentInnerEvents(path, toolCallID string) []agentruntime.Event {
	f, err := os.Open(path) // #nosec G304 -- path 由主转录所在目录 + agentId 拼出
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	dec := claudecode.NewRecordDecoder()
	var out []agentruntime.Event
	sc := transcriptimport.NewRecordScanner(f)
	for sc.Scan() {
		events, ok := dec.Decode(sc.Bytes())
		if !ok {
			continue
		}
		for _, ev := range events {
			// 子代理内部的 usage 是另一个 Anthropic 会话的输入量,混进主轮会让
			// 「已用上下文」骤降到子代理的小上下文 —— 线上路径就是靠
			// parent_tool_use_id 把它挡在外面的,而子文件的记录本身不带那个字段。
			if ev.Kind == claudecode.EventUsage {
				continue
			}
			ev.ParentToolUseID = toolCallID
			translated, _, _ := translate(ev)
			out = append(out, translated...)
		}
	}
	return out
}

// diskRecord 是 ~/.claude/projects/<slug>/<sid>.jsonl 一行记录的骨架。
// 只解回放调度需要的字段(身份、时间、归属、用户正文);事件正文交给
// claudecode.RecordDecoder,不在这里重复一套。
type diskRecord struct {
	Type                      string          `json:"type"`
	Subtype                   string          `json:"subtype"`
	UUID                      string          `json:"uuid"`
	ParentUUID                string          `json:"parentUuid"`
	LeafUUID                  string          `json:"leafUuid"`
	Timestamp                 string          `json:"timestamp"`
	Cwd                       string          `json:"cwd"`
	SessionID                 string          `json:"sessionId"`
	Entrypoint                string          `json:"entrypoint"`
	IsMeta                    bool            `json:"isMeta"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	ToolUseResult             json.RawMessage `json:"toolUseResult"`
	Message                   json.RawMessage `json:"message"`
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

func (r diskRecord) model() string {
	msg, ok := r.decodeMessage()
	if !ok {
		return ""
	}
	return msg.Model
}

// isUserPrompt 判"这条是用户真的说了一句话",也就是一轮的起点。
//
// 排除三类合成 user 记录:工具结果(带 toolUseResult / content 是 tool_result 块)、
// CLI 自己注入的 meta 与压缩摘要、以及只在转录里可见的提示。
func (r diskRecord) isUserPrompt() bool {
	if r.Type != "user" || r.IsMeta || r.IsCompactSummary || r.IsVisibleInTranscriptOnly {
		return false
	}
	if len(r.ToolUseResult) > 0 {
		return false
	}
	text, images := r.userContent()
	return text != "" || len(images) > 0
}

// userContent 取用户这一轮说的话与贴的图。content 在磁盘上有两种形状:纯字符串,
// 或 Anthropic content block 数组。
func (r diskRecord) userContent() (string, []blocks.ImageBlock) {
	msg, ok := r.decodeMessage()
	if !ok {
		return "", nil
	}
	if len(msg.Content) == 0 {
		return "", nil
	}
	if msg.Content[0] == '"' {
		var s string
		if json.Unmarshal(msg.Content, &s) != nil {
			return "", nil
		}
		return s, nil
	}
	var items []diskContentBlock
	if json.Unmarshal(msg.Content, &items) != nil {
		return "", nil
	}
	var (
		text   strings.Builder
		images []blocks.ImageBlock
	)
	for _, it := range items {
		switch it.Type {
		case "text":
			if it.Text == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(it.Text)
		case "image":
			if img, ok := it.Source.image(); ok {
				images = append(images, img)
			}
		}
	}
	return text.String(), images
}

type diskMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

func (r diskRecord) decodeMessage() (diskMessage, bool) {
	if len(r.Message) == 0 {
		return diskMessage{}, false
	}
	var msg diskMessage
	if err := json.Unmarshal(r.Message, &msg); err != nil {
		return diskMessage{}, false
	}
	return msg, true
}

type diskContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	Thinking   string          `json:"thinking"`
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_use_id"`
	Source     diskImageSource `json:"source"`
}

type diskImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

func (s diskImageSource) image() (blocks.ImageBlock, bool) {
	switch {
	case s.URL != "":
		return blocks.ImageBlock{MediaType: s.MediaType, Source: blocks.BlobSource{URL: s.URL}}, true
	case s.Data != "":
		raw, err := base64.StdEncoding.DecodeString(s.Data)
		if err != nil {
			return blocks.ImageBlock{}, false
		}
		return blocks.ImageBlock{MediaType: s.MediaType, Source: blocks.BlobSource{Inline: raw}}, true
	default:
		return blocks.ImageBlock{}, false
	}
}

// transcriptIndex 是索引一趟的产物:叶子链成员集合 + 元信息。
// 每条记录只留骨架与计数,正文不留 —— 索引的内存占用与转录**条数**成正比,
// 与正文大小无关。
type transcriptIndex struct {
	chain map[string]struct{}
	meta  transcriptimport.Meta
}

// recordSkeleton 是索引期为每条记录留下的东西。
type recordSkeleton struct {
	parent        string
	typ           string
	isUserPrompt  bool
	isCompact     bool
	model         string
	ts            time.Time
	toolCallIDs   []string
	toolResultIDs []string
	emptyThinking int
	agentIDs      []string
}

// indexTranscript 走一趟文件,建立 uuid → 骨架 的索引,再沿 last-prompt.leafUuid
// 的 parentUuid 回溯出叶子链,最后只按链上的记录算元信息与缺口。
//
// 为什么不按文件行序:实测 60 个文件里 13 个有分支(fork / 回退 / 编辑重发),
// 按行序会把已经被抛弃的分支一起塞进转录;而叶子指针 60/60 都在。
func indexTranscript(path string) (*transcriptIndex, error) {
	f, err := os.Open(path) // #nosec G304 -- path 由 resolveLocator 校验过在 projects 根内
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		skeletons = map[string]*recordSkeleton{}
		order     []string
		leaf      string
		badLines  int
		meta      = transcriptimport.Meta{
			Backend:           agent_backend_entity.TypeClaudeCode,
			ProviderSessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		}
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
		if rec.Type == "last-prompt" && rec.LeafUUID != "" {
			leaf = rec.LeafUUID // 取最后一条:它指向当下这条分支的叶子
			continue
		}
		if rec.UUID == "" {
			continue
		}
		if meta.Cwd == "" {
			meta.Cwd = rec.Cwd
		}
		if meta.Origin == transcriptimport.OriginUnknown {
			meta.Origin = originOf(rec.Entrypoint)
		}
		if meta.Title == "" && rec.isUserPrompt() {
			text, _ := rec.userContent()
			meta.Title = transcriptimport.FirstLine(text)
		}
		sk := skeletonOf(rec)
		skeletons[rec.UUID] = sk
		order = append(order, rec.UUID)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if leaf == "" && len(order) > 0 {
		// 没有叶子指针(老 CLI / 被截断)时退回文件里最后一条记录,链仍然是一条真链。
		leaf = order[len(order)-1]
	}

	chainIDs := transcriptimport.WalkChain(skeletons, leaf, func(sk *recordSkeleton) string { return sk.parent })
	idx := &transcriptIndex{chain: make(map[string]struct{}, len(chainIDs)), meta: meta}
	for _, id := range chainIDs {
		idx.chain[id] = struct{}{}
	}
	summarize(idx, skeletons, chainIDs, badLines, filepath.Dir(path))
	return idx, nil
}

func skeletonOf(rec diskRecord) *recordSkeleton {
	sk := &recordSkeleton{
		parent:       rec.ParentUUID,
		typ:          rec.Type,
		isUserPrompt: rec.isUserPrompt(),
		isCompact:    rec.Type == "system" && rec.Subtype == "compact_boundary",
		ts:           rec.time(),
	}
	if meta, ok := parseSubagentResult(rec.ToolUseResult); ok {
		sk.agentIDs = append(sk.agentIDs, meta.AgentID)
	}
	msg, ok := rec.decodeMessage()
	if !ok || len(msg.Content) == 0 || msg.Content[0] != '[' {
		return sk
	}
	sk.model = msg.Model
	var items []diskContentBlock
	if json.Unmarshal(msg.Content, &items) != nil {
		return sk
	}
	for _, it := range items {
		switch it.Type {
		case "tool_use":
			sk.toolCallIDs = append(sk.toolCallIDs, it.ID)
		case "tool_result":
			sk.toolResultIDs = append(sk.toolResultIDs, it.ToolCallID)
		case "thinking":
			// 走 Anthropic 模型时磁盘上只剩签名,思维正文是真的不在了 ——
			// 这是缺口,不是解析失败。
			if it.Thinking == "" {
				sk.emptyThinking++
			}
		}
	}
	return sk
}

// summarize 只按叶子链上的记录算元信息与缺口 —— 被抛弃的分支既不进转录,也不该
// 进计数。
func summarize(idx *transcriptIndex, skeletons map[string]*recordSkeleton, chain []string, badLines int, dir string) {
	var (
		openTools     = map[string]struct{}{}
		emptyThinking int
		missingSubs   int
		toolCalls     int
	)
	for _, id := range chain {
		sk := skeletons[id]
		if sk.isUserPrompt {
			idx.meta.Turns++
		}
		if sk.isCompact {
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
		for _, agentID := range sk.agentIDs {
			if _, err := os.Stat(subagentPath(dir, idx.meta.ProviderSessionID, agentID)); err != nil {
				missingSubs++
			}
		}
	}
	idx.meta.ToolCalls = toolCalls
	idx.meta.Gaps = transcriptimport.BuildGaps(emptyThinking, badLines, len(openTools), missingSubs)
}

// 编译期确认契约实现完整。
var (
	_ transcriptimport.Source     = transcriptSource{}
	_ transcriptimport.Transcript = (*diskTranscript)(nil)
)
