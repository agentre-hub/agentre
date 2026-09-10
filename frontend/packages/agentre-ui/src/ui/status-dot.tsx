import * as React from "react";

import { cn } from "../lib/utils";
import { statusConfig } from "../transcript/agent-status";
import type { AgentStatus } from "../transcript/agent-status";

type StatusDotProps = React.ComponentProps<"span"> & {
  status: AgentStatus;
  size?: "xs" | "sm" | "md";
};

const dotSizeClassNames = {
  xs: "size-1.5",
  sm: "size-2",
  md: "size-2.5",
};

/**
 * 会话运行态的圆点。状态**同时**以 `aria-label` 呈现 —— 不只靠颜色。
 *
 * 调用方把它藏进折叠区域时传 `aria-hidden` + `aria-label={undefined}`
 * 覆盖掉（见 `SessionRow`）：一个读屏读得到但点不着的圆点比没有更糟。
 */
function StatusDot({
  className,
  size = "sm",
  status,
  ...props
}: StatusDotProps) {
  const config = statusConfig[status];

  return (
    <span
      aria-label={`${config.label.toLowerCase()} status`}
      className={cn(
        "inline-block shrink-0 rounded-full",
        dotSizeClassNames[size],
        config.dotClassName,
        className,
      )}
      {...props}
    />
  );
}

export { StatusDot };
export type { StatusDotProps };
