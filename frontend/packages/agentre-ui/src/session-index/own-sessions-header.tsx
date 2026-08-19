import { ChevronDown, MessagesSquare } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

/**
 * 父项目「自己的会话」子分组的组头。只在父项目同时有自己的会话和子项目时出现：
 * 共用父级那一个折叠箭头会把会话和子项目绑死，会话多的父项目会把子项目整个挤出视野。
 *
 * 视觉上与子项目组头同级，但用 messages 图标而非项目字形 —— 明确「这是一段会话，
 * 不是一个子项目」。
 */
export type OwnSessionsHeaderProps = {
  /** 父项目名。只进无障碍名（「展开/收起 X 的会话」），行内不重复写一遍。 */
  name: string;
  count: number;
  expanded: boolean;
  onToggle: () => void;
};

export function OwnSessionsHeader({
  name,
  count,
  expanded,
  onToggle,
}: OwnSessionsHeaderProps) {
  const { t } = useUiTranslation();

  return (
    <button
      type="button"
      className="flex w-full cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-xs outline-none hover:bg-sidebar-active-bg focus-visible:ring-[3px] focus-visible:ring-ring/50"
      onClick={onToggle}
      aria-expanded={expanded}
      aria-label={t("sessionIndex.ownSessions.toggle", { name })}
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
        {t("sessionIndex.ownSessions.label")}
      </span>
      <span className="font-mono text-2xs text-subtle-foreground">{count}</span>
    </button>
  );
}
