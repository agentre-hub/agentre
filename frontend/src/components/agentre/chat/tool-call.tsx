// 桌面端的通用工具卡片:一个工具名 + 一行状态(未知/null 工具走这张)。
//
// 与共享包 canonical-tool/ 的关系(那边的卡片按 kind 分派):
//   - canonical-tool/raw/card.tsx 处理非规范工具
//   - canonical-tool/<kind>/card.tsx 处理规范 kind
// 这张是本仓自己的兜底形状。
//
// 它由 components/agentre/index.ts 再导出给宿主与测试。

import * as React from "react";
import { Check, LoaderCircle, Wrench } from "lucide-react";

import { cn } from "@/lib/utils";

import { TranscriptCard } from "@agentre-hub/agentre-ui";
import type { AgentStatus } from "../types";
import { statusConfig } from "../types";

type ToolCallProps = React.ComponentProps<"div"> & {
  path?: string;
  status?: AgentStatus;
  statusLabel: string;
  toolName: string;
};

export function ToolCall({
  className,
  path,
  status = "running",
  statusLabel,
  toolName,
  ...props
}: ToolCallProps) {
  const config = statusConfig[status];
  const StatusIcon = status === "waiting" ? LoaderCircle : Check;

  return (
    <TranscriptCard
      data-selectable-text="true"
      className={cn("flex flex-col gap-1.5 px-3 py-2.5", className)}
      {...props}
    >
      <div className="flex min-w-0 items-center gap-1.5 font-mono text-aux">
        <Wrench className="size-3.5 shrink-0 text-primary-text" />
        <span className="font-semibold text-primary-text">{toolName}</span>
        {path ? (
          <>
            <span className="text-muted-foreground">·</span>
            <span className="min-w-0 truncate text-muted-foreground">
              {path}
            </span>
          </>
        ) : null}
      </div>
      <div className="flex items-center gap-1.5 font-mono text-meta">
        <StatusIcon className={cn("size-3", config.textClassName)} />
        <span className={status === "running" ? config.textClassName : ""}>
          {statusLabel}
        </span>
      </div>
    </TranscriptCard>
  );
}

// Generic tool card extension point: canonical-tool/raw/card.tsx handles
// non-canonical tools; canonical-tool/<kind>/card.tsx handles canonical kinds.
