// 顶部三个模式小芯片(图标 / 图片 / 首字母),以及每个模式对应的图标。

import * as React from "react";
import { Image as ImageIcon } from "lucide-react";

import { cn } from "@/lib/utils";

import { hasIcon, iconForKey } from "../icon-registry";

export function ModeChip({
  active,
  disabled,
  icon: Icon,
  label,
  hint,
  onClick,
}: {
  active: boolean;
  disabled?: boolean;
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  label: string;
  hint: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex flex-1 flex-col items-start gap-0 rounded-md border px-2 py-1.5 text-left transition-colors",
        active
          ? "border-primary bg-primary-soft text-primary-text"
          : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground",
        disabled && "cursor-not-allowed opacity-50 hover:bg-transparent",
      )}
    >
      <span className="flex items-center gap-1 text-xs font-semibold">
        <Icon className="size-3.5" aria-hidden={true} />
        {label}
      </span>
      <span
        className={cn(
          "font-mono text-2xs",
          active ? "text-primary-text/70" : "text-muted-foreground",
        )}
      >
        {hint}
      </span>
    </button>
  );
}

export function getModeIconForChip(iconKey: string) {
  if (hasIcon(iconKey)) {
    return iconForKey(iconKey);
  }
  return ImageIcon; // 占位（未设置时显示个图）
}

// ----------------------------------------------------------------------------
// 内部：图片模式面板
// ----------------------------------------------------------------------------
