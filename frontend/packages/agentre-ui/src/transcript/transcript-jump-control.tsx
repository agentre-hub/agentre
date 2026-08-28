import * as React from "react";
import { ArrowDown } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

/** 补齐（catch-up）带来的东西：新增了多少行、其中还有几项没回答的决策。 */
export type TranscriptCatchUpSummary = {
  /**
   * 补齐新增的行数。**0 也是一种真情况**：补齐把上千条 delta 全追加进了还在流的
   * 那一行，行数没变但内容确实多了 —— 此时报不出条数，就只说有新内容，
   * 与其编一个数字不如少说一句。
   */
  newRows: number;
  /** 补齐里还没被回答的待决策数。 */
  pendingDecisions: number;
};

export type TranscriptJumpControlProps = {
  onJump: () => void;
  /** 没有补齐时不给（或给 null）：药丸退回「回到底部」那一句。 */
  catchUp?: TranscriptCatchUpSummary | null;
  /**
   * 视口下沿之后还开了几轮。宿主算好后传进来 —— 组件是纯呈现件，不碰消息数组，
   * 也不碰滚动几何（两端宿主的转录数据源与滚动实现并不相同）。
   *
   * 不传（或 0）时呈现与它出现之前逐字一致：`agentre-server` 那端不传，
   * 药丸继续只写「回到底部」。
   */
  turnsBelow?: number;
  className?: string;
};

/**
 * 转录区底部浮出的那枚控件 —— **只有药丸一种形状**。
 *
 * 它此前是两副样子：没有补齐时是一枚圆形图标钮（只有一个 ↓，靠 `aria-label` 说话），
 * 有补齐时才长成带文字的药丸。两副样子说的是同一件事「下面还有，点我回去」，而圆钮
 * 那一副把话全藏进了 tooltip：看得见的只有一个箭头。2026-08-24 起统一成药丸，
 * 没有补齐时写「回到底部」，有补齐时写清楚多了多少、其中几项待处理。
 *
 * 待处理那一段必须是**文字**：状态色点只是修饰，颜色不能是信息的唯一载体
 * （`docs/design.md` 无障碍）。
 *
 * 不另挂 `aria-label`：按钮的可访问名就是这些文字本身，条数、待处理项数与轮数因此
 * 一并被读出来；挂了反而会把它们盖掉。
 *
 * 说什么分三档，互斥，按此优先级：
 *   1. 有断连补齐账 —— 报补齐新增量（以及其中还剩几项待决策）；
 *   2. 无补齐账、下方还有至少一整轮 —— 说出轮数；
 *   3. 其余 —— 「回到底部」。
 * 1 压过 2 是因为两个数回答的是不同问题：「你离开时流进来多少」与「你此刻落后多少」。
 * 断连刚回来时前者才是用户要的，而一枚浮层药丸只有一行字的宽度，塞两个数会让它们
 * 互相解释不清。
 *
 * 横向位置由**外层**承担：药丸在它所在的那条列里居中。它此前挂的是 `ml-auto`，
 * 会被甩到滚动容器右缘 —— 桌面端的转录列靠左并封顶在可读宽度，右边是大片空白，
 * 药丸就落在那片空白里，离它所描述的内容几百 px。
 *
 * **列几何本身是宿主布局，包里不写死**：桌面端的转录列是 `ml-10 + max-w-measure`
 * （靠左），`agentre-server` 的是 `mx-auto + max-w-measure`（本就居中）。写死其中
 * 一端的常量，另一端必然偏。宿主用 `className` 把自己那条列交进来；不给的那端就在
 * 滚动容器里居中，对居中列的宿主而言那正是对的答案。
 */
export function TranscriptJumpControl({
  catchUp,
  className,
  onJump,
  turnsBelow = 0,
}: TranscriptJumpControlProps): React.ReactElement {
  const { t } = useUiTranslation();

  return (
    <div
      className={cn(
        "pointer-events-none sticky bottom-4 z-20 flex justify-center",
        className,
      )}
    >
      <Button
        type="button"
        data-testid="transcript-jump-control"
        variant="outline"
        size="sm"
        title={t("transcriptJump.title")}
        onClick={onJump}
        className={cn(
          "pointer-events-auto flex w-fit max-w-full rounded-full bg-background shadow-md hover:shadow-lg dark:bg-background",
          "animate-in fade-in slide-in-from-bottom-1 duration-200 ease-out motion-reduce:animate-none",
        )}
      >
        <ArrowDown aria-hidden="true" />
        <span className="truncate">
          {catchUp
            ? catchUp.newRows > 0
              ? t("transcriptJump.newCount", { count: catchUp.newRows })
              : t("transcriptJump.newSome")
            : turnsBelow > 0
              ? t("transcriptJump.turnsBelow", { count: turnsBelow })
              : t("transcriptJump.backToBottom")}
        </span>
        {catchUp && catchUp.pendingDecisions > 0 ? (
          <span
            data-testid="transcript-jump-pending"
            className="flex items-center gap-1 text-status-waiting"
          >
            <span
              aria-hidden="true"
              className="size-1.5 rounded-full bg-status-waiting"
            />
            {t("transcriptJump.pendingCount", {
              count: catchUp.pendingDecisions,
            })}
          </span>
        ) : null}
      </Button>
    </div>
  );
}
