import type * as React from "react";

/**
 * 记忆的身份标记（来源、详情、所在层级）：只画图标，被替换掉的字成为可访问名与悬停提示。
 * 记忆页的行与转录里的记忆卡共用这一份。
 */
function MetaIcon({
  icon: Icon,
  name,
}: {
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  name: string;
}) {
  return (
    <span
      role="img"
      aria-label={name}
      title={name}
      className="inline-flex size-3 shrink-0 items-center justify-center"
    >
      <Icon aria-hidden className="size-full" />
    </span>
  );
}

export { MetaIcon };
