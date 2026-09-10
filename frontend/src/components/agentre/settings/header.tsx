// 设置页的标题行(H1 + 说明 + 右侧动作槽)。各分区共用它,所以它先于分区存在。

import * as React from "react";

export type SettingsPageHeaderProps = {
  actions?: React.ReactNode;
  description: string;
  title: string;
};

export function SettingsPageHeader({
  actions,
  description,
  title,
}: SettingsPageHeaderProps) {
  // 没有页级操作的设置页不套外层行，DOM 与加 actions 之前保持一致。
  if (!actions) {
    return (
      <div className="flex max-w-3xl flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">{title}</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
    );
  }

  // 标题块必须可收缩（min-w-0 flex-1），否则 max-w-3xl 的固有宽度加上操作按钮会撑破
  // 容器，操作被挤到下一行并左对齐——描述越长、按钮越多越容易触发。
  return (
    <div
      data-slot="settings-page-header"
      className="flex items-start justify-between gap-3"
    >
      <div className="flex min-w-0 max-w-3xl flex-1 flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">{title}</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-2">{actions}</div>
    </div>
  );
}
