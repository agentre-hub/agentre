// 一次试跑的结果卡片(退出码、stdout / stderr、耗时)。

import type { TFunction } from "i18next";
import { CheckCircle2, XCircle } from "lucide-react";

import { cn } from "@/lib/utils";

import { runOk, type RunHookResult } from "../hooks-page-model";

export function RunResultCard({
  result,
  t,
}: {
  result: RunHookResult;
  t: TFunction;
}) {
  const ok = runOk(result);
  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border",
        ok
          ? "border-status-running bg-status-running-bg"
          : "border-status-error",
      )}
    >
      <div
        className="flex items-center gap-2.5 border-b px-3.5 py-2.5"
        style={{
          borderColor: ok
            ? "var(--color-status-running)"
            : "var(--color-status-error)",
        }}
      >
        {ok ? (
          <CheckCircle2 className="h-3.5 w-3.5 text-status-running" />
        ) : (
          <XCircle className="h-3.5 w-3.5 text-status-error" />
        )}
        <span
          className={cn(
            "text-xs font-bold",
            ok ? "text-status-running" : "text-status-error",
          )}
        >
          {ok
            ? t("hooks.run.ok", { code: result.exitCode })
            : result.timedOut
              ? t("hooks.run.timedOut")
              : t("hooks.run.failed", { code: result.exitCode })}
        </span>
        <span className="flex-1" />
        <span className="font-mono text-3xs text-muted-foreground">
          {t("hooks.run.meta", {
            ms: result.durationMs,
            persist: result.persisted
              ? t("hooks.run.persisted")
              : t("hooks.run.noPersist"),
          })}
        </span>
      </div>
      <div className="flex flex-col gap-2.5 p-3.5">
        {result.parseError ? (
          <p className="text-xs text-status-error">
            {t("hooks.run.parseError", { error: result.parseError })}
          </p>
        ) : (
          <p className="text-xs text-foreground">
            {t("hooks.run.summary", {
              events: result.events?.length ?? 0,
              new: result.newCount,
              dup: result.dupCount,
            })}
          </p>
        )}
        {result.stdout ? (
          <div className="flex flex-col gap-1">
            <span className="font-mono text-3xs text-muted-foreground">
              {t("hooks.run.stdout")}
            </span>
            <pre
              data-selectable-text="true"
              className="overflow-x-auto rounded-md border border-border bg-code-surface p-3 font-mono text-2xs leading-relaxed text-code-muted-foreground"
            >
              {result.stdout}
            </pre>
          </div>
        ) : null}
        {result.stderr ? (
          <div className="flex flex-col gap-1">
            <span className="font-mono text-3xs text-muted-foreground">
              {t("hooks.run.stderr")}
            </span>
            <pre
              data-selectable-text="true"
              className="overflow-x-auto rounded-md border border-border bg-code-surface p-3 font-mono text-2xs leading-relaxed text-status-error"
            >
              {result.stderr}
            </pre>
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ── Section card shell ───────────────────────────────────────────────────────
