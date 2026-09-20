import * as React from "react";

import { cn } from "../lib/utils";

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        // 字号在移动端是 16px：iOS 聚焦字号 < 16px 的输入控件会自动放大整个视口，
        // 而这条行为没有开关可关（唯一能压的 `user-scalable=no` 会把用户自己要的
        // 双指缩放一起禁掉）。md 及以上回到 14px，桌面排版逐像素不变。与同包的
        // `<Input>` 同一条口径。
        "w-full min-w-0 rounded-md border border-control-border bg-card px-3 py-2 text-base md:text-sm shadow-xs transition-[color,box-shadow] outline-none placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
        "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40",
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
