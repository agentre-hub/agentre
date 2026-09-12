import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { toneClass } from "./tones";
import { ISSUE_TONES, type IssueTone } from "./types";

export interface LabelPaletteProps {
  value: IssueTone;
  onChange: (tone: IssueTone) => void;
  /** 同一页上会出现两块色板（改一行、建一条），测试锚点各归各的。 */
  testIdPrefix?: string;
  className?: string;
}

/**
 * 8 档色板。**不开放自由取色** —— 任意十六进制会立刻绕过 tokens.css，暗色下自负
 * 盈亏；这 8 档是设计系统里已经过对比度的那一组（与 `issue_entity` 的 allowedTones
 * 同一份取值域）。
 */
export function LabelPalette({
  value,
  onChange,
  testIdPrefix = "label",
  className,
}: LabelPaletteProps) {
  const { t } = useUiTranslation();

  return (
    <div
      data-testid={`${testIdPrefix}-palette`}
      role="radiogroup"
      aria-label={t("board.labels.tone")}
      className={cn("flex flex-wrap items-center gap-1", className)}
    >
      {ISSUE_TONES.map((tone) => (
        <button
          key={tone}
          type="button"
          role="radio"
          aria-checked={value === tone}
          aria-label={tone}
          data-testid={`${testIdPrefix}-tone-${tone}`}
          onClick={() => onChange(tone)}
          className={cn(
            "size-5 cursor-pointer rounded-full transition-transform",
            "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
            toneClass(tone),
            value === tone &&
              "ring-2 ring-primary ring-offset-1 ring-offset-popover",
          )}
        />
      ))}
    </div>
  );
}
