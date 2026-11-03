import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import type { PresetAvatar } from "../presets/preset-registry";

// 预设档与搜索结果共用同一张单选网格，原生按钮支持 Tab 聚焦和 Enter / Space 选择。
export function PresetGrid({
  items,
  value,
  onPick,
  label,
}: {
  items: readonly PresetAvatar[];
  value: string;
  onPick(key: string): void;
  label: string;
}) {
  const { t } = useUiTranslation();
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="grid grid-cols-6 gap-1.5"
    >
      {items.map((preset) => (
        <button
          key={preset.key}
          type="button"
          role="radio"
          aria-checked={value === preset.key}
          aria-label={t(preset.labelKey)}
          title={t(preset.labelKey)}
          onClick={() => onPick(preset.key)}
          className={cn(
            "rounded-lg outline-none transition-transform hover:-translate-y-0.5 focus-visible:focus-outline",
            value === preset.key &&
              "outline outline-2 outline-offset-2 outline-foreground",
          )}
        >
          <img
            src={preset.src}
            alt=""
            draggable={false}
            className="aspect-square w-full rounded-lg"
          />
        </button>
      ))}
    </div>
  );
}
