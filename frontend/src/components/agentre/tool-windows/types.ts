import type * as React from "react";
import type { LucideIcon } from "lucide-react";

import type { ToolWindowId } from "@/stores/tool-window-layout-store";

/**
 * 一个工具窗的注册项。对话页把它要挂的工具窗列成数组交给 `<ToolWindowLayout
 * windows>`：图标条只为注册了的工具窗出图标，槽位只渲染注册了的工具窗——布局
 * store 认识全部七个，还没实现的那几个就只是不出现。
 */
export type ToolWindowDefinition = {
  id: ToolWindowId;
  /** 工具窗名称（已翻译）：图标的可及名与悬停提示、区域名、标题栏都用它。 */
  label: string;
  icon: LucideIcon;
  /**
   * 带数量的工具窗（变更）的数字：图标条角标与标题栏都显示它。null / 缺省 =
   * 不带数量或数据未就绪；为 0 时图标条不显示角标。
   */
  count?: number | null;
  /**
   * 数量只显示在标题栏、不在图标条上画角标（关联：数的是条目总数，不是待处理项）。
   * 缺省 = true，即带数量的工具窗两处都显示。
   */
  countBadge?: boolean;
  /** 工具窗里有需要留意的情况（关联：同一份代码上有文件重叠）：图标条上的图标显示提醒色圆点。 */
  alert?: boolean;
  /** 工具窗里有进程在跑（终端）：图标条上的图标显示运行中圆点。缺省 = 无此概念。 */
  running?: boolean;
  /** 工具窗的内容；标题栏由外壳画，内容超出可视高度时在外壳内滚动。 */
  content: React.ReactNode;
};
