/**
 * 项目的旧字形选择入口：保留现有触发器契约，网格与调色板由统一记号弹层复用。
 * 宿主迁入 IdentityMarkPicker 后不需要各自维护图标搜索与颜色选中态。
 */
import * as React from "react";
import { Pencil } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { ColorPalette } from "../identity/color-palette";
import { IconGrid } from "../identity/icon-grid";
import { cn } from "../lib/utils";
import { ProjectGlyph } from "../session-index/project-glyph";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

export interface ProjectGlyphPickerProps {
  /** 字形上退回首字时用它，也进弹层触发器的 aria-label。 */
  name: string;
  /** 当前持久化 icon key。 */
  icon?: string;
  /** 颜色 token，如 "agent-3"。 */
  color?: string;
  onPickIcon(iconKey: string): void;
  onPickColor(color: string): void;
  className?: string;
}

export function ProjectGlyphPicker({
  name,
  icon,
  color,
  onPickIcon,
  onPickColor,
  className,
}: ProjectGlyphPickerProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-testid="project-glyph-trigger"
          aria-label={t("projectSettings.identity.glyphAria", { name })}
          className={cn(
            "group/glyph relative shrink-0 rounded-xl outline-none focus-visible:focus-outline",
            className,
          )}
        >
          {/* 预览就是那枚真字形，key 交给它自己解。 */}
          <ProjectGlyph
            testId="project-glyph-preview"
            project={{ name, color, icon }}
            className="size-13 rounded-xl text-base"
          />
          <span
            aria-hidden="true"
            className="absolute -bottom-1 -right-1 inline-flex size-5 items-center justify-center rounded-full bg-field-fill text-muted-foreground shadow-sm transition-colors group-hover/glyph:text-foreground"
          >
            <Pencil className="size-2.5" />
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[320px] p-0">
        <div className="p-3">
          <p className="text-2xs font-semibold tracking-wide text-muted-foreground">
            {t("projectSettings.field.color")}
          </p>
          <ColorPalette
            color={color}
            onPickColor={onPickColor}
            testIdPrefix="project-glyph"
          />
        </div>
        <IconGrid
          value={icon ?? ""}
          onPick={(key) => {
            onPickIcon(key);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}
