import { CircleX, Pencil } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { OutlineItem } from "../derive";

type Props = {
  items: OutlineItem[];
  activeMessageId: number | null;
  onSelect: (messageId: number) => void;
};

function formatTime(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return (
    d.getHours().toString().padStart(2, "0") +
    ":" +
    d.getMinutes().toString().padStart(2, "0")
  );
}

export function OutlineView({ items, activeMessageId, onSelect }: Props) {
  const { t } = useTranslation();

  if (items.length === 0) {
    return (
      <div className="px-3 py-6 text-center text-xs text-muted-foreground">
        {t("chatContext.outline.empty")}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-0.5 px-2 py-2.5">
      {items.map((it) => {
        const active = it.messageId === activeMessageId;
        return (
          <button
            key={it.messageId}
            type="button"
            onClick={() => onSelect(it.messageId)}
            data-active={active}
            data-outline-message-id={it.messageId}
            className={cn(
              "flex flex-col gap-1 rounded-md px-2.5 py-2 text-left text-xs transition-colors",
              active
                ? "border-l-2 border-primary bg-primary/10 text-foreground"
                : "border-l-2 border-transparent text-muted-foreground hover:bg-muted/50",
            )}
          >
            <div
              className={cn(
                "flex items-center gap-1.5 font-mono text-3xs",
                active ? "text-primary" : "text-muted-foreground/80",
              )}
            >
              <span>{formatTime(it.time)}</span>
              <span className="text-border-strong">·</span>
              <span>{t("chatContext.outline.turn", { turn: it.turn })}</span>
            </div>
            <p
              className={cn(
                "line-clamp-2 leading-snug",
                active ? "font-medium" : "",
              )}
            >
              {it.text}
            </p>
            {it.edits > 0 || it.err ? (
              <div className="flex items-center gap-1.5">
                {it.err ? (
                  <span className="inline-flex items-center gap-1 rounded-sm bg-destructive/10 px-1.5 py-0.5 text-3xs font-medium text-destructive">
                    <CircleX className="size-2.5" aria-hidden="true" />
                    error
                  </span>
                ) : null}
                {it.edits > 0 ? (
                  <span className="inline-flex items-center gap-1 rounded-sm border border-border bg-card px-1.5 py-0.5 text-3xs font-medium text-muted-foreground">
                    <Pencil className="size-2.5" aria-hidden="true" />
                    {it.edits} edits
                  </span>
                ) : null}
              </div>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
