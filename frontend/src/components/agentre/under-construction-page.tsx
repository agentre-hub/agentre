import * as React from "react";
import { useTranslation } from "react-i18next";
import { Hammer } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { Badge } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

type UnderConstructionPageProps = React.ComponentProps<"section"> & {
  description: string;
  icon?: LucideIcon;
  title: string;
};

function UnderConstructionPage({
  className,
  description,
  icon: Icon = Hammer,
  title,
  ...props
}: UnderConstructionPageProps) {
  const { t } = useTranslation();
  const titleId = React.useId();

  return (
    <section
      aria-labelledby={titleId}
      className={cn(
        "relative flex min-h-full w-full min-w-0 flex-col justify-center overflow-hidden px-4 py-8 sm:px-6 lg:px-10",
        className,
      )}
      {...props}
    >
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 -z-0 [background-image:linear-gradient(to_right,var(--color-border)_1px,transparent_1px),linear-gradient(to_bottom,var(--color-border)_1px,transparent_1px)] [background-position:0_0] [background-size:32px_32px] opacity-[0.18] [mask-image:radial-gradient(ellipse_at_top_left,black,transparent_70%)]"
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -left-24 -top-24 -z-0 size-72 rounded-full bg-primary/10 blur-3xl"
      />

      <div className="relative z-10 flex max-w-2xl flex-col gap-5">
        <div className="flex flex-wrap items-center gap-3">
          <span className="relative inline-flex size-10 shrink-0 items-center justify-center rounded-lg border border-border bg-card text-primary-text shadow-xs">
            <Icon className="size-5" aria-hidden="true" />
            <span
              aria-hidden="true"
              className="absolute -bottom-0.5 -right-0.5 inline-flex size-2.5 items-center justify-center rounded-full border-2 border-card bg-status-waiting"
            />
          </span>
          <Badge
            variant="secondary"
            className="gap-1.5 rounded-sm px-2 py-0.5 font-mono text-2xs font-semibold"
          >
            <span
              aria-hidden="true"
              className="inline-block size-1.5 motion-safe:animate-pulse rounded-full bg-status-waiting"
            />
            {t("underConstruction.badge")}
          </Badge>
        </div>
        <div className="flex flex-col gap-2">
          <h1
            id={titleId}
            className="text-2xl font-semibold tracking-normal sm:text-3xl"
          >
            {title}
          </h1>
          <p className="max-w-xl text-sm leading-relaxed text-muted-foreground">
            {description}
          </p>
        </div>
        <div
          className="flex max-w-md flex-col gap-1.5 rounded-md border border-dashed border-border bg-card/40 px-3 py-2.5"
          aria-label={t("underConstruction.progressLabel")}
        >
          <div className="flex items-center justify-between font-mono text-2xs text-muted-foreground">
            <span>{t("underConstruction.progressLabel")}</span>
            <span>{t("underConstruction.planning")}</span>
          </div>
          <div
            aria-hidden="true"
            className="h-1 overflow-hidden rounded-full bg-secondary"
          >
            <div className="h-full w-1/4 rounded-full bg-status-waiting" />
          </div>
        </div>
      </div>
    </section>
  );
}

export { UnderConstructionPage };
