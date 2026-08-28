// 最近使用横条：紧跟在顶部特殊项之后、供应商分组之前（mockup 的列表次序），
// 自带「最近使用」标签，单行横向可移除 chip，不占竖列表位。
import { X } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { cn } from "../../lib/utils";

import { LlmModelLogo, LlmProviderLogo } from "../ai-brand-logo";
import type { RecentChip } from "./options";
import type { ModelTarget } from "./types";

export function RecentChipsRow({
  chips,
  onPick,
  onRemove,
}: {
  chips: RecentChip[];
  onPick: (target: ModelTarget) => void;
  onRemove: (target: ModelTarget) => void;
}) {
  const { t } = useTranslation();
  return (
    <li role="none">
      {/* mockup .recents：7px 8px 5px / gap 5px / 单行横滑且不显示滚动条。 */}
      <div
        data-testid="recent-chips"
        className="flex flex-nowrap items-center gap-[5px] overflow-x-auto px-2 pb-[5px] pt-[7px] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        <span className="shrink-0 pr-px text-3xs uppercase tracking-[0.06em] text-muted-foreground">
          {t("modelTargetPicker.recentLabel")}
        </span>
        {chips.map((r) => (
          <span
            key={r.key}
            className={cn(
              "flex h-[22px] shrink-0 items-center gap-1 rounded-md border border-border bg-card pl-[5px] pr-1 text-2xs transition-colors",
              // mockup .rchip：整颗 chip 是手形 + hover 只加深描边；停用的不给可点暗示。
              r.disabled
                ? "cursor-not-allowed"
                : "cursor-pointer hover:border-border-strong",
            )}
          >
            <button
              type="button"
              disabled={r.disabled}
              title={r.title}
              className="flex max-w-[10rem] cursor-pointer items-center gap-1 font-mono text-foreground disabled:cursor-not-allowed disabled:opacity-50"
              onClick={() => {
                if (r.disabled) return;
                onPick(r.target);
              }}
            >
              {/* 品牌标识只做视觉，绝不能进按钮的无障碍名（logo 自带 role=img + 品牌名）。 */}
              <span aria-hidden="true" className="flex shrink-0">
                {r.kind === "fixed" ? (
                  <LlmModelLogo
                    model={r.label}
                    providerType={r.providerType}
                    providerName={r.providerName}
                    className="size-3.5"
                  />
                ) : (
                  <LlmProviderLogo
                    providerType={r.providerType}
                    providerName={r.providerName}
                    className="size-3.5"
                  />
                )}
              </span>
              <span className="truncate">{r.label}</span>
            </button>
            <button
              type="button"
              aria-label={t("modelTargetPicker.removeRecent", {
                label: r.label,
              })}
              className="shrink-0 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              onClick={() => onRemove(r.target)}
            >
              <X className="size-3" aria-hidden="true" />
            </button>
          </span>
        ))}
      </div>
    </li>
  );
}
