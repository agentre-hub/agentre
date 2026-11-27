package chat_svc

import (
	"context"
	"math"
	"slices"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// CtlListFilter 是 agrctl list sessions 的收窄条件;零值 = 不收窄。
type CtlListFilter struct {
	AgentID   int64
	ProjectID int64
	// Device 是 --device 的写法:设备展示名、指纹,或 "local" / "this desktop"(本机)。
	Device string
	Status CtlConversationStatus
	// Limit <= 0 取 ctlListDefaultLimit。
	Limit int
}

const (
	ctlListDefaultLimit = 50
	ctlListMaxLimit     = 500
	ctlListPage         = 100
	// ctlMessageChunk 是读转录时每次向前翻的条数。
	ctlMessageChunk = 200
	// ctlMessagesDefaultTurns 是 --last 的默认轮数。
	ctlMessagesDefaultTurns = 20
	// ctlLastTextRunes 是详情里「最后一轮最终文本摘要」的长度上限。
	ctlLastTextRunes = 500
)

// ctlLocalDeviceRefs 是 --device 里指本机桌面的写法。
var ctlLocalDeviceRefs = []string{ctlLocalDevice, "local"}

func (s *chatSvc) ListCtlConversations(ctx context.Context, f CtlListFilter) ([]*CtlConversation, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = ctlListDefaultLimit
	}
	limit = min(limit, ctlListMaxLimit)
	filter := chat_repo.SessionIndexFilter{}
	if f.AgentID > 0 {
		filter.AgentID = &f.AgentID
	}
	if f.ProjectID > 0 {
		filter.ProjectID = &f.ProjectID
	}
	agentNames := map[int64]string{}
	deviceNames := map[int64]string{}
	var out []*CtlConversation
	for offset := 0; len(out) < limit; offset += ctlListPage {
		page, err := chat_repo.Session().ListIndexPaged(ctx, filter, offset, ctlListPage)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		for _, sess := range page {
			if sess == nil || sess.Status != consts.ACTIVE {
				continue
			}
			c := &CtlConversation{
				SessionID: sess.ID, ConversationID: sess.ConversationID, AgentID: sess.AgentID,
				Status: s.ctlStatus(sess), Title: sess.Title, ProjectID: sess.ProjectID,
				CreatedAt: sess.Createtime, LastMessageAt: sess.LastMessageAt,
			}
			if f.Status != "" && c.Status != f.Status {
				continue
			}
			if sess.RanOnDaemon() {
				name, ok := deviceNames[sess.ExecDeviceID]
				if !ok {
					name = ctlDeviceName(ctx, sess.ExecDeviceID)
					deviceNames[sess.ExecDeviceID] = name
				}
				c.Device = name
			}
			if !ctlDeviceMatches(f.Device, c.Device, string(sess.ExecDeviceFingerprint)) {
				continue
			}
			name, ok := agentNames[sess.AgentID]
			if !ok {
				a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
				if err != nil {
					return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", sess.AgentID))
				}
				if a != nil {
					name = a.Name
				}
				agentNames[sess.AgentID] = name
			}
			c.AgentName = name
			out = append(out, c)
			if len(out) == limit {
				return out, nil
			}
		}
		if len(page) < ctlListPage {
			break
		}
	}
	return out, nil
}

// ctlDeviceMatches 判断一条会话(设备名 device,空 = 本机;指纹 fp)是否落在 --device want 上。
func ctlDeviceMatches(want, device, fp string) bool {
	want = strings.TrimSpace(want)
	switch {
	case want == "":
		return true
	case device == "":
		return slices.Contains(ctlLocalDeviceRefs, want)
	default:
		return want == device || (fp != "" && want == fp)
	}
}

// CtlConversationDetail 是 agrctl get session 的详情:会话行之外再带后端、模型、累计用量与
// 最后一轮最终文本摘要。拿不到的项为零值,由调用方省略。
type CtlConversationDetail struct {
	CtlConversation
	Backend                   string
	ProviderKey, ModelKey     string
	InputTokens, OutputTokens int
	LastText                  string
}

func (s *chatSvc) DetailCtlConversation(ctx context.Context, sessionID int64) (*CtlConversationDetail, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	if sess == nil || sess.Status != consts.ACTIVE {
		return nil, nil
	}
	c, err := s.describeCtlConversation(ctx, sess)
	if err != nil {
		return nil, err
	}
	d := &CtlConversationDetail{CtlConversation: *c, ProviderKey: sess.ProviderKey, ModelKey: sess.ModelKey}
	backendID := sess.ExecAgentBackendID
	if backendID <= 0 {
		a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", sess.AgentID))
		}
		if a != nil {
			backendID = a.AgentBackendID
		}
	}
	if backendID > 0 {
		be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", backendID))
		}
		if be != nil {
			d.Backend = be.Name
		}
	}
	metas, err := transcript_repo.Message().ListMeta(ctx, sess.ID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sess.ID))
	}
	for _, m := range metas {
		d.InputTokens += m.TotalInputTokens
		d.OutputTokens += m.CompletionTokens
	}
	text, err := s.FinalAssistantText(ctx, c.LastAssistantMessageID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sess.ID))
	}
	if r := []rune(text); len(r) > ctlLastTextRunes {
		text = string(r[:ctlLastTextRunes]) + "…"
	}
	d.LastText = text
	return d, nil
}

// CtlMessagesQuery 是 agrctl messages 的取数条件。
type CtlMessagesQuery struct {
	// Last 是最近多少轮(一轮 = 一条用户消息起到下一条用户消息前);<= 0 取默认 20。
	Last int
	// BeforeSeq > 0:只取 seq 小于它的消息(上一页的 BeforeSeq)。
	BeforeSeq int
	Full      bool
}

// CtlMessageBlock 是 --full 里一条消息的一个块:text / tool_use / tool_result。
type CtlMessageBlock struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Name    string         `json:"name,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	IsError bool           `json:"isError,omitempty"`
}

// CtlMessage 是一条消息:Text 是它全部文本块的拼接;Blocks 只在 --full 时有。
type CtlMessage struct {
	Seq    int
	Role   string
	At     int64
	Text   string
	Blocks []CtlMessageBlock
}

// CtlMessagesPage 是一页消息(时间正序)。HasMore 时 BeforeSeq 是下一页的游标。
type CtlMessagesPage struct {
	Messages  []CtlMessage
	HasMore   bool
	BeforeSeq int
}

func (s *chatSvc) ListCtlMessages(ctx context.Context, sessionID int64, q CtlMessagesQuery) (*CtlMessagesPage, error) {
	turns := q.Last
	if turns <= 0 {
		turns = ctlMessagesDefaultTurns
	}
	cursor := q.BeforeSeq
	if cursor <= 0 {
		cursor = math.MaxInt32
	}
	var (
		desc    []*chat_entity.Message
		users   int
		cut     = -1
		hasMore bool
	)
	for exhausted := false; cut < 0 && !exhausted; {
		rows, err := transcript_repo.Message().ListMetaBefore(ctx, sessionID, cursor, ctlMessageChunk+1)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
		}
		if len(rows) > ctlMessageChunk {
			rows = rows[:ctlMessageChunk]
		} else {
			exhausted = true
		}
		for i, r := range rows {
			desc = append(desc, r)
			if r.Role != "user" {
				continue
			}
			if users++; users == turns {
				cut = len(desc) - 1
				hasMore = i < len(rows)-1 || !exhausted
				break
			}
		}
		if len(rows) == 0 {
			break
		}
		cursor = rows[len(rows)-1].Seq
	}
	page := &CtlMessagesPage{HasMore: hasMore}
	if len(desc) == 0 {
		return page, nil
	}
	if cut >= 0 {
		page.BeforeSeq = desc[cut].Seq
	}
	kept := make([]*chat_entity.Message, len(desc))
	for i, m := range desc {
		kept[len(desc)-1-i] = m
	}
	if err := transcript_repo.Message().FillBlocks(ctx, kept); err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	page.Messages = make([]CtlMessage, 0, len(kept))
	for _, m := range kept {
		cm, err := ctlMessageOf(m, q.Full)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("messageId", m.ID))
		}
		page.Messages = append(page.Messages, cm)
	}
	return page, nil
}

func ctlMessageOf(m *chat_entity.Message, full bool) (CtlMessage, error) {
	out := CtlMessage{Seq: m.Seq, Role: m.Role, At: m.Createtime}
	bs, err := m.GetBlocks()
	if err != nil {
		return out, err
	}
	var text strings.Builder
	for _, b := range bs {
		switch t := b.(type) {
		case cagoblocks.TextBlock:
			text.WriteString(t.Text)
			if full {
				out.Blocks = append(out.Blocks, CtlMessageBlock{Type: "text", Text: t.Text})
			}
		case *cagoblocks.TextBlock:
			text.WriteString(t.Text)
			if full {
				out.Blocks = append(out.Blocks, CtlMessageBlock{Type: "text", Text: t.Text})
			}
		case cagoblocks.ToolUseBlock:
			if full {
				out.Blocks = append(out.Blocks, CtlMessageBlock{Type: "tool_use", Name: t.Name, Input: t.Input})
			}
		case cagoblocks.ToolResultBlock:
			if full {
				out.Blocks = append(out.Blocks, CtlMessageBlock{Type: "tool_result", Text: toolResultText(t), IsError: t.IsError})
			}
		}
	}
	out.Text = text.String()
	return out, nil
}

func toolResultText(r cagoblocks.ToolResultBlock) string {
	var sb strings.Builder
	for _, c := range r.Content {
		switch t := c.(type) {
		case cagoblocks.TextBlock:
			sb.WriteString(t.Text)
		case *cagoblocks.TextBlock:
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func (s *chatSvc) AnswerCtlQuestion(ctx context.Context, sessionID int64, requestID string, answers []blocks.AskAnswerDTO) error {
	_, err := s.AnswerUserQuestion(ctx, &AnswerUserQuestionRequest{SessionID: sessionID, RequestID: requestID, Answers: answers})
	return err
}
