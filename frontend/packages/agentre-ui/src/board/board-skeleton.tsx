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
          <div className="h-6 w-24 animate-pulse rounded-md bg-secondary" />
          {Array.from({ length: 3 - (columnIndex % 2) }, (_, cardIndex) => (
            <div
              key={cardIndex}
              className="h-20 animate-pulse rounded-md border border-border bg-card"
            />
          ))}
        </section>
      ))}
    </>
  );
}
