package blocks

import (
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 审批卡内容与 ctl 契约之间的那几处换算。桌面端执行者（ctl_svc）与 agentred 的 ctl 代理
// 各出一张同形的卡，措辞只在这里写一份，不各抄一份任其漂移。

// CtlKindNames 是资源类型在卡片、结果行与错误消息里的名字，与 agrctl 的资源名一致。
var CtlKindNames = map[agentrewire.CtlKind]string{
	agentrewire.CtlKind_CTL_KIND_AGENT:      "agent",
	agentrewire.CtlKind_CTL_KIND_DEPARTMENT: "department",
	agentrewire.CtlKind_CTL_KIND_PROJECT:    "project",
	agentrewire.CtlKind_CTL_KIND_PROVIDER:   "provider",
	agentrewire.CtlKind_CTL_KIND_MODEL:      "model",
	agentrewire.CtlKind_CTL_KIND_BACKEND:    "backend",
}

// CtlOpName 是 create | update | delete；不认识的 op 原样写出枚举名。
func CtlOpName(op agentrewire.CtlOp) string {
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return "create"
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "update"
	case agentrewire.CtlOp_CTL_OP_DELETE:
		return "delete"
	}
	return op.String()
}

// RedactCtlCommand 是卡上的命令行：不信客户端已经脱敏，请求里带的密钥明文若出现在
// 命令行里，一律换成 …。
func RedactCtlCommand(req *agentrewire.CtlWriteRequest) string {
	command := req.GetCommand()
	for _, secret := range []string{req.GetResource().GetProvider().GetApiKey(), req.GetResource().GetBackend().GetToken()} {
		if secret != "" {
			command = strings.ReplaceAll(command, secret, "…")
		}
	}
	return command
}

// NewCtlApprovalChange 把执行者算出的一条变更转成卡上的一条；cascade 只在级联删除部门时给。
func NewCtlApprovalChange(c *agentrewire.CtlChange, cascade *CtlApprovalCascade) CtlApprovalChange {
	ch := CtlApprovalChange{Op: CtlOpName(c.GetOp()), Kind: CtlKindNames[c.GetKind()], ID: c.GetId(), Name: c.GetName(), Cascade: cascade}
	for _, f := range c.GetFields() {
		ch.Fields = append(ch.Fields, CtlApprovalField{Field: f.GetField(), Before: f.Before, After: f.After, Secret: f.GetSecret()})
	}
	return ch
}

// CtlResultText 是批准并写入之后卡上的结果行；c 为 nil 时为空。
func CtlResultText(c *agentrewire.CtlChange) string {
	if c == nil {
		return ""
	}
	subject := CtlKindNames[c.GetKind()] + " " + c.GetName()
	switch c.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return fmt.Sprintf("已创建 %s（id %d）", subject, c.GetId())
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "已更新 " + subject
	default:
		return "已删除 " + subject
	}
}
