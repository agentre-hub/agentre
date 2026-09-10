import { Skeleton } from "../ui/skeleton";
import { BOARD_STAGES } from "./types";

/**
 * 加载态是**四列骨架卡片**，不是屏幕中央一个转圈：数据到位就地填充，板不跳。
 */
export function BoardSkeleton() {
  return (
    <>
      {BOARD_STAGES.map((stage, columnIndex) => (
        <section
          key={stage}
          data-testid={`board-skeleton-column-${stage}`}
          aria-hidden="true"
          className="flex w-[300px] shrink-0 flex-col gap-2"
        >
          <Skeleton className="h-6 w-24 rounded-md" />
          {Array.from({ length: 3 - (columnIndex % 2) }, (_, cardIndex) => (
            // 这一档要的是卡面加描边，不是一条灰杠：占位的形状照落地后的样子来。
            <Skeleton
              key={cardIndex}
              className="h-20 rounded-md border border-border bg-card"
            />
          ))}
        </section>
      ))}
    </>
  );
}
