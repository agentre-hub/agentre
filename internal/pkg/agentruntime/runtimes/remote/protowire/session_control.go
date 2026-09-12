package protowire

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func PendingWaitersResponseFromProto(response *agentrewire.SessionPendingWaitersResponse) wire.SessionPendingWaitersResult {
	out := wire.SessionPendingWaitersResult{}
	for _, permission := range response.GetToolPermissions() {
		out.ToolPermissions = append(out.ToolPermissions, agentruntime.PendingToolPermission{RequestID: permission.GetRequestId(), ToolName: permission.GetToolName(), Input: append([]byte(nil), permission.GetInput()...)})
	}
	for _, ask := range response.GetAskUserQuestions() {
		item := agentruntime.PendingAskUserQuestion{RequestID: ask.GetRequestId()}
		for _, question := range ask.GetQuestions() {
			item.Questions = append(item.Questions, decodeAskQuestion(question))
		}
		out.AskUserQuestions = append(out.AskUserQuestions, item)
	}
	return out
}

// WaiterSnapshotToProto 把 backend 此刻仍在阻塞的待决策快照翻上线。
func WaiterSnapshotToProto(value agentruntime.WaiterSnapshot) *agentrewire.SessionPendingWaitersResponse {
	out := &agentrewire.SessionPendingWaitersResponse{}
	for _, permission := range value.ToolPermissions {
		out.ToolPermissions = append(out.ToolPermissions, &agentrewire.PendingToolPermission{RequestId: permission.RequestID, ToolName: permission.ToolName, Input: append([]byte(nil), permission.Input...)})
	}
	for _, ask := range value.AskUserQuestions {
		out.AskUserQuestions = append(out.AskUserQuestions, &agentrewire.PendingAskUserQuestion{RequestId: ask.RequestID, Questions: AskQuestionsToProto(ask.Questions)})
	}
	return out
}

func PendingWaitersResponseToProto(value wire.SessionPendingWaitersResult) *agentrewire.SessionPendingWaitersResponse {
	out := &agentrewire.SessionPendingWaitersResponse{}
	for _, permission := range value.ToolPermissions {
		out.ToolPermissions = append(out.ToolPermissions, &agentrewire.PendingToolPermission{RequestId: permission.RequestID, ToolName: permission.ToolName, Input: append([]byte(nil), permission.Input...)})
	}
	for _, ask := range value.AskUserQuestions {
		item := &agentrewire.PendingAskUserQuestion{RequestId: ask.RequestID}
		item.Questions = AskQuestionsToProto(ask.Questions)
		out.AskUserQuestions = append(out.AskUserQuestions, item)
	}
	return out
}

func encodeAskQuestion(value agentruntime.AskQuestion) *agentrewire.AskQuestion {
	options := make([]*agentrewire.AskOption, 0, len(value.Options))
	for _, option := range value.Options {
		options = append(options, &agentrewire.AskOption{Label: option.Label, Description: option.Description, Preview: option.Preview})
	}
	return &agentrewire.AskQuestion{Id: value.ID, Question: value.Question, Header: value.Header, MultiSelect: value.MultiSelect, IsOther: value.IsOther, IsSecret: value.IsSecret, Options: options}
}

func decodeAskQuestion(value *agentrewire.AskQuestion) agentruntime.AskQuestion {
	out := agentruntime.AskQuestion{ID: value.GetId(), Question: value.GetQuestion(), Header: value.GetHeader(), MultiSelect: value.GetMultiSelect(), IsOther: value.GetIsOther(), IsSecret: value.GetIsSecret()}
	for _, option := range value.GetOptions() {
		out.Options = append(out.Options, agentruntime.AskOption{Label: option.GetLabel(), Description: option.GetDescription(), Preview: option.GetPreview()})
	}
	return out
}

func AskQuestionsToProto(values []agentruntime.AskQuestion) []*agentrewire.AskQuestion {
	out := make([]*agentrewire.AskQuestion, 0, len(values))
	for _, value := range values {
		out = append(out, encodeAskQuestion(value))
	}
	return out
}

func AskAnswersToProto(values []agentruntime.AskAnswer) []*agentrewire.AskAnswer {
	out := make([]*agentrewire.AskAnswer, 0, len(values))
	for _, value := range values {
		out = append(out, &agentrewire.AskAnswer{QuestionIndex: int32(value.QuestionIndex), Labels: value.Labels, OtherText: value.OtherText})
	}
	return out
}

// AskQuestionsFromProto 把线上问题列表翻回领域值。它与 AskQuestionsToProto 成对,
// 提交答案这条路径的收端(daemon handler)照它读请求,不再各自抄一份逐字段搬运。
func AskQuestionsFromProto(values []*agentrewire.AskQuestion) []agentruntime.AskQuestion {
	out := make([]agentruntime.AskQuestion, 0, len(values))
	for _, value := range values {
		out = append(out, decodeAskQuestion(value))
	}
	return out
}

// AskAnswersFromProto 把线上答案列表翻回领域值。
func AskAnswersFromProto(values []*agentrewire.AskAnswer) []agentruntime.AskAnswer {
	out := make([]agentruntime.AskAnswer, 0, len(values))
	for _, value := range values {
		out = append(out, agentruntime.AskAnswer{QuestionIndex: int(value.GetQuestionIndex()), Labels: append([]string(nil), value.GetLabels()...), OtherText: value.GetOtherText()})
	}
	return out
}

// ConsumedSteersToProto 把轮末残留的 pending steer 翻上线。
func ConsumedSteersToProto(values []agentruntime.ConsumedSteer) []*agentrewire.ConsumedSteer {
	out := make([]*agentrewire.ConsumedSteer, 0, len(values))
	for _, value := range values {
		out = append(out, &agentrewire.ConsumedSteer{QueuedId: value.QueuedID, Text: value.Text, SourcePeer: string(value.SourcePeer), SourceName: value.SourceName})
	}
	return out
}

// CapabilitiesToProto 把某个 backend 的能力集翻上线。
func CapabilitiesToProto(value capability.Capabilities) *agentrewire.RuntimeCapabilitiesResponse {
	meta := value.PermissionModeMeta
	out := &agentrewire.RuntimeCapabilitiesResponse{PermissionMode: &agentrewire.PermissionModeMeta{
		AllowedModes:         meta.AllowedModes,
		DefaultMode:          meta.DefaultMode,
		SwitchableDuringTurn: meta.SwitchableDuringTurn,
		Order:                meta.Order,
		LaunchDefaultMode:    meta.LaunchDefaultMode,
	}}
	for name, enabled := range value.Set {
		out.Capabilities = append(out.Capabilities, &agentrewire.CapabilityEntry{Name: string(name), Enabled: enabled})
	}
	return out
}
