import * as React from "react";
import { useTranslation } from "react-i18next";
import { Check, ShieldAlert, X } from "lucide-react";
import {
  useTranscriptPorts,
  CollapsibleCode,
  TranscriptCard,
  TranscriptCardBody,
  TranscriptPill,
} from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { ToolApprovalData } from "@/stores/chat-streams-store";

// ToolApprovalCard 渲染 agent 内置写工具(org_create_department / org_update_agent /
// ...)的审批卡。视觉对齐 canonical-tool/tool-permission/card.tsx,但走
// 独立组件,按 block.type==="tool_approval" 直接路由(不进 CanonicalToolRouter)。
// toolKey 标识来源工具,供标题/文案与 approved 后处理选择。
//
// status 自身就是 truth:
//   - "pending":渲染入参 pre 块 + 批准/拒绝按钮
//   - "approved"|"denied"|"expired":渲染只读徽标 + result 文本(动态内容原样展示)
// 后端 finalize 已把悬空 pending 落成 expired,前端不按会话活跃度自行推断。
export const ToolApprovalCard: React.FC<{
  approval: ToolApprovalData;
  sessionId: number;
}> = ({ approval, sessionId }) => {
  const { t } = useTranslation();
  const ports = useTranscriptPorts();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const isPending = approval.status === "pending";
  const isApproved = approval.status === "approved";

  const answer = async (allow: boolean) => {
    if (!approval.requestId || submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      // 所有内置写工具审批统一走同一个端口,按 requestId 路由唤醒;
      // 接到哪个后端(桌面端 chat_svc / server 端 relay)由宿主决定。
      await ports.answerToolApproval({
        sessionId,
        requestId: approval.requestId,
        allow,
      });
    } catch {
      // 应答失败:切回可重试态并把错误文案露出来(对齐 tool-permission 卡的内联 error
      // 呈现,不用 toast)。决议落库与唤醒挂起 MCP 调用由后端保证。
      setError(t("toolApproval.submitFailed"));
      setSubmitting(false);
    }
  };

  const inputJson = approval.toolInput
    ? JSON.stringify(approval.toolInput, null, 2)
    : "";

  return (
    <TranscriptCard
      data-testid="tool-approval-card"
      data-selectable-text="true"
      className={cn(
        "text-card-foreground",
        !isPending && !isApproved
          ? "border-destructive/40"
          : "border-status-waiting/40",
      )}
    >
      {/* 静态标题行,非交互(该卡没有折叠/展开),不套 TranscriptCardHeader —— 那是
      个 <button>,套上会制造"可点击"的假象。仅手动把内边距对齐到 px-3.5 py-2.5,
      与其它卡片头视觉对齐。 */}
      <div className="flex items-center gap-2 px-3.5 py-2.5">
        <ShieldAlert
          className={cn(
            "h-4 w-4 shrink-0",
            isPending
              ? "text-status-waiting"
              : isApproved
                ? "text-status-running"
                : "text-destructive",
          )}
        />
        <span data-copyable-control-text="true" className="font-medium">
          {t(`toolApproval.tools.${approval.toolName}`, {
            defaultValue: approval.toolName,
          })}
        </span>
        <span className="text-aux text-muted-foreground">
          {t("toolApproval.title")}
        </span>
        {!isPending && (
          <TranscriptPill
            data-copyable-control-text="true"
            className={cn(
              "ml-auto",
              isApproved
                ? "bg-status-running-bg text-status-running"
                : "bg-destructive/10 text-destructive",
            )}
          >
            {t(`toolApproval.status.${approval.status}`)}
          </TranscriptPill>
        )}
      </div>

      {isPending && inputJson && (
        <TranscriptCardBody>
          <CollapsibleCode
            value={inputJson}
            surface="muted"
            bodyClassName="rounded-sm px-2.5 py-2"
          />
        </TranscriptCardBody>
      )}

      {isPending ? (
        <TranscriptCardBody className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={submitting} onClick={() => answer(true)}>
            <Check className="mr-1 h-3.5 w-3.5" />
            {t("toolApproval.approve")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={submitting}
            onClick={() => answer(false)}
          >
            <X className="mr-1 h-3.5 w-3.5" />
            {t("toolApproval.deny")}
          </Button>
          {error && <span className="text-aux text-destructive">{error}</span>}
        </TranscriptCardBody>
      ) : approval.result ? (
        <TranscriptCardBody className="text-aux text-muted-foreground">
          {approval.result}
        </TranscriptCardBody>
      ) : null}
    </TranscriptCard>
  );
};
