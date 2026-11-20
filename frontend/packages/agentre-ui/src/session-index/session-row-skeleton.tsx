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
  /**
   * `"phone"`：照 `SessionRow variant="phone"` 的几何画（64px 行、44px 圆角头像、
   * 右上时间）。不传是桌面那一档（状态点 + 两条细线）。
   */
  variant?: "phone";
  className?: string;
};

function SessionRowSkeleton({
  rows = DEFAULT_ROWS.length,
  variant,
  className,
}: SessionRowSkeletonProps) {
  const phone = variant === "phone";
  return (
    <div
      data-slot="session-row-skeleton"
      data-variant={variant}
      aria-hidden="true"
      className={cn("flex flex-col", phone ? "gap-0" : "gap-0.5", className)}
    >
      {DEFAULT_ROWS.slice(0, rows).map((opacity, index) =>
        phone ? (
          // 与真实手机行同一副几何（session-row.tsx 的 phone 档）：落地时列表不跳。
          <div
            key={index}
            style={{ opacity }}
            className="flex min-h-16 items-center gap-3 px-2 py-2.5"
          >
            <Skeleton className="size-11 shrink-0 rounded-xl" />
            <span className="flex min-w-0 flex-1 flex-col gap-2">
              <span className="flex items-center gap-2">
                <Skeleton
                  className="h-3.5 rounded"
                  style={{ width: `${68 - index * 9}%` }}
                />
                <Skeleton
                  data-slot="session-row-skeleton-time"
                  className="ml-auto h-2.5 w-8 shrink-0 rounded"
                />
              </span>
              <Skeleton className="h-3 w-3/5 rounded" />
            </span>
          </div>
        ) : (
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
        ),
      )}
    </div>
  );
}

export { SessionRowSkeleton };
export type { SessionRowSkeletonProps };
