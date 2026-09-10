// 区块外壳:标题 + 说明 + children,页面各处复用。

import * as React from "react";
import type { LucideIcon } from "lucide-react";

export function SectionCard({
  icon: Icon,
  title,
  subtitle,
  action,
  children,
}: {
  icon: LucideIcon;
  title: string;
  subtitle: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
        <span className="flex h-[30px] w-[30px] items-center justify-center rounded-md bg-secondary text-primary">
          <Icon className="h-4 w-4" />
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="text-aux font-semibold text-foreground">
            {title}
          </span>
          <span className="font-mono text-3xs text-muted-foreground">
            {subtitle}
          </span>
        </span>
        {action}
      </div>
      <div className="flex flex-col gap-3 p-3.5">{children}</div>
    </div>
  );
}

// ── Script tab (trigger + script + env) ──────────────────────────────────────
