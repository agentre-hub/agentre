import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "../lib/utils";

const alertVariants = cva(
  "relative grid w-full grid-cols-[0_1fr] items-start gap-y-0.5 rounded-lg px-4 py-3 text-sm has-[>svg]:grid-cols-[calc(var(--spacing)*4)_1fr] has-[>svg]:gap-x-3 [&>svg]:size-4 [&>svg]:translate-y-0.5 [&>svg]:text-current",
  {
    variants: {
      variant: {
        default: "bg-card text-card-foreground",
        // 说明文字在亮色下用足色：waiting-text 叠 90% 在白卡片上只有 4.36。
        // 暗色保留原来比标题低一档的 /90（-text 与填充色同值，叠完仍 ≥4.9）。
        destructive:
          "bg-card text-destructive-text *:data-[slot=alert-description]:text-destructive-text dark:*:data-[slot=alert-description]:text-destructive-text/90 [&>svg]:text-current",
        // 「需要你处理，但还不是故障」这一档。没有它，等待 / 离线 / 待确认这类
        // 状态只能在中性与红之间二选一，红色因此变廉价、真出事时不再有人当回事。
        warning:
          "bg-card text-status-waiting-text *:data-[slot=alert-description]:text-status-waiting-text dark:*:data-[slot=alert-description]:text-status-waiting-text/90 [&>svg]:text-current",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

function Alert({
  className,
  variant,
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  return (
    <div
      data-slot="alert"
      role="alert"
      className={cn(alertVariants({ variant }), className)}
      {...props}
    />
  );
}

function AlertTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        "col-start-2 line-clamp-1 min-h-4 font-medium tracking-tight",
        className,
      )}
      {...props}
    />
  );
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        "col-start-2 grid justify-items-start gap-1 text-sm text-muted-foreground [&_p]:leading-relaxed",
        className,
      )}
      {...props}
    />
  );
}

export { Alert, AlertTitle, AlertDescription };
