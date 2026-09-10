// 左侧筛选区:分组标题 + 可勾选的条目。

import * as React from "react";
import { ChevronDown, SlidersHorizontal } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

// 筛选是**一个**入口，两维都收在里面（决策 12「筛选不常驻占位」明确否决了
// 「常驻两个下拉」：未筛选时它们既不说明用途也占掉一整行）。命中之后说话的是
// 下面那排可清除的 chip，不是顶栏上一直摆着的两个「后端：全部」。
export type FilterSection = {
  key: string;
  heading: string;
  icon: React.ReactNode;
  value: string;
  onValueChange: (value: string) => void;
  options: Array<{ id: number; label: string }>;
};

export function FilterEntry(props: {
  activeCount: number;
  sections: FilterSection[];
}) {
  const { t } = useTranslation();
  const active = props.activeCount > 0;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid="org-filter-entry"
          aria-label={t("org.index.filters.entryAria")}
          className={cn(
            "inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50",
            active
              ? "border-primary bg-primary-soft text-primary-text"
              : "border-border text-muted-foreground",
          )}
        >
          <SlidersHorizontal className="size-3.5" aria-hidden="true" />
          <span>{t("org.index.filters.entry")}</span>
          {active ? (
            <span className="font-mono text-2xs">{props.activeCount}</span>
          ) : null}
          <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 overflow-auto">
        {props.sections.map((section, index) => (
          <React.Fragment key={section.key}>
            {index > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuLabel className="flex items-center gap-1.5 text-muted-foreground">
              {section.icon}
              {section.heading}
            </DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={section.value}
              onValueChange={section.onValueChange}
            >
              <DropdownMenuRadioItem
                value="0"
                data-testid={`org-filter-${section.key}-option-0`}
              >
                {t("common.all")}
              </DropdownMenuRadioItem>
              {section.options.map((option) => (
                <DropdownMenuRadioItem
                  key={option.id}
                  value={String(option.id)}
                  data-testid={`org-filter-${section.key}-option-${option.id}`}
                >
                  {option.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </React.Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * 三个呈现件都在共享包里（规格 2026-08-18「server 端的组织管理面」要两端同一批
 * 组件）。留在这一层的只有**装配**：dnd-kit 的 `useDraggable` / `useDroppable`、
 * 键盘那条候选落点链，以及桌面端自己的头像 / 图标注册表 —— 这些在 agentre-server
 * 那侧要么不存在（Wails 头像），要么形态不同（浏览器不拖拽）。
 */
