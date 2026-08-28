// Package blocks 集中 chat_svc 的 cago 自定义 ContentBlock 类型,Audience 一律 ToUI。
// 这些 block 持久化到 chat_messages.blocks_json,LLM context 不可见(由 cago
// LoadConversation 按 Audience 过滤)。
package blocks

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// UserAskBlock (旧名 AskUserQuestionBlock) 持久化 user.ask 交互全态:问题 + 答案 + 跳过。
//
// ToolCallID 关联到同 turn 内 raw ToolUseBlock.ID;race 情况下(control_request 比
// tool_use 先到)可能为空,前端按 RequestID 占位、等 tool_use 帧到了 merge。
type UserAskBlock struct {
	RequestID  string           `json:"request_id"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Questions  []AskQuestionDTO `json:"questions"`
	Answered   bool             `json:"answered,omitempty"`
	Answers    []AskAnswerDTO   `json:"answers,omitempty"`
	Skipped    bool             `json:"skipped,omitempty"`
	Expired    bool             `json:"expired,omitempty"`
}

func (UserAskBlock) Type() string                      { return "user_ask" }
func (UserAskBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[UserAskBlock]() }

// AskQuestionDTO / AskOptionDTO / AskAnswerDTO 是 wire/persistence schema,
// 与 agentruntime.AskQuestion 等同形。
type AskQuestionDTO struct {
	ID          string         `json:"id,omitempty"`
	Question    string         `json:"question"`
	Header      string         `json:"header,omitempty"`
	MultiSelect bool           `json:"multiSelect,omitempty"`
	IsOther     bool           `json:"isOther,omitempty"`
	IsSecret    bool           `json:"isSecret,omitempty"`
	Options     []AskOptionDTO `json:"options"`
}

type AskOptionDTO struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

type AskAnswerDTO struct {
	QuestionIndex int      `json:"questionIndex"`
	Labels        []string `json:"labels"`
	OtherText     string   `json:"otherText,omitempty"`
}

func QuestionsFromRuntime(qs []agentruntime.AskQuestion) []AskQuestionDTO {
	if len(qs) == 0 {
		return nil
	}
	out := make([]AskQuestionDTO, 0, len(qs))
	for _, q := range qs {
		opts := make([]AskOptionDTO, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, AskOptionDTO{
				Label:       o.Label,
				Description: o.Description,
				Preview:     o.Preview,
			})
		}
		out = append(out, AskQuestionDTO{
			ID:          q.ID,
			Question:    q.Question,
			Header:      q.Header,
			MultiSelect: q.MultiSelect,
			IsOther:     q.IsOther,
			IsSecret:    q.IsSecret,
			Options:     opts,
		})
	}
	return out
}

func AnswersFromRuntime(ans []agentruntime.AskAnswer) []AskAnswerDTO {
	if len(ans) == 0 {
		return nil
	}
	out := make([]AskAnswerDTO, 0, len(ans))
	for _, a := range ans {
		out = append(out, AskAnswerDTO{
			QuestionIndex: a.QuestionIndex,
			Labels:        append([]string(nil), a.Labels...),
			OtherText:     a.OtherText,
		})
	}
	return out
}

func AnswersToRuntime(ans []AskAnswerDTO) []agentruntime.AskAnswer {
	if len(ans) == 0 {
		return nil
	}
	out := make([]agentruntime.AskAnswer, 0, len(ans))
	for _, a := range ans {
		out = append(out, agentruntime.AskAnswer{
			QuestionIndex: a.QuestionIndex,
			Labels:        append([]string(nil), a.Labels...),
			OtherText:     a.OtherText,
		})
	}
	return out
}
