import { cn } from "../lib/utils";
import { Skeleton } from "../ui/skeleton";

/**
 * 会话列表首屏的骨架（规格 2026-09-16 决策 14）。
 *
 * 它取代的是「加载中…」那种一行文字：同一件事各处说一遍，而且**不占位置**，行落地
 * 时整列跳一下。这里复用包内 `Skeleton` 原语，不另起一套灰块样式。
 *
 * 对读屏隐藏：正在取这件事由容器的 `aria-busy` 说一次，几条灰条不必再念一遍。
 */
const DEFAULT_ROWS = [0.95, 0.75, 0.55, 0.35];

type SessionRowSkeletonProps = {
  rows?: number;
  className?: string;
};

function SessionRowSkeleton({
  rows = DEFAULT_ROWS.length,
  className,
}: SessionRowSkeletonProps) {
  return (
    <div
      data-slot="session-row-skeleton"
      aria-hidden="true"
      className={cn("flex flex-col gap-0.5", className)}
    >
      {DEFAULT_ROWS.slice(0, rows).map((opacity, index) => (
        <div
          key={index}
          style={{ opacity }}
          className="flex items-center gap-2 px-2 py-2"
        >
          <Skeleton className="size-2 shrink-0 rounded-full" />
          <span className="min-w-0 flex-1">
            <Skeleton
              className="h-3 rounded"
              style={{ width: `${72 - index * 9}%` }}
            />
            <Skeleton className="mt-1.5 h-2.5 w-2/5 rounded" />
          </span>
        </div>
      ))}
    </div>
  );
}

export { SessionRowSkeleton };
export type { SessionRowSkeletonProps };
