import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";

import { cn } from "../lib/utils";
import { useTouch } from "./touch";

// outline 与 secondary 是同一套外观：相对前景色 8% 的填充、悬停 12%，不画边。
const BUTTON_FILL =
  "bg-button-fill text-secondary-foreground hover:bg-button-fill-hover";

const buttonVariants = cva(
  "inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap transition-all outline-none focus-visible:focus-outline disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        destructive:
          "bg-destructive text-destructive-foreground hover:bg-destructive/90 dark:bg-destructive/60",
        outline: BUTTON_FILL,
        secondary: BUTTON_FILL,
        ghost:
          "hover:bg-accent hover:text-accent-foreground dark:hover:bg-accent/50",
        link: "text-primary-text underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4 py-2 has-[>svg]:px-3",
        xs: "h-6 gap-1 rounded-md px-2 text-xs has-[>svg]:px-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 rounded-md px-3 has-[>svg]:px-2.5",
        lg: "h-10 rounded-md px-6 has-[>svg]:px-4",
        icon: "size-9",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

// 触屏按下态：底色比静止 / 悬停再深一档。触屏没有悬停，hover 类只剩「点完粘住」，
// 所以只在宿主打开触屏档时才带这些类，桌面外观不变。
const BUTTON_PRESSED: Record<string, string> = {
  default: "active:bg-primary-pressed",
  // 深色下静止是 /60，按下沿用同一不透明度、换更深的底色。
  destructive:
    "active:bg-destructive-pressed dark:active:bg-destructive-pressed/60",
  outline: "active:bg-foreground/20",
  secondary: "active:bg-foreground/20",
  ghost: "active:bg-foreground/15",
  link: "",
};

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  touch,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
    /** 触屏档：按下时底色加深一档。不传则跟随最近的 TouchProvider。 */
    touch?: boolean;
  }) {
  const isTouch = useTouch(touch);
  const Comp = asChild ? Slot.Root : "button";

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(
        buttonVariants({ variant, size, className }),
        isTouch && BUTTON_PRESSED[variant ?? "default"],
      )}
      {...props}
    />
  );
}

export { Button, buttonVariants };
