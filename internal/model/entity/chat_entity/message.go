package chat_entity

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// MessageTextMaxBytes — defensive ceiling on a single user message body.
// 字节而非 rune 是兜底，token 上限由 provider 自己再算；这里只防异常的
// 超大 blob（整个多 MB 文件误粘）撑爆 DB / UI。256 KiB ≈ 8.7 万汉字，
// 足够粘贴大段日志 / 文件。
const MessageTextMaxBytes = 256 * 1024

var allowedMessageRoles = map[string]struct{}{
	"user":      {},
	"assistant": {},
}

// Message is a single chat message (user or assistant).
type Message struct {
	ID        int64 `gorm:"column:id;primaryKey;autoIncrement"`
	SessionID int64 `gorm:"column:session_id;type:bigint;not null;default:0"`
	// DeviceFingerprint 空串 = 本地；非空 = 执行这条消息那台机器的规范设备指纹
	// （与 agent_backends.device_fingerprint 同值）。给 chat 历史保留"这条消息当时跑在哪台机器"。
	DeviceFingerprint string `gorm:"column:device_fingerprint;type:text;not null;default:''"`
	Role              string `gorm:"column:role;type:text;not null"`
	// BlocksJSON 是消息正文的内存形态(StoredBlock 数组的 JSON)。它**不是一列**:
	// 正文按「一块一行」存在 chat_message_blocks,由仓储在读时重组、写时拆解,
	// 使服务层照旧只与 GetBlocks() / SetBlocks() 打交道。
	BlocksJSON          string `gorm:"-"`
	Model               string `gorm:"column:model;type:text;not null;default:''"`
	PromptTokens        int    `gorm:"column:prompt_tokens;type:int;not null;default:0"`
	CompletionTokens    int    `gorm:"column:completion_tokens;type:int;not null;default:0"`
	CachedTokens        int    `gorm:"column:cached_tokens;type:int;not null;default:0"`
	CacheCreationTokens int    `gorm:"column:cache_creation_tokens;type:int;not null;default:0"`
	ReasoningTokens     int    `gorm:"column:reasoning_tokens;type:int;not null;default:0"`
	// TotalInputTokens runtime translator 按 family 聚合的本次 API call 输入大小;
	// Anthropic = prompt+cached+cacheCreation;OpenAI = prompt(cached 是 prompt 子集)。
	// 替代前端"自行家族判断"硬编码;chat_svc UsageUpdate handler 直接写。
	TotalInputTokens int     `gorm:"column:total_input_tokens;type:int;not null;default:0"`
	DurationMs       int     `gorm:"column:duration_ms;type:int;not null;default:0"`
	FirstTokenMs     int     `gorm:"column:first_token_ms;type:int;not null;default:0"`
	TokensPerSec     float64 `gorm:"column:tokens_per_sec;type:real;not null;default:0"`
	ForkAnchor       string  `gorm:"column:fork_anchor;type:text;not null;default:''"`
	ErrorText        string  `gorm:"column:error_text;type:text;not null;default:''"`
	Seq              int     `gorm:"column:seq;type:int;not null;default:0"`
	Createtime       int64   `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime       int64   `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

func (*Message) TableName() string { return "chat_messages" }

func (m *Message) Check(ctx context.Context) error {
	if m == nil {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if m.SessionID <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := allowedMessageRoles[m.Role]; !ok {
		return i18n.NewError(ctx, code.ChatInvalidRole)
	}
	if m.BlocksJSON == "" {
		return i18n.NewError(ctx, code.ChatBlocksMalformed)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(m.BlocksJSON), &raw); err != nil {
		return i18n.NewError(ctx, code.ChatBlocksMalformed)
	}
	return nil
}

// GetBlocks decodes blocks_json through the cago/agents block registry.
func (m *Message) GetBlocks() ([]blocks.ContentBlock, error) {
	if m == nil || m.BlocksJSON == "" {
		return nil, nil
	}
	var stored []blocks.StoredBlock
	if err := json.Unmarshal([]byte(m.BlocksJSON), &stored); err != nil {
		return nil, err
	}
	return blocks.DecodeAll(stored)
}

// SetBlocks encodes through the registry and stores as blocks_json.
func (m *Message) SetBlocks(bs []blocks.ContentBlock) error {
	stored, err := blocks.EncodeAll(bs)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	m.BlocksJSON = string(buf)
	return nil
}
