import { Globe } from "lucide-react";

import {
  AgentAvatar,
  DepartmentMark,
  ProjectGlyph,
  cn,
} from "@agentre-hub/agentre-ui";

import type { MemoryDirectoryEntry } from "./directory";

/**
 * 一个归属的身份记号：全局一枚地球、部门 / agent / 项目用各自既有的记号。
 * 记号旁边总有名字，所以一律 aria-hidden（design.md §10「装饰图标」）。
 */
export function OwnerMark({
  entry,
  size = "sm",
}: {
  entry: MemoryDirectoryEntry;
  size?: "xs" | "sm";
}) {
  const box = size === "xs" ? "size-4" : "size-6";
  const mark = entry.mark;
  let node: React.ReactNode;
  switch (mark.kind) {
    case "global":
      node = (
        <span
          className={cn(
            "flex items-center justify-center rounded-md bg-primary-soft text-primary-text",
            box,
          )}
        >
          <Globe className={size === "xs" ? "size-3" : "size-3.5"} />
        </span>
      );
      break;
    case "department":
      node = (
        <DepartmentMark
          name={entry.label}
          icon={mark.icon}
          accentColor={mark.accentColor}
          size={size}
        />
      );
      break;
    case "agent":
      node = (
        <AgentAvatar
          name={entry.label}
          color={mark.color}
          iconKey={mark.avatarIcon}
          avatarDataUrl={mark.avatarDataUrl}
          size={size}
        />
      );
      break;
    case "project":
      node = (
        <ProjectGlyph
          project={{ name: entry.label, icon: mark.icon, color: mark.color }}
          className={box}
        />
      );
      break;
  }
  return (
    <span aria-hidden="true" className="flex shrink-0">
      {node}
    </span>
  );
}
