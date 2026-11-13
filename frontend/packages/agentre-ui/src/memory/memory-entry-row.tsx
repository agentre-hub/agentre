import { Brain, ChevronRight } from "lucide-react";

import { useUiTranslation } from "../i18n";

export type MemoryEntryRowProps = {
  /** 这个归属名下的正常条目数。 */
  count: number;
  onOpen(): void;
  "data-testid"?: string;
};

/**
 * 「记忆 N 条」入口行：agent 详情、部门详情、项目设置里各一行，点一下由宿主带着归属
 * 跳到记忆页。数从哪来、跳到哪是宿主的事，这里只画这一行。
 */
export function MemoryEntryRow({
  count,
  onOpen,
  "data-testid": testId,
}: MemoryEntryRowProps) {
  const { t } = useUiTranslation();
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onOpen}
      className="flex w-full items-center gap-2 rounded-md bg-button-fill px-3 py-2 text-left text-sm text-foreground hover:bg-accent on-accent focus-visible:focus-outline"
    >
      <Brain className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        {t("memory.entryRow", { count })}
      </span>
      <ChevronRight
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden
      />
    </button>
  );
}
