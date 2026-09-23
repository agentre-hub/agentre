package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OtherAnswerLabel 是 Claude Code AskUserQuestion 工具在 UI 端
// "自定义答案"对应的魔法 label。多 backend 共用该约定，确保前端
// 同一种 AnswerSubmit payload 在不同 backend 里都被正确解释。
const OtherAnswerLabel = "__other__"

// AskQuestion 一次 AskUserQuestion 调用中的单个问题，
// backend-agnostic：Claude Code 内置版、Codex function tool、内置 Agent
// 注册的 ask_user_question tool 都翻译到这里。
//
// 「其他」自由输入由后端逐题决定:DisallowOther=true 表示该后端不接受这道题的
// 非选项答案(Hermes 有选项的题;OpenClaw 按题目 isOther 取反),卡片就不画自由
// 输入 —— 无选项的题例外,文本输入就是它唯一的答法。零值保住 Claude Code / Codex
// 一贯的「总是提供其他」,IsOther 只是后端协议原值,不参与这项判断。
//
// IsSecret=true 的题以遮蔽输入作答;它的答案只发回后端,留档与回显一律经
// RedactSecretAnswers 剥掉内容,只剩「已作答」。
type AskQuestion struct {
	ID            string
	Question      string
	Header        string
	MultiSelect   bool
	IsOther       bool
	IsSecret      bool
	DisallowOther bool
	Options       []AskOption
}

// AskOption 一个可选答案。Preview 仅 single-select 时有意义。
type AskOption struct {
	Label       string
	Description string
	Preview     string
}

// AskAnswer 用户的单条回答。
//
//   - 单选：Labels 长度 1。
//   - 多选：Labels 长度 ≥1。
//   - "自定义答案"：Labels 含 OtherAnswerLabel，且 OtherText 必填；
//     BuildUpdatedInputAnswers 会把 OtherAnswerLabel 替换成 OtherText 后再做 csv。
type AskAnswer struct {
	QuestionIndex int
	Labels        []string
	OtherText     string
}

type askUserQuestionInputRaw struct {
	Questions []struct {
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
			Preview     string `json:"preview"`
		} `json:"options"`
	} `json:"questions"`
}

// ParseAskUserQuestionInput 把任意 backend 的 tool_use.input bytes
// 反序列化成统一 []AskQuestion。失败原因严格分类，便于 caller
// 区分协议错和业务空（empty questions）。
func ParseAskUserQuestionInput(raw json.RawMessage) ([]AskQuestion, error) {
	if len(raw) == 0 {
		return nil, errors.New("agentruntime: empty AskUserQuestion input")
	}
	var parsed askUserQuestionInputRaw
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("agentruntime: parse AskUserQuestion input: %w", err)
	}
	if len(parsed.Questions) == 0 {
		return nil, errors.New("agentruntime: AskUserQuestion has no questions")
	}
	out := make([]AskQuestion, 0, len(parsed.Questions))
	for _, q := range parsed.Questions {
		opts := make([]AskOption, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, AskOption{
				Label:       o.Label,
				Description: o.Description,
				Preview:     o.Preview,
			})
		}
		out = append(out, AskQuestion{
			Question:    q.Question,
			Header:      q.Header,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		})
	}
	return out, nil
}

// BuildUpdatedInputAnswers 把用户在 UI 选好的 answers 拼成 Claude Code
// 期待的 {[questionText]: "csv-of-labels"} map，写进 control_response 的
// updatedInput.answers。
//
// 关键约束（hapi 项目踩过的坑）：
//   - answers key 必须是 question 字面文本，不是 index
//   - 多选时 value 用逗号拼接
//   - __other__ 在 csv 里替换为 OtherText 而非保留魔法 label
//   - 空 answers / 空 Labels 必须返回 error —— 否则 Claude Code 会
//     emit "User has answered your questions: ." 然后 turn 静默挂死
func BuildUpdatedInputAnswers(questions []AskQuestion, answers []AskAnswer) (map[string]string, error) {
	if len(answers) == 0 {
		return nil, errors.New("agentruntime: empty answers (would silently hang the turn)")
	}
	result := make(map[string]string, len(answers))
	for _, ans := range answers {
		if ans.QuestionIndex < 0 || ans.QuestionIndex >= len(questions) {
			return nil, fmt.Errorf("agentruntime: answer question index %d out of range (have %d questions)", ans.QuestionIndex, len(questions))
		}
		if len(ans.Labels) == 0 {
			return nil, fmt.Errorf("agentruntime: question %d has no selected labels", ans.QuestionIndex)
		}
		seen := make(map[string]struct{}, len(ans.Labels))
		parts := make([]string, 0, len(ans.Labels))
		for _, label := range ans.Labels {
			value := label
			if label == OtherAnswerLabel {
				if strings.TrimSpace(ans.OtherText) == "" {
					return nil, fmt.Errorf("agentruntime: question %d picked %q with empty OtherText", ans.QuestionIndex, OtherAnswerLabel)
				}
				value = ans.OtherText
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			parts = append(parts, value)
		}
		result[questions[ans.QuestionIndex].Question] = strings.Join(parts, ",")
	}
	return result, nil
}

// RedactSecretAnswers 返回答复的留档形态:秘密问题的答复只保留题号 —— 这就是
// 「已作答、不含内容」的表示 —— 其余原样。生产者把原答复发给后端,把这份交给
// UserAskResolved;传入切片不被改写。题号越界的答复原样保留,不猜它是否秘密。
func RedactSecretAnswers(questions []AskQuestion, answers []AskAnswer) []AskAnswer {
	if answers == nil {
		return nil
	}
	out := make([]AskAnswer, 0, len(answers))
	for _, ans := range answers {
		if ans.QuestionIndex >= 0 && ans.QuestionIndex < len(questions) && questions[ans.QuestionIndex].IsSecret {
			out = append(out, AskAnswer{QuestionIndex: ans.QuestionIndex})
			continue
		}
		out = append(out, ans)
	}
	return out
}
