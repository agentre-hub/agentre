import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { SearchInput } from "../ui/search-input";
import { Spinner } from "../ui/spinner";

export interface BoardSearchBoxProps {
  value: string;
  onChange: (value: string) => void;
  /** 防抖在途或宿主取数在途：右端一枚转圈，**旧结果留在原地**。 */
  busy?: boolean;
  /** 命中数；不给就不显示（没筛选时显示一个总数没有意义）。 */
  matchedCount?: number;
  className?: string;
}

/**
 * 搜索框。转圈画在输入框**右端内侧**：把它挪到看板上就会遮住结果，而这里的约定
 * 恰恰是查询期间旧结果一动不动。
 */
export function BoardSearchBox({
  value,
  onChange,
  busy,
  matchedCount,
  className,
}: BoardSearchBoxProps) {
  const { t } = useUiTranslation();

  return (
    <div
      data-testid="board-search"
      className={cn("flex shrink-0 items-center gap-1.5", className)}
    >
      <div className="relative">
        <SearchInput
          value={value}
          onChange={onChange}
          size="sm"
          placeholder={t("board.search.placeholder")}
          aria-label={t("board.search.placeholder")}
          className="w-56"
          inputClassName={busy ? "pr-5" : undefined}
        />
        {busy ? (
          <Spinner
            data-testid="board-search-spinner"
            className="pointer-events-none absolute right-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
          />
        ) : null}
      </div>
      {typeof matchedCount === "number" ? (
        <span
          data-testid="board-search-count"
          className="shrink-0 font-mono text-2xs text-muted-foreground"
        >
          {t("board.search.count", { count: matchedCount })}
        </span>
      ) : null}
    </div>
  );
}
