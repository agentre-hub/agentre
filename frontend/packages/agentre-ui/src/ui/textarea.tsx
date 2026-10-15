import * as React from "react";

import { cn } from "../lib/utils";

// 与 Input 同一组变体语义（见 input.tsx）。
const VARIANT = {
  default:
    "bg-field-fill hover:bg-field-fill-hover focus-visible:bg-sheet aria-invalid:ring-2 aria-invalid:ring-destructive",
  inline: "bg-transparent",
} as const;

function Textarea({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<"textarea"> & { variant?: keyof typeof VARIANT }) {
  return (
    <textarea
      data-slot="textarea"
      data-variant={variant}
      className={cn(
        // 字号在移动端是 16px：iOS 聚焦字号 < 16px 的输入控件会自动放大整个视口，
        // 而这条行为没有开关可关（唯一能压的 `user-scalable=no` 会把用户自己要的
        // 双指缩放一起禁掉）。md 及以上回到 14px，桌面排版逐像素不变。与同包的
        // `<Input>` 同一条口径。
        "w-full min-w-0 rounded-md px-3 py-2 text-base md:text-sm transition-[color,box-shadow,background-color] outline-none placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
        "focus-visible:focus-outline",
        VARIANT[variant],
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
