package chat_entity

import "github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"

// Message 的存储实体（消息 + 块）已抽成独立域 transcript_entity，两个宿主（桌面端 /
// agentred）共用同一份实现（决策 8）。这里保留一个类型别名，让尚未纳入本轮范围的
// 调用方（internal/bootstrap、internal/peer、chat_import_svc 等）不必跟着改 import
// path —— Message 与 transcript_entity.Message 是完全同一个类型，不是两份拷贝。
type Message = transcript_entity.Message

// MessageTextMaxBytes 转发自 transcript_entity，保持既有调用方（chat_svc 的入站文本
// 长度校验）不必更名。
const MessageTextMaxBytes = transcript_entity.MessageTextMaxBytes
