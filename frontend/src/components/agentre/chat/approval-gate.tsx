// 审批闸门:一段说明 + 批准 / 拒绝两个出口。
//
// 由 components/agentre/index.ts 再导出(foundation 用例直接渲染它)。

import * as React from "react";
import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { TranscriptCard } from "@agentre-hub/agentre-ui";

type ApprovalGateProps = React.ComponentProps<"section"> & {
  description: string;
  onApprove?: () => void;
  onReject?: () => void;
  title: string;
};

export function ApprovalGate({
  className,
  description,
  onApprove,
  onReject,
  title,
  ...props
}: ApprovalGateProps) {
  const { t } = useTranslation();
  return (
    <TranscriptCard
      className={cn(
        "flex items-center gap-3 border-status-waiting bg-status-waiting-bg px-4 py-3",
        className,
      )}
      {...props}
    >
      <TriangleAlert
        className="size-5 shrink-0 text-status-waiting"
        aria-hidden="true"
      />
      <div className="min-w-0 flex-1">
        <div className="text-aux font-semibold text-status-waiting">
          {title}
        </div>
        <div className="mt-0.5 text-aux leading-snug">{description}</div>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-8"
        onClick={onReject}
      >
        {t("common.reject")}
      </Button>
      <Button
        type="button"
        size="sm"
        className="h-8 bg-status-running text-status-running-foreground hover:bg-status-running/90"
        onClick={onApprove}
      >
        {t("chat.actions.approve")}
      </Button>
    </TranscriptCard>
  );
}

/**
 * 桌面端 composer 的**装配面**。渲染住在 `@agentre-hub/agentre-ui` 的 ChatComposer
 * 里，这里只负责把这一端独有的能力接上去：@ 提及的数据源、该 agent 生效的技能
 * 命令、以及 Wails 的原生拖入通道。
 *
 * 这三样都是宿主耦合，包不得知道：`useChatAgents` / `useProjectList` 读的是本机
 * 的清单，`ChatReadDroppedImages` 是 Wails 绑定。与 `agent-backends.tsx` /
 * `llm-providers.tsx` 是同一种装配根，不是第二份实现。
 */
