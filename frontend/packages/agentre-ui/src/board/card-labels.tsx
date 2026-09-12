import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { toneClass, toneClassNames } from "./tones";
import type { BoardLabelView } from "./types";

/** 卡片上最多画两枚标签，其余进 `+N` —— 再多一枚标题就被挤成一行。 */
export const CARD_LABEL_LIMIT = 2;

const CHIP_CLASS_NAME =
  "inline-flex max-w-[8.5rem] items-center truncate rounded-sm px-1.5 py-px text-2xs font-medium leading-4";

export interface BoardCardLabelsProps {
  labels?: BoardLabelView[];
  className?: string;
}

export function BoardCardLabels({ labels, className }: BoardCardLabelsProps) {
  const { t } = useUiTranslation();

  if (!labels || labels.length === 0) return null;

  const shown = labels.slice(0, CARD_LABEL_LIMIT);
  const overflow = labels.length - shown.length;

  return (
    <span className={cn("flex flex-wrap items-center gap-1", className)}>
      {shown.map((label) => (
        <span
          key={label.id}
          className={cn(CHIP_CLASS_NAME, toneClass(label.tone))}
        >
          {label.name}
        </span>
      ))}
      {overflow > 0 ? (
        // 溢出计数与中性档同一画法：描边。填充版在暗色弹层里会整个消失。
        <span
          className={cn(CHIP_CLASS_NAME, toneClassNames.gray)}
          title={t("board.moreLabels", { count: overflow })}
        >
          {`+${overflow}`}
        </span>
      ) : null}
    </span>
  );
}
