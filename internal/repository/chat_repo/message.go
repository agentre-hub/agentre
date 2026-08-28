package chat_repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// sessionMutexes 是 per-session 的 read-modify-write 锁。FlipSubagentStatus 与
// AppendSubagentChildren 在同一会话里并发对同一条 launch 消息行做「Find → 改写 →
// Update」时,按会话粒度串行化,避免互相覆盖对方的写入。
// key: sessionID(int64),value: *sync.Mutex。
var sessionMutexes sync.Map

// lockForSession 返回会话 ID 对应的 *sync.Mutex。锁在会话存续期间常驻(不被 GC);
// 会话数实践上有限,不构成问题。
func lockForSession(sessionID int64) *sync.Mutex {
	v, _ := sessionMutexes.LoadOrStore(sessionID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

//go:generate mockgen -source message.go -destination mock_chat_repo/mock_message.go

type MessageRepo interface {
	// List 取回整条会话的消息**连同全部正文**。它是 ListMeta + FillBlocks 的合体,
	// 留给「本来就要读整条转录」的调用方(如导出、written paths);读路径(LoadSession)
	// 不再走它 —— 见 ListMeta 的说明。
	List(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error)
	// ListMeta 只取消息元数据,不碰块表。读路径是「元数据全量 + 块按需取」(决策 6):
	// 元数据全量保证前端能看到的信息集合不缩水(轮次、token 计数、错误、时间),
	// 正文则由 FillBlocks 按窗口补,一条 8k 块的会话不再整条搬进内存。
	// 返回的实体 BlocksJSON 是空串,表示「这条消息的正文没补过」。
	ListMeta(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error)
	// FillBlocks 给点名的这几条消息补上全部正文(一次 IN 查询)。就地改写传入的实体。
	FillBlocks(ctx context.Context, msgs []*chat_entity.Message) error
	// FillBlocksByType 只补指定类型的块。派生视图(后台任务面板 / 大纲 / 变更)按类型
	// 点查它需要的那一类块,而不是把整条转录搬到前端自己遍历;块表的 type 列让这件事
	// 成为索引点查。types 为空时不发查询,所有消息拿到空正文。
	FillBlocksByType(ctx context.Context, msgs []*chat_entity.Message, types []string) error
	Find(ctx context.Context, id int64) (*chat_entity.Message, error)
	NextSeq(ctx context.Context, sessionID int64) (int, error)
	Create(ctx context.Context, m *chat_entity.Message) error
	Update(ctx context.Context, m *chat_entity.Message) error
	// UpdateUsage 只写 token 计数那几列。
	//
	// 不要退回 Update:整行 Save 会把调用方手上那份实体的正文一起重写(整批块行删了重插),
	// 而 usage 帧是**每个 API call 一帧**、一条长轮次消息的正文是 MB 级的(实测单条最大
	// 12.9 MB),于是「存 6 个整数」被放大成「重写整条正文」,一个 30 次工具调用的轮次要
	// 重写几十遍。理由与 Session().UpdateContextWindow 同源,只是这里的载荷大三个量级。
	UpdateUsage(ctx context.Context, id int64, u MessageUsage) error
	// UpdateErrorText 只写 error_text 一列,理由同 UpdateUsage。
	UpdateErrorText(ctx context.Context, id int64, text string) error
	// DeleteFromSeq 删除指定 session 下 seq >= fromSeq 的所有消息，返回被删除的行数。
	// 用于「从第 N 条消息开始重新生成」时一次性截断后续记录。
	DeleteFromSeq(ctx context.Context, sessionID int64, fromSeq int) (int64, error)
	// FlipSubagentStatus 定向把本会话里 tool_call_id==toolCallID 的 subagent_state 块状态改成
	// status(后台 bash 在之后的自主轮才完成,无法走 per-turn accumulator)。summary 非空时
	// 同时写入块的 summary 字段。找不到则静默返回 nil(任务可能已 evict / 非本会话)。
	FlipSubagentStatus(ctx context.Context, sessionID int64, toolCallID, status, summary string) error
	// PatchSubagentProgress 定向把 parent_tool_call_id==toolCallID 的 subagent_state 块的
	// 运行时进度字段更新为最新快照。后台 subagent 在会话**空闲态**跑,发起它的那一轮
	// accumulator 早已收尾,进度只能这样落回发起消息(否则重开会话看到的永远是派遣那一刻
	// 的旧数字)。p 的零值字段跳过;全零 / 找不到命中块则静默返回 nil。
	PatchSubagentProgress(ctx context.Context, sessionID int64, toolCallID string, p SubagentProgress) error
	// AppendSubagentChildren 把后台 subagent 内部产生的子块追加进发起消息里对应
	// subagent_state 块的 nested_tool_call_ids,同时把 childBlocksJSON 里的 StoredBlock
	// 作为新块行追加到该消息正文末尾。childIDs 自动去重(跳过已在数组中的 id)。
	// 找不到命中块则静默返回 nil。
	AppendSubagentChildren(ctx context.Context, sessionID int64, parentToolCallID, childBlocksJSON string, childIDs []string) error
	// FindAssistantBySubagentToolCallID 按定位键点查本会话里 type=="subagent_state" 且
	// tool_call_id==toolCallID 的块,返回它的宿主消息(后台 subagent 的发起卡所在消息)。
	// toolCallID 空 / 无命中 / 该会话没有这类块时返回 (nil, nil)。仅读取定位,不改写。
	FindAssistantBySubagentToolCallID(ctx context.Context, sessionID int64, toolCallID string) (*chat_entity.Message, error)
	// LatestAssistant 取某会话 seq 最大的一条 assistant 消息(无 → nil,nil)。
	// 用于 peek 运行中子任务的当前输出（read 工具的 running 分支）。
	LatestAssistant(ctx context.Context, sessionID int64) (*chat_entity.Message, error)
	// FindSubagentState 按 tool_call_id==toolCallID 定位本会话里的 subagent_state 块,返回它
	// 记录的 CLI task_id + 当前 status(供 StopBackgroundTask 下发 stop_task)。定位方式同
	// FlipSubagentStatus(块表索引点查),只读不改。toolCallID 空 / 无命中时返回 (found=false)。
	FindSubagentState(ctx context.Context, sessionID int64, toolCallID string) (taskID, status string, found bool, err error)
}

var defaultMessage MessageRepo

func Message() MessageRepo             { return defaultMessage }
func RegisterMessage(impl MessageRepo) { defaultMessage = impl }
func NewMessage() MessageRepo          { return &messageRepo{} }

type messageRepo struct{}

func (r *messageRepo) List(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error) {
	rows, err := r.ListMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := r.FillBlocks(ctx, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *messageRepo) ListMeta(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error) {
	var rows []*chat_entity.Message
	if err := db.Ctx(ctx).
		Where("session_id = ?", sessionID).
		Order("seq ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *messageRepo) FillBlocks(ctx context.Context, msgs []*chat_entity.Message) error {
	return fillBlocks(ctx, msgs, nil)
}

func (r *messageRepo) FillBlocksByType(ctx context.Context, msgs []*chat_entity.Message, types []string) error {
	if len(types) == 0 {
		// 一个类型都没点名 = 什么都不该取。这里若退化成 fillBlocks(nil) 就是整条转录
		// 全量读回,正是本轮要消灭的形态;空正文交出去,调用方拿到的是「没有这类块」。
		for _, m := range msgs {
			if m != nil {
				m.BlocksJSON = "[]"
			}
		}
		return nil
	}
	return fillBlocks(ctx, msgs, types)
}

func (r *messageRepo) NextSeq(ctx context.Context, sessionID int64) (int, error) {
	var next int
	err := db.Ctx(ctx).
		Table("chat_messages").
		Select("COALESCE(MAX(seq), 0) + 1").
		Where("session_id = ?", sessionID).
		Row().Scan(&next)
	if err != nil {
		return 0, err
	}
	return next, nil
}

func (r *messageRepo) Create(ctx context.Context, m *chat_entity.Message) error {
	now := time.Now().UnixMilli()
	if m.Createtime == 0 {
		m.Createtime = now
	}
	m.Updatetime = now
	// 元数据行与它的块行必须一起成立:半条消息(有元数据、没正文)对上层是不可修复的。
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		return insertBlocks(txCtx, m.ID, m.BlocksJSON)
	})
}

func (r *messageRepo) Update(ctx context.Context, m *chat_entity.Message) error {
	m.Updatetime = time.Now().UnixMilli()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		if err := tx.Save(m).Error; err != nil {
			return err
		}
		return replaceBlocks(txCtx, m.ID, m.BlocksJSON)
	})
}

// MessageUsage 是一帧 usage 要落库的那几列的取值(累加语义由调用方在内存实体上完成,
// 这里只负责把最终值写进对应列)。
type MessageUsage struct {
	PromptTokens        int
	CompletionTokens    int
	CachedTokens        int
	CacheCreationTokens int
	ReasoningTokens     int
	TotalInputTokens    int
}

func (r *messageRepo) UpdateUsage(ctx context.Context, id int64, u MessageUsage) error {
	return db.Ctx(ctx).Model(&chat_entity.Message{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"prompt_tokens":         u.PromptTokens,
			"completion_tokens":     u.CompletionTokens,
			"cached_tokens":         u.CachedTokens,
			"cache_creation_tokens": u.CacheCreationTokens,
			"reasoning_tokens":      u.ReasoningTokens,
			"total_input_tokens":    u.TotalInputTokens,
			"updatetime":            time.Now().UnixMilli(),
		}).Error
}

func (r *messageRepo) UpdateErrorText(ctx context.Context, id int64, text string) error {
	return db.Ctx(ctx).Model(&chat_entity.Message{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"error_text": text,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func (r *messageRepo) Find(ctx context.Context, id int64) (*chat_entity.Message, error) {
	var m chat_entity.Message
	if err := db.Ctx(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := fillBlocks(ctx, []*chat_entity.Message{&m}, nil); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *messageRepo) LatestAssistant(ctx context.Context, sessionID int64) (*chat_entity.Message, error) {
	var m chat_entity.Message
	err := db.Ctx(ctx).
		Where("session_id = ? AND role = ?", sessionID, "assistant").
		Order("seq DESC").
		Limit(1).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := fillBlocks(ctx, []*chat_entity.Message{&m}, nil); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *messageRepo) FindSubagentState(ctx context.Context, sessionID int64, toolCallID string) (string, string, bool, error) {
	row, err := findSubagentStateBlock(ctx, sessionID, toolCallID)
	if err != nil || row == nil {
		return "", "", false, err
	}
	data, err := chat_entity.DecodeBlockData(row.Codec, row.Data)
	if err != nil {
		return "", "", false, err
	}
	taskID, status, err := SubagentStateFromBlockData(data)
	if err != nil {
		return "", "", false, err
	}
	return taskID, status, true, nil
}

// SubagentStateFromBlockData 从一个 subagent_state 块的正文里读出 task_id + status。
// 遵循与 patchSubagentStateData 相同的 UseNumber 解码纪律。导出以便直接单测。
func SubagentStateFromBlockData(data []byte) (string, string, error) {
	decoded, err := decodeBlockObject(data)
	if err != nil {
		return "", "", err
	}
	taskID, _ := decoded["task_id"].(string)
	status, _ := decoded["status"].(string)
	return taskID, status, nil
}

// rewriteSubagentBlock 按定位键点查后台任务发起卡所在的 subagent_state 块,把 rewrite
// 改写后的正文写回**那一个块行**,不再读出并重写宿主消息的全部块。rewrite 返回
// (重写后的正文, 是否改写过, error)。命中不到目标块时静默返回 nil:任务可能已
// evict / 非本会话。
func (r *messageRepo) rewriteSubagentBlock(
	ctx context.Context, sessionID int64, toolCallID string,
	rewrite func(data []byte) ([]byte, bool, error),
) error {
	// serialize read-modify-write per session to avoid lost-update races between
	// FlipSubagentStatus / PatchSubagentProgress / AppendSubagentChildren.
	mu := lockForSession(sessionID)
	mu.Lock()
	defer mu.Unlock()

	row, err := findSubagentStateBlock(ctx, sessionID, toolCallID)
	if err != nil || row == nil {
		return err
	}
	data, err := chat_entity.DecodeBlockData(row.Codec, row.Data)
	if err != nil {
		return err
	}
	rewritten, changed, err := rewrite(data)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return updateBlockData(ctx, row, rewritten)
}

func (r *messageRepo) FlipSubagentStatus(ctx context.Context, sessionID int64, toolCallID, status, summary string) error {
	if toolCallID == "" || status == "" {
		return nil
	}
	logger.Ctx(ctx).Info("chat_repo.FlipSubagentStatus: flipping subagent_state status",
		zap.Int64("sessionId", sessionID), zap.String("toolUseId", toolCallID), zap.String("status", status))

	return r.rewriteSubagentBlock(ctx, sessionID, toolCallID, func(data []byte) ([]byte, bool, error) {
		return FlipSubagentInBlockData(data, status, summary)
	})
}

func (r *messageRepo) PatchSubagentProgress(ctx context.Context, sessionID int64, toolCallID string, p SubagentProgress) error {
	if toolCallID == "" || p.IsZero() {
		return nil
	}
	return r.rewriteSubagentBlock(ctx, sessionID, toolCallID, func(data []byte) ([]byte, bool, error) {
		return PatchSubagentProgressInBlockData(data, p)
	})
}

// decodeBlockObject 把一个块的正文解成 map。数字字段(total_tokens / duration_ms /
// tool_uses)用 json.Decoder + UseNumber() 保持 json.Number,避免经 map[string]any 的
// float64 强转把整数重写成科学计数(如 1e+04)。
func decodeBlockObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var decoded map[string]any
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// patchSubagentStateData 就地改写一个 subagent_state 块的正文(apply 返回 false 表示
// 这块没动),返回重写后的正文 + 是否改写过。仓储层不依赖 service 的 block 类型,
// 只按该块的少数已知字段操作;未被 apply 触碰的字段原样保留。
func patchSubagentStateData(data []byte, apply func(data map[string]any) bool) ([]byte, bool, error) {
	decoded, err := decodeBlockObject(data)
	if err != nil {
		return data, false, err
	}
	if !apply(decoded) {
		return data, false, nil
	}
	buf, err := json.Marshal(decoded)
	if err != nil {
		return data, false, err
	}
	return buf, true, nil
}

// FlipSubagentInBlockData 就地翻转一个 subagent_state 块的 status,返回重写后的正文 +
// 是否改写过。summary 非空时同时写入 summary 字段。只触碰 status / summary 两个字段。
// 导出以便直接单测改写逻辑。
func FlipSubagentInBlockData(data []byte, status, summary string) ([]byte, bool, error) {
	return patchSubagentStateData(data, func(decoded map[string]any) bool {
		decoded["status"] = status
		if summary != "" {
			decoded["summary"] = summary
		}
		return true
	})
}

// SubagentProgress 是 subagent_state 块的运行时进度快照。零值字段代表「这一帧没带这项」,
// patch 时跳过 —— CLI 的 task_progress 偶尔缺 usage,不该把已经攒起来的工具数 / token
// 抹回 0。
type SubagentProgress struct {
	TotalTokens  int
	ToolUses     int
	DurationMs   int
	LastToolName string
	// Model 是子代理内部帧解析出的实际模型(R2)。first-wins 在上游 SubagentModelHandler
	// 已经保证,这里只是把它跟其它进度字段一起搬进跨轮补丁;空值代表"这一轮没有新模型
	// 可写",patch 时跳过,不抹掉已记录的值。
	Model string
}

// IsZero 表示这份快照没带任何进度信息,调用方可据此跳过一次读-改-写。
func (p SubagentProgress) IsZero() bool {
	return p.TotalTokens == 0 && p.ToolUses == 0 && p.DurationMs == 0 && p.LastToolName == "" && p.Model == ""
}

// PatchSubagentProgressInBlockData 就地更新一个 subagent_state 块的进度字段,返回重写后的
// 正文 + 是否改写过。零值字段跳过(见 SubagentProgress)。导出以便直接单测改写逻辑。
func PatchSubagentProgressInBlockData(data []byte, p SubagentProgress) ([]byte, bool, error) {
	if p.IsZero() {
		return data, false, nil
	}
	return patchSubagentStateData(data, func(decoded map[string]any) bool {
		if p.TotalTokens > 0 {
			decoded["total_tokens"] = json.Number(strconv.Itoa(p.TotalTokens))
		}
		if p.ToolUses > 0 {
			decoded["tool_uses"] = json.Number(strconv.Itoa(p.ToolUses))
		}
		if p.DurationMs > 0 {
			decoded["duration_ms"] = json.Number(strconv.Itoa(p.DurationMs))
		}
		if p.LastToolName != "" {
			decoded["last_tool_name"] = p.LastToolName
		}
		if p.Model != "" {
			decoded["model"] = p.Model
		}
		return true
	})
}

// AppendNestedToolCallIDsInBlockData 把 childIDs 去重追加进一个 subagent_state 块的
// nested_tool_call_ids,返回重写后的正文 + 是否改写过。导出以便直接单测改写逻辑。
func AppendNestedToolCallIDsInBlockData(data []byte, childIDs []string) ([]byte, bool, error) {
	return patchSubagentStateData(data, func(decoded map[string]any) bool {
		existing := map[string]bool{}
		ids, _ := decoded["nested_tool_call_ids"].([]any)
		for _, v := range ids {
			if s, ok := v.(string); ok {
				existing[s] = true
			}
		}
		for _, id := range childIDs {
			if !existing[id] {
				ids = append(ids, id)
				existing[id] = true
			}
		}
		decoded["nested_tool_call_ids"] = ids
		return true
	})
}

func (r *messageRepo) AppendSubagentChildren(ctx context.Context, sessionID int64, parentToolCallID, childBlocksJSON string, childIDs []string) error {
	if parentToolCallID == "" || childBlocksJSON == "" {
		return nil
	}
	// serialize read-modify-write per session to avoid lost-update races with FlipSubagentStatus.
	mu := lockForSession(sessionID)
	mu.Lock()
	defer mu.Unlock()

	row, err := findSubagentStateBlock(ctx, sessionID, parentToolCallID)
	if err != nil || row == nil {
		return err
	}
	data, err := chat_entity.DecodeBlockData(row.Codec, row.Data)
	if err != nil {
		return err
	}
	rewritten, changed, err := AppendNestedToolCallIDsInBlockData(data, childIDs)
	if err != nil {
		return err
	}
	if changed {
		if err := updateBlockData(ctx, row, rewritten); err != nil {
			return err
		}
	}
	return appendBlocks(ctx, row.MessageID, childBlocksJSON)
}

func (r *messageRepo) FindAssistantBySubagentToolCallID(ctx context.Context, sessionID int64, toolCallID string) (*chat_entity.Message, error) {
	row, err := findSubagentStateBlock(ctx, sessionID, toolCallID)
	if err != nil || row == nil {
		return nil, err
	}
	return r.Find(ctx, row.MessageID)
}

func (r *messageRepo) DeleteFromSeq(ctx context.Context, sessionID int64, fromSeq int) (int64, error) {
	var deleted int64
	err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		// 先删块行,再删宿主消息 —— 反过来会让块行在两条语句之间成为孤儿。
		if err := deleteBlocksOfMessages(txCtx, "session_id = ? AND seq >= ?", sessionID, fromSeq); err != nil {
			return err
		}
		res := tx.Where("session_id = ? AND seq >= ?", sessionID, fromSeq).Delete(&chat_entity.Message{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
