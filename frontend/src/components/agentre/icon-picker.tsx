// 部门详情仍使用的图标选择器，Agent 身份区已改用共享组件。

import * as React from "react";
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";

import { cn } from "@agentre-hub/agentre-ui";

import { iconForKey, iconMeta } from "./icon-registry";
import { agentColorClassNames, type AgentColor } from "./types";

import { IconGridPanel } from "./icon-picker/icon-grid-panel";

type IconPickerProps = {
  value: string;
  onChange: (key: string) => void;
  accentColor: AgentColor;
  ariaLabel?: string;
  className?: string;
};

export function IconPicker({
  value,
  onChange,
  accentColor,
  ariaLabel,
  className,
}: IconPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const Icon = iconForKey(value);
  const meta = iconMeta(value);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={ariaLabel ?? t("org.department.icon")}
          className={cn(
            "flex w-full items-center gap-2.5 rounded-md bg-field-fill px-2 py-1.5 text-left text-sm transition-colors hover:bg-field-fill-hover",
            className,
          )}
        >
          <span
            className={cn(
              "inline-flex size-6 shrink-0 items-center justify-center rounded text-agent-foreground",
              agentColorClassNames[accentColor],
            )}
            aria-hidden="true"
          >
            {React.createElement(Icon, {
              className: "size-3.5",
              "aria-hidden": true,
            })}
          </span>
          <span className="flex-1 truncate font-mono text-2xs">
            {meta?.label ?? value ?? t("iconPicker.selectIcon")}
          </span>
          <ChevronDown
            className="size-3 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[360px] p-0" align="start">
        <IconGridPanel
          value={value}
          onSelect={(key) => {
            onChange(key);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}
