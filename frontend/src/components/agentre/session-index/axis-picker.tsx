// axis-picker.tsx —— 统一会话索引的分组维度选择器（项目 / Agent / 时间）。
//
// 规格 docs/specs/2026-08-16-unified-chat-index.md 决策 3：这里**只放
// 「图标 + 当前值 + chevron」，不带「分组」二字**。320px 侧栏减 px-4 只剩 288px，
// 带标签时「分组 Agent」这一档会把同一行的「未读 N」chip 挤到第二行（mockup 实测）。
// 可发现性交给 `title` —— 它不占行内像素。
import { Bot, Briefcase, ChevronDown, Clock } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { IndexAxis } from "@/lib/session-axis";

export type AxisPickerProps = {
  value: IndexAxis;
  onChange: (axis: IndexAxis) => void;
};

const AXES: readonly IndexAxis[] = ["project", "agent", "time"];

const AXIS_ICONS: Record<IndexAxis, LucideIcon> = {
  project: Briefcase,
  agent: Bot,
  time: Clock,
};

// 逐档写死整条 key 而不是按 axis 拼出来：i18n 守卫（src/__tests__/i18n.test.ts）
// 只认源码里的静态字面量，拼出来的 key 漏翻译时没人会红。
function axisLabel(t: TFunction, axis: IndexAxis): string {
  switch (axis) {
    case "project":
      return t("sessionIndex.axis.project");
    case "agent":
      return t("sessionIndex.axis.agent");
    case "time":
      return t("sessionIndex.axis.time");
  }
}

export function AxisPicker({ value, onChange }: AxisPickerProps) {
  const { t } = useTranslation();
  const ActiveIcon = AXIS_ICONS[value];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid="axis-picker"
          data-axis={value}
          title={t("sessionIndex.axis.title")}
          className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-md px-1.5 text-2xs font-medium text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 motion-reduce:transition-none"
        >
          <ActiveIcon className="size-3.5" aria-hidden="true" />
          <span className="truncate">{axisLabel(t, value)}</span>
          <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-[160px]">
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) => onChange(next as IndexAxis)}
        >
          {AXES.map((axis) => {
            const Icon = AXIS_ICONS[axis];
            return (
              <DropdownMenuRadioItem
                key={axis}
                value={axis}
                data-testid={`axis-option-${axis}`}
                className="text-xs"
              >
                <Icon className="size-3.5" aria-hidden="true" />
                {axisLabel(t, axis)}
              </DropdownMenuRadioItem>
            );
          })}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
