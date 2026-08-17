// frontend/src/components/agentre/session-index/own-sessions-header.tsx
//
// 父项目「自己的会话」子分组的组头。只在父项目同时有自己的会话和子项目时出现
// （见 index-group-row 的 nestOwnSessions）：共用父级那一个折叠箭头会把会话和子项目
// 绑死，会话多的父项目会把子项目整个挤出视野。
//
// 视觉上与子项目组头同级，但用 messages 图标而非项目头像 —— 明确「这是一段会话，
// 不是一个子项目」。
import { ChevronDown, MessagesSquare } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

export function OwnSessionsHeader({
  name,
  count,
  expanded,
  onToggle,
}: {
  name: string;
  count: number;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      className="flex w-full cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-xs outline-none hover:bg-sidebar-active-bg focus-visible:ring-[3px] focus-visible:ring-ring/50"
      onClick={onToggle}
      aria-expanded={expanded}
      aria-label={t("projects.session.groupToggle", { name })}
    >
      <ChevronDown
        className={cn(
          "size-3 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
          !expanded && "-rotate-90",
        )}
        aria-hidden="true"
      />
      <MessagesSquare
        className="size-3.5 text-muted-foreground"
        aria-hidden="true"
      />
      <span className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("projects.session.group")}
      </span>
      <span className="font-mono text-2xs text-subtle-foreground">{count}</span>
    </button>
  );
}
