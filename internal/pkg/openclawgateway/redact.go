package openclawgateway

import (
	"bytes"
	"encoding/json"
)

const redactedAnswer = "[redacted]"

var answersKey = []byte(`"answers"`)

// redactQuestionAnswers returns the raw-frame debug form of a Gateway frame
// with every question answer value replaced. Question answers travel as
// {"answers":{"answers":{"<questionId>":["value", ...]}}} in question.resolve
// params, its response, question.resolved events and question.get records;
// an isSecret answer is a credential, and a frame alone does not say which
// question is secret, so all answer values are redacted. Everything else in
// the frame is kept so Debug logging still shows the full exchange.
func redactQuestionAnswers(raw []byte) []byte {
	if !bytes.Contains(raw, answersKey) {
		return raw
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return []byte(`{"redacted":"unparseable frame carrying answers"}`)
	}
	if !redactAnswerValues(value) {
		return raw
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"redacted":"unencodable frame carrying answers"}`)
	}
	return redacted
}

// redactAnswerValues walks a decoded frame and blanks the values of every
// answers.answers map it finds, reporting whether anything changed.
func redactAnswerValues(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "answers" {
				if wrapper, ok := child.(map[string]any); ok {
					if byQuestion, ok := wrapper["answers"].(map[string]any); ok {
						for questionID, values := range byQuestion {
							list, ok := values.([]any)
							if !ok {
								byQuestion[questionID] = redactedAnswer
								changed = true
								continue
							}
							for i := range list {
								list[i] = redactedAnswer
								changed = true
							}
						}
						continue
					}
				}
			}
			if redactAnswerValues(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if redactAnswerValues(child) {
				changed = true
			}
		}
	}
	return changed
}
