package chat_entity

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/cago-frame/agents/agent/blocks"
)

// BlockCompressThreshold 是块正文的压缩阈值(字节)。低于等于它的正文原样存储。
//
// 4 KiB 来自实测:该阈值覆盖 74.7% 的字节而只需处理 10.4% 的块。全量压缩对均值
// 660 字节的小块收益有限,64 KiB 只覆盖 26.2% 的字节。
const BlockCompressThreshold = 4 * 1024

// 块正文的编码方式。codec 列存的就是这些取值。
const (
	// BlockCodecRaw 正文原样存储(未超阈值,或压缩后反而变大)。
	BlockCodecRaw = 0
	// BlockCodecDeflate 正文以 RFC 1951 deflate 流存储。
	BlockCodecDeflate = 1
)

// MessageBlock 是消息正文数组里的一个块,一块一行。
//
// (message_id, idx) 是自然键:idx 是块在原数组中的下标,决定重组顺序。块集合与宿主
// 消息同生共死 —— 消息被物理删除时块行随之删除,任何时刻不允许存在没有宿主消息的块行。
//
// ToolCallID 是定位键,由仓储按块类型填充(subagent_state 填它的 parent_tool_call_id,
// 工具类块填它自身或它所应答的工具调用 id,其余类型留空);空值不进索引。
type MessageBlock struct {
	MessageID  int64  `gorm:"column:message_id;type:bigint;not null;primaryKey"`
	Idx        int    `gorm:"column:idx;type:int;not null;primaryKey"`
	Type       string `gorm:"column:type;type:text;not null;default:''"`
	ToolCallID string `gorm:"column:tool_call_id;type:text;not null;default:''"`
	Codec      int    `gorm:"column:codec;type:int;not null;default:0"`
	Data       []byte `gorm:"column:data;type:blob;not null"`
}

// TableName 绑定表名。
func (*MessageBlock) TableName() string { return "chat_message_blocks" }

// EncodeBlockData 把块正文编码成落库形态,返回 codec 与要写进 data 列的字节。
//
// 超过阈值时压缩;未超过、或压缩后反而变大时原样存储(压缩不是无条件更省 —— 已经是
// 高熵内容的块压完会变大)。调用方永远只需把返回的两个值原样落库。
func EncodeBlockData(raw []byte) (int, []byte) {
	if len(raw) <= BlockCompressThreshold {
		return BlockCodecRaw, raw
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return BlockCodecRaw, raw
	}
	if _, err := w.Write(raw); err != nil {
		return BlockCodecRaw, raw
	}
	if err := w.Close(); err != nil {
		return BlockCodecRaw, raw
	}
	if buf.Len() >= len(raw) {
		return BlockCodecRaw, raw
	}
	return BlockCodecDeflate, buf.Bytes()
}

// DecodeBlockData 是 EncodeBlockData 的逆运算:按 codec 还原块正文。
// 未知 codec 报错而不是把字节原样交出去 —— 静默返回压缩流会让上层拿到无法解析的正文。
func DecodeBlockData(codec int, data []byte) ([]byte, error) {
	switch codec {
	case BlockCodecRaw:
		return data, nil
	case BlockCodecDeflate:
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
		if err != nil {
			return nil, fmt.Errorf("decode message block: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown message block codec %d", codec)
	}
}

// 定位键取法不同的三种块类型。
const (
	// BlockTypeSubagentState 是后台子代理的状态卡,定位键取它的 parent_tool_call_id。
	BlockTypeSubagentState = "subagent_state"
	blockTypeToolUse       = "tool_use"
	blockTypeToolResult    = "tool_result"
)

// blockLocatorProbe 只解出定位键涉及的几个字段;块的其余字段原样留在 data 里。
type blockLocatorProbe struct {
	ID string `json:"id"`
	// ToolUseID 是**工具结果块自己的载荷键** tool_use_id(它应答的那次调用),与下面
	// 那个 tool_call_id 是两个不同的载荷字段,不是同一个名字的两种写法。载荷键不在
	// 本轮改名范围内(决策 21)。
	ToolUseID        string `json:"tool_use_id"`
	ToolCallID       string `json:"tool_call_id"`
	ParentToolCallID string `json:"parent_tool_call_id"`
}

// blockToolCallID 按块类型推出定位键:subagent_state 用它的 parent_tool_call_id,
// 工具调用用它自身的 id,工具结果用它所应答的 tool_use_id,其余类型看有没有
// tool_call_id 字段(审批 / 嵌套工具这类块有),都没有就留空。
//
// 正文不是 JSON 对象时留空:定位键只是索引,取不到不影响正文照常存储。
func blockToolCallID(typ string, data []byte) string {
	var probe blockLocatorProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	switch typ {
	case BlockTypeSubagentState:
		return probe.ParentToolCallID
	case blockTypeToolUse:
		return probe.ID
	case blockTypeToolResult:
		return probe.ToolUseID
	default:
		return probe.ToolCallID
	}
}

// SplitBlocksJSON 把一条消息的正文拆成块行:idx 就是块在数组里的下标,
// data 按阈值压缩存储。
func SplitBlocksJSON(messageID int64, blocksJSON string) ([]*MessageBlock, error) {
	if blocksJSON == "" {
		return nil, nil
	}
	var stored []blocks.StoredBlock
	if err := json.Unmarshal([]byte(blocksJSON), &stored); err != nil {
		return nil, err
	}
	rows := make([]*MessageBlock, 0, len(stored))
	for i := range stored {
		codec, data := EncodeBlockData(stored[i].Data)
		rows = append(rows, &MessageBlock{
			MessageID:  messageID,
			Idx:        i,
			Type:       stored[i].Type,
			ToolCallID: blockToolCallID(stored[i].Type, stored[i].Data),
			Codec:      codec,
			Data:       data,
		})
	}
	return rows, nil
}

// JoinBlocks 把块行按 idx 升序重组成正文 —— 与拆解前逐块字节等价。
func JoinBlocks(rows []*MessageBlock) (string, error) {
	if len(rows) == 0 {
		return "[]", nil
	}
	sorted := make([]*MessageBlock, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Idx < sorted[j].Idx })

	stored := make([]blocks.StoredBlock, 0, len(sorted))
	for _, row := range sorted {
		data, err := DecodeBlockData(row.Codec, row.Data)
		if err != nil {
			return "", err
		}
		stored = append(stored, blocks.StoredBlock{Type: row.Type, Data: data})
	}
	buf, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
