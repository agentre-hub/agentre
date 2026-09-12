import * as React from "react";
import { Boxes, RefreshCw, Search, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button, Dialog, DialogContent } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { TriStateToggle } from "./tri-state-toggle";
import {
  groupCatalogItems,
  type CatalogBadgeTone,
  type CatalogItem,
  type TriState,
} from "./catalog";

type Props = {
  open: boolean;
  title: string; // 已 t() 解析
  subtitle?: string; // 已 t() 解析
  searchPlaceholder: string; // 已 t() 解析
  items: CatalogItem[];
  loading?: boolean;
  footerNote?: string; // 已 t() 解析（如个人技能只读提示）
  onToggle: (id: string) => void;
  onSetState?: (id: string, next: TriState) => void;
  triLabels?: Record<TriState, string>; // 已 t() 解析,三态行用
  footerSummary?: string; // 已 t() 解析,替代默认"已选 N"
  onConfirm: () => void;
  onCancel: () => void;
  onRescan?: () => void;
};

const badgeToneClass: Record<CatalogBadgeTone, string> = {
  recommended: "bg-primary-soft text-primary-text",
  installed: "bg-status-running-bg text-status-running",
  available: "bg-secondary text-muted-foreground",
  approval: "bg-status-waiting-bg text-status-waiting",
  needInstall: "bg-status-waiting-bg text-status-waiting",
};

export function CapabilityPicker(props: Props) {
  const { t } = useTranslation();
  const [query, setQuery] = React.useState("");

  const filtered = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return props.items;
    return props.items.filter(
      (it) =>
        it.name.toLowerCase().includes(q) ||
        it.description.toLowerCase().includes(q),
    );
  }, [props.items, query]);

  const groups = React.useMemo(() => groupCatalogItems(filtered), [filtered]);
  const selectedCount = props.items.filter((it) => it.enabled).length;

  return (
    <Dialog open={props.open} onOpenChange={(o) => !o && props.onCancel()}>
      {props.open && (
        <DialogContent
          className="max-w-[460px] gap-0 overflow-hidden p-0"
          showCloseButton={false}
        >
          {/* header */}
          <div className="flex flex-col gap-1.5 border-b border-border px-[18px] py-3.5">
            <div className="flex items-center gap-2">
              <Boxes className="size-4 text-primary-text" aria-hidden="true" />
              <span className="text-prose font-semibold">{props.title}</span>
              <div className="flex-1" />
              <button
                type="button"
                aria-label={t("capability.picker.close")}
                onClick={props.onCancel}
                className="text-muted-foreground hover:text-foreground"
              >
                <X className="size-4" />
              </button>
            </div>
            {props.subtitle && (
              <span className="font-mono text-2xs text-muted-foreground">
                {props.subtitle}
              </span>
            )}
          </div>

          {/* search + rescan */}
          <div className="flex items-center gap-2.5 border-b border-border/60 px-[18px] py-3">
            <div className="flex flex-1 items-center gap-2 rounded-md border border-border px-2.5 py-2">
              <Search
                className="size-3.5 text-muted-foreground"
                aria-hidden="true"
              />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={props.searchPlaceholder}
                aria-label={props.searchPlaceholder}
                className="w-full bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              />
            </div>
            {props.onRescan && (
              <Button
                variant="outline"
                size="sm"
                className="h-auto gap-1.5 px-2.5 py-2"
                onClick={props.onRescan}
              >
                <RefreshCw className="size-3" aria-hidden="true" />
                {t("capability.picker.rescan")}
              </Button>
            )}
          </div>

          {/* body */}
          <div className="flex max-h-[420px] flex-col gap-2 overflow-y-auto px-4 py-3">
            {props.loading ? (
              <p className="py-6 text-center text-2xs text-muted-foreground">
                {t("capability.picker.loading")}
              </p>
            ) : props.items.length === 0 ? (
              <p className="py-6 text-center text-2xs text-muted-foreground">
                {t("capability.picker.empty")}
              </p>
            ) : groups.length === 0 ? (
              <p className="py-6 text-center text-2xs text-muted-foreground">
                {t("capability.picker.searchEmpty")}
              </p>
            ) : (
              groups.map((g) => (
                <div key={g.group} className="flex flex-col gap-1.5">
                  {g.group && (
                    <div className="flex items-center gap-2 pt-1">
                      <span className="font-mono text-2xs font-semibold uppercase text-muted-foreground">
                        {g.group}
                      </span>
                      <div className="flex-1" />
                      <span className="font-mono text-2xs text-muted-foreground">
                        {g.items.length}
                      </span>
                    </div>
                  )}
                  {g.items.map((it) => {
                    const disabled = Boolean(it.disabledReason);
                    if (it.state && props.triLabels) {
                      return (
                        <div
                          key={it.id}
                          className={cn(
                            "flex items-center gap-2.5 rounded-md border border-border bg-card px-2.5 py-2",
                            disabled && "opacity-50",
                          )}
                        >
                          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                            <span className="truncate text-xs font-semibold">
                              {it.name}
                            </span>
                            <span className="truncate font-mono text-2xs text-muted-foreground">
                              {it.description}
                            </span>
                          </span>
                          {it.badges && it.badges.length > 0 && (
                            <span className="flex shrink-0 items-center gap-1">
                              {it.badges.map((b) => (
                                <span
                                  key={b.label}
                                  className={cn(
                                    "rounded px-1.5 py-0.5 font-mono text-2xs font-semibold",
                                    badgeToneClass[b.tone],
                                  )}
                                >
                                  {b.label}
                                </span>
                              ))}
                            </span>
                          )}
                          {disabled ? (
                            <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                              {it.disabledReason}
                            </span>
                          ) : (
                            <TriStateToggle
                              value={it.state}
                              labels={props.triLabels}
                              onChange={(next) =>
                                props.onSetState?.(it.id, next)
                              }
                            />
                          )}
                        </div>
                      );
                    }
                    return (
                      <button
                        key={it.id}
                        type="button"
                        role="checkbox"
                        aria-checked={it.enabled}
                        aria-label={it.name}
                        disabled={disabled}
                        onClick={() => !disabled && props.onToggle(it.id)}
                        className={cn(
                          "flex items-center gap-2.5 rounded-md border px-2.5 py-2 text-left transition-colors",
                          it.enabled
                            ? "border-primary/30 bg-primary-soft/40"
                            : "border-border bg-card hover:bg-secondary/40",
                          disabled && "opacity-50",
                        )}
                      >
                        <span
                          className={cn(
                            "flex size-4 shrink-0 items-center justify-center rounded-[5px] border",
                            it.enabled
                              ? "border-primary bg-primary text-primary-foreground"
                              : "border-border bg-card",
                          )}
                          aria-hidden="true"
                        >
                          {it.enabled && <span className="text-3xs">✓</span>}
                        </span>
                        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                          <span className="truncate text-xs font-semibold">
                            {it.name}
                          </span>
                          <span className="truncate font-mono text-2xs text-muted-foreground">
                            {it.description}
                          </span>
                        </span>
                        {it.badges && it.badges.length > 0 && (
                          <span className="flex shrink-0 items-center gap-1">
                            {it.badges.map((b) => (
                              <span
                                key={b.label}
                                className={cn(
                                  "rounded px-1.5 py-0.5 font-mono text-2xs font-semibold",
                                  badgeToneClass[b.tone],
                                )}
                              >
                                {b.label}
                              </span>
                            ))}
                          </span>
                        )}
                        {it.disabledReason && (
                          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                            {it.disabledReason}
                          </span>
                        )}
                      </button>
                    );
                  })}
                </div>
              ))
            )}
            {props.footerNote && (
              <p className="mt-1 rounded-md bg-secondary/40 px-2.5 py-2 font-mono text-2xs text-muted-foreground">
                {props.footerNote}
              </p>
            )}
          </div>

          {/* footer */}
          <div className="flex items-center gap-2.5 border-t border-border bg-secondary/30 px-[18px] py-3">
            <span className="flex-1 font-mono text-2xs text-muted-foreground">
              {props.footerSummary ??
                t("capability.picker.selected", { count: selectedCount })}
            </span>
            <Button variant="outline" size="sm" onClick={props.onCancel}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={props.onConfirm}>
              {t("capability.picker.done")}
            </Button>
          </div>
        </DialogContent>
      )}
    </Dialog>
  );
}
