package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestUserAskBlock_TypeAndAudience(t *testing.T) {
	Convey("UserAskBlock 类型 + Audience", t, func() {
		b := UserAskBlock{}
		So(b.Type(), ShouldEqual, "user_ask")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)
	})
}

func TestUserAskBlock_FieldsMutable(t *testing.T) {
	Convey("UserAskBlock 可 mutate Answered/Answers/Skipped", t, func() {
		b := &UserAskBlock{}
		b.Answered = true
		b.Answers = []AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"A"}}}
		So(b.Answered, ShouldBeTrue)
		So(b.Skipped, ShouldBeFalse)
		So(b.Answers, ShouldHaveLength, 1)
		So(b.Answers[0].Labels[0], ShouldEqual, "A")
	})
}

func TestUserAskBlock_FactoryRegistered(t *testing.T) {
	Convey("UserAskBlock 已通过 init() 注册到 cago factory; Encode/Decode round-trip", t, func() {
		b := &UserAskBlock{RequestID: "r-1", Questions: []AskQuestionDTO{{Question: "q?"}}}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		So(sb.Type, ShouldEqual, "user_ask")

		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(UserAskBlock)
		So(ok, ShouldBeTrue)
		So(got.RequestID, ShouldEqual, "r-1")
	})
}

func TestUserAskQuestionDTOConversion(t *testing.T) {
	Convey("runtime questions convert to persistence DTOs", t, func() {
		got := QuestionsFromRuntime([]agentruntime.AskQuestion{{
			ID:          "q-1",
			Question:    "Deploy?",
			Header:      "Confirm",
			MultiSelect: true,
			IsOther:     true,
			IsSecret:    true,
			Options: []agentruntime.AskOption{{
				Label:       "yes",
				Description: "ship it",
				Preview:     "deploy now",
			}},
		}})

		So(got, ShouldHaveLength, 1)
		So(got[0].ID, ShouldEqual, "q-1")
		So(got[0].Question, ShouldEqual, "Deploy?")
		So(got[0].Header, ShouldEqual, "Confirm")
		So(got[0].MultiSelect, ShouldBeTrue)
		So(got[0].IsOther, ShouldBeTrue)
		So(got[0].IsSecret, ShouldBeTrue)
		So(got[0].Options, ShouldHaveLength, 1)
		So(got[0].Options[0].Label, ShouldEqual, "yes")
		So(got[0].Options[0].Description, ShouldEqual, "ship it")
		So(got[0].Options[0].Preview, ShouldEqual, "deploy now")
	})

	Convey("empty question input returns nil", t, func() {
		So(QuestionsFromRuntime(nil), ShouldBeNil)
	})
}

func TestUserAskAnswerDTOConversion(t *testing.T) {
	Convey("runtime answers convert to persistence DTOs with copied labels", t, func() {
		labels := []string{"A", "B"}
		got := AnswersFromRuntime([]agentruntime.AskAnswer{{
			QuestionIndex: 2,
			Labels:        labels,
			OtherText:     "custom",
		}})
		labels[0] = "mutated"

		So(got, ShouldHaveLength, 1)
		So(got[0].QuestionIndex, ShouldEqual, 2)
		So(got[0].Labels, ShouldResemble, []string{"A", "B"})
		So(got[0].OtherText, ShouldEqual, "custom")
	})

	Convey("DTO answers convert back to runtime answers with copied labels", t, func() {
		labels := []string{"yes"}
		got := AnswersToRuntime([]AskAnswerDTO{{
			QuestionIndex: 1,
			Labels:        labels,
			OtherText:     "manual",
		}})
		labels[0] = "changed"

		So(got, ShouldHaveLength, 1)
		So(got[0].QuestionIndex, ShouldEqual, 1)
		So(got[0].Labels, ShouldResemble, []string{"yes"})
		So(got[0].OtherText, ShouldEqual, "manual")
	})

	Convey("empty answer inputs return nil", t, func() {
		So(AnswersFromRuntime(nil), ShouldBeNil)
		So(AnswersToRuntime(nil), ShouldBeNil)
	})
}
