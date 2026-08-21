// @ts-nocheck
import * as React from "react";
import { Select as SelectPrimitive } from "radix-ui";
import { Check, ChevronDown, ChevronUp } from "lucide-react";

import { cn } from "../../lib/utils";

const Select = SelectPrimitive.Root;
const SelectGroup = SelectPrimitive.Group;
const SelectValue = SelectPrimitive.Value;

function SelectTrigger({
  className,
  children,
  size = "sm",
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger> & {
  size?: "sm" | "md";
}) {
  return (
    <SelectPrimitive.Trigger
      data-slot="select-trigger"
      data-size={size}
      className={cn(
        "group/select-trigger flex w-full min-w-0 items-center justify-between gap-2 rounded-md border border-input bg-transparent text-left text-sm font-medium text-foreground shadow-xs outline-none transition-[color,box-shadow,border-color] dark:bg-input/30",
        "data-[size=sm]:h-9 data-[size=sm]:px-3 data-[size=md]:h-10 data-[size=md]:px-3.5",
        "placeholder:text-muted-foreground data-[placeholder]:text-muted-foreground",
        "hover:border-border-strong",
        "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40",
        "data-[state=open]:border-ring data-[state=open]:ring-[3px] data-[state=open]:ring-ring/30",
        "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/20",
        "[&_svg]:pointer-events-none [&_svg]:shrink-0",
        className,
      )}
      {...props}
    >
      <span className="flex min-w-0 items-center gap-2 truncate">
        {children}
      </span>
      <SelectPrimitive.Icon asChild>
        <ChevronDown
          className="size-3.5 text-muted-foreground transition-transform duration-150 group-data-[state=open]/select-trigger:rotate-180 group-data-[state=open]/select-trigger:text-foreground"
          aria-hidden="true"
        />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  );
}

function SelectScrollUpButton({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.ScrollUpButton>) {
  return (
    <SelectPrimitive.ScrollUpButton
      data-slot="select-scroll-up"
      className={cn(
        "flex cursor-default items-center justify-center py-1 text-muted-foreground",
        className,
      )}
      {...props}
    >
      <ChevronUp className="size-3.5" aria-hidden="true" />
    </SelectPrimitive.ScrollUpButton>
  );
}

function SelectScrollDownButton({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.ScrollDownButton>) {
  return (
    <SelectPrimitive.ScrollDownButton
      data-slot="select-scroll-down"
      className={cn(
        "flex cursor-default items-center justify-center py-1 text-muted-foreground",
        className,
      )}
      {...props}
    >
      <ChevronDown className="size-3.5" aria-hidden="true" />
    </SelectPrimitive.ScrollDownButton>
  );
}

function SelectContent({
  className,
  children,
  position = "popper",
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        data-slot="select-content"
        position={position}
        sideOffset={sideOffset}
        className={cn(
          // 注意：max-h 只用纯 Tailwind 任意值，不要再嵌套 `min(..., var(...))`。
          // var(--radix-select-content-available-height) 在 Radix 把 popper 定位前是
          // guaranteed-invalid，导致整条 `min(...)` 失效、max-height 落空，弹层不限高。
          "relative z-50 max-h-[280px] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-[0_12px_36px_-12px_rgba(0,0,0,0.25),0_0_0_1px_rgba(0,0,0,0.04)] outline-none",
          "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          "data-[side=bottom]:slide-in-from-top-1 data-[side=top]:slide-in-from-bottom-1",
          className,
        )}
        {...props}
      >
        <SelectScrollUpButton />
        <SelectPrimitive.Viewport className="p-1">
          {children}
        </SelectPrimitive.Viewport>
        <SelectScrollDownButton />
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  );
}

function SelectLabel({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Label>) {
  return (
    <SelectPrimitive.Label
      data-slot="select-label"
      className={cn(
        "px-2 py-1.5 font-mono text-2xs uppercase tracking-[0.08em] text-muted-foreground",
        className,
      )}
      {...props}
    />
  );
}

function SelectItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={cn(
        "relative flex w-full cursor-pointer select-none items-center gap-2 rounded-md py-1.5 pl-2 pr-7 text-sm text-foreground outline-none",
        "focus:bg-accent focus:text-accent-foreground",
        "data-[state=checked]:bg-primary-soft data-[state=checked]:text-primary-text",
        "data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <SelectPrimitive.ItemText asChild>
        <span className="flex min-w-0 flex-1 items-center gap-2 truncate">
          {children}
        </span>
      </SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator className="absolute right-2 inline-flex size-3.5 items-center justify-center">
        <Check className="size-3.5 text-primary-text" aria-hidden="true" />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  );
}

function SelectSeparator({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Separator>) {
  return (
    <SelectPrimitive.Separator
      data-slot="select-separator"
      className={cn("mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  );
}

export {
  Select,
  SelectGroup,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectLabel,
  SelectItem,
  SelectSeparator,
};
