import { Bot, ChevronDown, Clock, Folder, Monitor } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";

import { useUiTranslation } from "../i18n";

import type { IndexAxis } from "./axis-groups";

/**
 * 会话索引的分组维度选择器。
 *
 * 决策 3：这里**只放「图标 + 当前值 + chevron」，不带「分组」二字**。320px 侧栏减
 * px-4 只剩 288px，带标签时「分组 Agent」这一档会把同一行的「未读 N」chip 挤到第二
 * 行（mockup 实测）。可发现性交给 `title` —— 它不占行内像素。
 *
 * **可选轴清单由宿主传入**（决策 17）：桌面端只 offer 项目 / Agent / 时间，server
 * 控制台四档全给。包里写死一份清单的话，多出来的那一档在桌面端选得着却取不到数。
 *
 * 用 radix 的 DropdownMenu 而不是自己拿 Popover 摞几个按钮：菜单的键盘语义
 * （方向键漫游、首字母跳转、Esc 关闭、`menuitemradio` 的选中态）是它自带的，
 * 手搓一遍只会搓出一个看起来像菜单、按方向键没反应的东西。
 */

// 项目档用的是**行首那一枚文件夹字形**，不是另一个「项目感」的图标：选择器说的
// 「项目」和行里画的「项目」必须是同一个记号。
const AXIS_ICONS: Record<IndexAxis, LucideIcon> = {
  project: Folder,
  agent: Bot,
  time: Clock,
  machine: Monitor,
};

export type AxisPickerProps = {
  value: IndexAxis;
  /** 宿主 offer 哪几档，按摆出来的顺序。 */
  axes: readonly IndexAxis[];
  onChange: (axis: IndexAxis) => void;
};

export function AxisPicker({ value, axes, onChange }: AxisPickerProps) {
  const { t } = useUiTranslation();
  const ActiveIcon = AXIS_ICONS[value];
  const title = t("sessionIndex.axis.title");

  return (
    <DropdownMenuPrimitive.Root>
      <DropdownMenuPrimitive.Trigger asChild>
        <button
          type="button"
          data-testid="axis-picker"
          data-axis={value}
          title={title}
          className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-md px-1.5 text-2xs font-medium text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 motion-reduce:transition-none"
        >
          <ActiveIcon className="size-3.5" aria-hidden="true" />
          <span className="truncate">{axisLabel(t, value)}</span>
          <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuPrimitive.Trigger>
      <DropdownMenuPrimitive.Portal>
        <DropdownMenuPrimitive.Content
          align="start"
          sideOffset={6}
          className="z-50 min-w-[10rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
        >
          {/* 决策 3 把「分组」二字赶出那 288px 的行，不是赶出整个控件：菜单里有的是
              地方，标题行让第一次打开的人知道这几档是什么维度。 */}
          <DropdownMenuPrimitive.Label
            data-testid="axis-picker-label"
            className="px-2 py-1 text-2xs text-muted-foreground"
          >
            {title}
          </DropdownMenuPrimitive.Label>
          <DropdownMenuPrimitive.RadioGroup
            value={value}
            onValueChange={(next) => onChange(next as IndexAxis)}
          >
            {axes.map((axis) => {
              const Icon = AXIS_ICONS[axis];
              return (
                <DropdownMenuPrimitive.RadioItem
                  key={axis}
                  value={axis}
                  data-testid={`axis-option-${axis}`}
                  className="relative flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-xs outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground data-[state=checked]:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0"
                >
                  <Icon className="size-3.5" aria-hidden="true" />
                  {axisLabel(t, axis)}
                </DropdownMenuPrimitive.RadioItem>
              );
            })}
          </DropdownMenuPrimitive.RadioGroup>
        </DropdownMenuPrimitive.Content>
      </DropdownMenuPrimitive.Portal>
    </DropdownMenuPrimitive.Root>
  );
}

// 逐档写死整条 key 而不是按 axis 拼出来：i18n 守卫（src/i18n/i18n.test.tsx）只认
// 源码里的静态字面量，拼出来的 key 漏翻译时没人会红。
function axisLabel(
  t: ReturnType<typeof useUiTranslation>["t"],
  axis: IndexAxis,
): string {
  switch (axis) {
    case "project":
      return t("sessionIndex.axis.project");
    case "agent":
      return t("sessionIndex.axis.agent");
    case "time":
      return t("sessionIndex.axis.time");
    case "machine":
      return t("sessionIndex.axis.machine");
  }
}
