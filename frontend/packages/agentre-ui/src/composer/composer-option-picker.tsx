import { Check, ChevronDown } from "lucide-react";
import * as React from "react";

import { cn } from "../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

export type ComposerOption = {
  label: string;
  value: string;
  description?: string;
  disabled?: boolean;
};

export function ComposerOptionPicker({
  ariaLabel,
  disabled,
  onChange,
  options,
  value,
}: {
  ariaLabel: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  options: readonly ComposerOption[];
  value: string;
}) {
  const [open, setOpen] = React.useState(false);
  const selected = options.find((option) => option.value === value);
  if (options.length === 0) return null;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          aria-label={ariaLabel}
          className="inline-flex h-6 max-w-40 items-center gap-1 rounded-md border border-border bg-background px-2 text-2xs font-medium text-foreground disabled:opacity-50"
        >
          <span className="truncate">{selected?.label ?? value}</span>
          <ChevronDown aria-hidden="true" className="size-3 shrink-0" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" side="top" className="w-64 p-1.5">
        <div
          role="listbox"
          aria-label={ariaLabel}
          className="flex flex-col gap-0.5"
        >
          {options.map((option) => (
            <button
              key={option.value}
              type="button"
              role="option"
              aria-selected={option.value === value}
              aria-label={option.label}
              disabled={option.disabled}
              className={cn(
                "flex w-full items-start gap-2 rounded-md px-2.5 py-2 text-left text-xs hover:bg-accent disabled:opacity-50",
                option.value === value && "bg-accent",
              )}
              onClick={() => {
                setOpen(false);
                onChange(option.value);
              }}
            >
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{option.label}</span>
                {option.description ? (
                  <span className="block text-2xs text-muted-foreground">
                    {option.description}
                  </span>
                ) : null}
              </span>
              {option.value === value ? (
                <Check aria-hidden="true" className="size-3.5 shrink-0" />
              ) : null}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
