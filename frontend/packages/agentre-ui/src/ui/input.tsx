import * as React from "react";

import { cn } from "../lib/utils";

// 默认是填充字段：静止无边、5% 填充（悬停 7%），聚焦时底色回到内容页色、只出焦点环。
// inline 是就地编辑的文字（看起来是文字、点了才能改）：不填充，聚焦只出焦点环。
const VARIANT = {
  default:
    "bg-field-fill hover:bg-field-fill-hover focus-visible:bg-sheet aria-invalid:ring-2 aria-invalid:ring-destructive",
  inline: "bg-transparent",
} as const;

function Input({
  variant = "default",
  autoCapitalize = "off",
  autoComplete = "off",
  autoCorrect = "off",
  className,
  spellCheck = false,
  type,
  ...props
}: React.ComponentProps<"input"> & { variant?: keyof typeof VARIANT }) {
  return (
    <input
      type={type}
      data-slot="input"
      data-variant={variant}
      autoCapitalize={autoCapitalize}
      autoComplete={autoComplete}
      autoCorrect={autoCorrect}
      spellCheck={spellCheck}
      className={cn(
        "h-9 w-full min-w-0 rounded-md px-3 py-1 text-base transition-[color,box-shadow,background-color] outline-none selection:bg-primary selection:text-primary-foreground file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
        "focus-visible:focus-outline",
        VARIANT[variant],
        className,
      )}
      {...props}
    />
  );
}

export { Input };
