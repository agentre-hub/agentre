// row-secondary-line.tsx —— 「按时间」档会话行的第二行（决策 5）。
//
// 这一档没有组头，两维都得落在行里。两维各自带**和其它档同一个字形**：agent 头像
// 就是「按项目」档行首那一枚，项目色文件夹就是「按 Agent」档行首那一枚 —— 否则同一
// 条会话在三个档之间长出三种样子，切档时读者要重新找一遍锚点。
//
// 自由会话如实写「随手对话」并把字形置灰，而不是留半行空白：决策 7 说它是一个正当的
// 去处，空白读起来像「项目丢了」。
import { Folder } from "lucide-react";

import { cn } from "@/lib/utils";

import { AgentAvatar } from "../primitives";
import { agentTextColorClassName, type AgentColor } from "../types";

export type RowSecondaryLineProps = {
  agentName: string;
  /** 颜色 token，如 "agent-1"。 */
  agentColor: string;
  /** 项目名；自由会话传「随手对话」。 */
  projectName: string;
  /** 项目色 token；`null` = 自由会话（字形置灰）。 */
  projectColor: string | null;
};

export function RowSecondaryLine({
  agentName,
  agentColor,
  projectName,
  projectColor,
}: RowSecondaryLineProps) {
  return (
    <span
      data-testid="row-secondary-line"
      className="flex min-w-0 items-center gap-1"
    >
      {agentName ? (
        <>
          <span
            data-kind="agent-avatar"
            className="inline-flex size-3.5 shrink-0 items-center justify-center"
          >
            <AgentAvatar
              name={agentName}
              initials={agentName.trim().slice(0, 1).toUpperCase() || undefined}
              color={(agentColor as AgentColor) || "agent-1"}
              size="sm"
              className="size-full rounded-sm text-[8px]"
            />
          </span>
          <span className="truncate">{agentName}</span>
        </>
      ) : null}
      {agentName && projectName ? (
        <span aria-hidden="true" className="text-subtle-foreground">
          ·
        </span>
      ) : null}
      {projectName ? (
        <>
          <span
            data-kind={
              projectColor === null ? "project-folder-muted" : "project-folder"
            }
            className="inline-flex size-3 shrink-0 items-center justify-center"
          >
            <Folder
              className={cn(
                "size-3",
                projectColor === null
                  ? "text-subtle-foreground"
                  : agentTextColorClassName(projectColor),
              )}
              aria-hidden="true"
            />
          </span>
          <span className="truncate">{projectName}</span>
        </>
      ) : null}
    </span>
  );
}
