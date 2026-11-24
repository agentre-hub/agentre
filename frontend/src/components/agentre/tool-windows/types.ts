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
  /** 工具窗的内容；标题栏由外壳画，内容超出可视高度时在外壳内滚动。 */
  content: React.ReactNode;
};
