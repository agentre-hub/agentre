package agentruntime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAskUserQuestionInput(t *testing.T) {
	t.Run("single question single-select with preview", func(t *testing.T) {
		raw := json.RawMessage(`{
			"questions": [{
				"question": "Which column should we add?",
				"header": "Schema choice",
				"multiSelect": false,
				"options": [
					{"label": "last_read_at int64", "description": "Timestamp.", "preview": "ALTER TABLE..."},
					{"label": "is_read bool", "description": "Flag."}
				]
			}]
		}`)
		got, err := ParseAskUserQuestionInput(raw)
		require.NoError(t, err)
		require.Len(t, got, 1)

		q := got[0]
		assert.Equal(t, "Which column should we add?", q.Question)
		assert.Equal(t, "Schema choice", q.Header)
		assert.False(t, q.MultiSelect)
		require.Len(t, q.Options, 2)
		assert.Equal(t, "last_read_at int64", q.Options[0].Label)
		assert.Equal(t, "Timestamp.", q.Options[0].Description)
		assert.Equal(t, "ALTER TABLE...", q.Options[0].Preview)
		assert.Empty(t, q.Options[1].Preview)
	})

	t.Run("multi-select question", func(t *testing.T) {
		raw := json.RawMessage(`{
			"questions": [{
				"question": "Which platforms?",
				"header": "Target",
				"multiSelect": true,
				"options": [
					{"label": "macOS", "description": ""},
					{"label": "Linux", "description": ""},
					{"label": "Windows", "description": ""}
				]
			}]
		}`)
		got, err := ParseAskUserQuestionInput(raw)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.True(t, got[0].MultiSelect)
		assert.Len(t, got[0].Options, 3)
	})

	t.Run("multiple questions preserved in order", func(t *testing.T) {
		raw := json.RawMessage(`{
			"questions": [
				{"question": "A?", "header": "h1", "options": [{"label": "x"}, {"label": "y"}]},
				{"question": "B?", "header": "h2", "options": [{"label": "p"}, {"label": "q"}]}
			]
		}`)
		got, err := ParseAskUserQuestionInput(raw)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "A?", got[0].Question)
		assert.Equal(t, "B?", got[1].Question)
	})

	t.Run("multiSelect missing → defaults to false", func(t *testing.T) {
		raw := json.RawMessage(`{
			"questions": [{
				"question": "Q?",
				"header": "h",
				"options": [{"label": "x"}, {"label": "y"}]
			}]
		}`)
		got, err := ParseAskUserQuestionInput(raw)
		require.NoError(t, err)
		assert.False(t, got[0].MultiSelect)
	})

	t.Run("invalid JSON → error", func(t *testing.T) {
		_, err := ParseAskUserQuestionInput(json.RawMessage(`{"questions": [`))
		assert.Error(t, err)
	})

	t.Run("nil input → error", func(t *testing.T) {
		_, err := ParseAskUserQuestionInput(nil)
		assert.Error(t, err)
	})

	t.Run("empty questions array → error", func(t *testing.T) {
		_, err := ParseAskUserQuestionInput(json.RawMessage(`{"questions": []}`))
		assert.Error(t, err)
	})
}

func TestBuildUpdatedInputAnswers(t *testing.T) {
	qs := []AskQuestion{
		{
			Question:    "Which column?",
			Header:      "Schema",
			MultiSelect: false,
			Options: []AskOption{
				{Label: "last_read_at int64"},
				{Label: "is_read bool"},
			},
		},
		{
			Question:    "Which platforms?",
			Header:      "Targets",
			MultiSelect: true,
			Options: []AskOption{
				{Label: "macOS"}, {Label: "Linux"}, {Label: "Windows"},
			},
		},
	}

	t.Run("single-select picks first option", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"last_read_at int64"}},
		}
		got, err := BuildUpdatedInputAnswers(qs[:1], ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"Which column?": "last_read_at int64"}, got)
	})

	t.Run("multi-select joins labels with comma", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"macOS", "Linux"}},
		}
		got, err := BuildUpdatedInputAnswers(qs[1:], ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"Which platforms?": "macOS,Linux"}, got)
	})

	t.Run("__other__ label replaced with OtherText", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"__other__"}, OtherText: "TEXT column with isolated NULL semantics"},
		}
		got, err := BuildUpdatedInputAnswers(qs[:1], ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"Which column?": "TEXT column with isolated NULL semantics"}, got)
	})

	t.Run("multi-select with __other__ mixed in csv", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"macOS", "__other__"}, OtherText: "FreeBSD"},
		}
		got, err := BuildUpdatedInputAnswers(qs[1:], ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"Which platforms?": "macOS,FreeBSD"}, got)
	})

	t.Run("multiple questions all answered", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"is_read bool"}},
			{QuestionIndex: 1, Labels: []string{"Linux", "Windows"}},
		}
		got, err := BuildUpdatedInputAnswers(qs, ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"Which column?":    "is_read bool",
			"Which platforms?": "Linux,Windows",
		}, got)
	})

	t.Run("empty answers → error to prevent silent turn hang", func(t *testing.T) {
		_, err := BuildUpdatedInputAnswers(qs, nil)
		assert.Error(t, err, "must reject empty answers per hapi gotcha #4")
	})

	t.Run("answer with empty Labels → error", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: nil},
		}
		_, err := BuildUpdatedInputAnswers(qs[:1], ans)
		assert.Error(t, err)
	})

	t.Run("answer index out of range → error", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 5, Labels: []string{"x"}},
		}
		_, err := BuildUpdatedInputAnswers(qs[:1], ans)
		assert.Error(t, err)
	})

	t.Run("duplicate label in multi-select dedup but preserve order", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"macOS", "Linux", "macOS"}},
		}
		got, err := BuildUpdatedInputAnswers(qs[1:], ans)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"Which platforms?": "macOS,Linux"}, got)
	})

	t.Run("__other__ with empty OtherText → error", func(t *testing.T) {
		ans := []AskAnswer{
			{QuestionIndex: 0, Labels: []string{"__other__"}, OtherText: ""},
		}
		_, err := BuildUpdatedInputAnswers(qs[:1], ans)
		assert.Error(t, err)
	})
}

// Given 一题秘密问题与一题普通问题的答复,When 生产者按问题剥秘密答案,Then 秘密那题
// 只留题号(表示「已作答」)、不带任何选项或文本,普通那题原样保留;输入切片不被改写。
func TestRedactSecretAnswers(t *testing.T) {
	questions := []AskQuestion{
		{ID: "q1", Question: "Region", Options: []AskOption{{Label: "us"}}},
		{ID: "q2", Question: "Token?", IsSecret: true},
	}
	answers := []AskAnswer{
		{QuestionIndex: 0, Labels: []string{"us"}},
		{QuestionIndex: 1, Labels: []string{OtherAnswerLabel}, OtherText: "s3cr3t"},
	}

	got := RedactSecretAnswers(questions, answers)

	assert.Equal(t, []AskAnswer{
		{QuestionIndex: 0, Labels: []string{"us"}},
		{QuestionIndex: 1},
	}, got)
	assert.Equal(t, "s3cr3t", answers[1].OtherText, "caller's answers must stay intact for the backend reply")

	t.Run("out-of-range index is kept as-is rather than guessed secret", func(t *testing.T) {
		got := RedactSecretAnswers(questions, []AskAnswer{{QuestionIndex: 5, Labels: []string{"x"}}})
		assert.Equal(t, []AskAnswer{{QuestionIndex: 5, Labels: []string{"x"}}}, got)
	})
	t.Run("no answers stays nil", func(t *testing.T) {
		assert.Nil(t, RedactSecretAnswers(questions, nil))
	})
}
