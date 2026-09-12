import * as React from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  CheckCircle2,
  KeyRound,
  Loader2,
  Plus,
  Save,
  Terminal,
  Timer,
  Trash2,
  XCircle,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import {
  formatRelativeTime,
  runOk,
  type Draft,
  type EnvVar,
  type HookEventItem,
  type InterpreterOption,
  type RunHookResult,
} from "./hooks-page-model";
import { HookDetailHeader } from "./hooks-page-header";
import { HooksSidebar } from "./hooks-page-sidebar";
import { useHooksPage } from "./use-hooks-page";

const TZ_OPTIONS = [
  "Asia/Shanghai",
  "UTC",
  "America/New_York",
  "Europe/London",
  "Asia/Tokyo",
];

// ── Run result (inline dry-run / run-now output) ─────────────────────────────

function RunResultCard({ result, t }: { result: RunHookResult; t: TFunction }) {
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

function SectionCard({
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

function ScriptTab({
  draft,
  onChange,
  interpreters,
  t,
}: {
  draft: Draft;
  onChange: (next: Draft) => void;
  interpreters: InterpreterOption[];
  t: TFunction;
}) {
  const setEnv = (env: EnvVar[]) => onChange({ ...draft, env });

  // Ensure the currently selected interpreter always appears even if not in
  // the probed list (e.g. while probing or on a platform mismatch).
  const options =
    interpreters.some((o) => o.key === draft.interpreter) ||
    interpreters.length === 0
      ? interpreters
      : [
          { key: draft.interpreter, path: "", installed: false },
          ...interpreters,
        ];
  const selected = options.find((o) => o.key === draft.interpreter);

  return (
    <div className="flex flex-col gap-4">
      <SectionCard
        icon={Timer}
        title={t("hooks.trigger.title")}
        subtitle={t("hooks.trigger.subtitle")}
      >
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.cronLabel")}
            </span>
            <Input
              value={draft.scheduleExpr}
              onChange={(e) =>
                onChange({ ...draft, scheduleExpr: e.target.value })
              }
              className="w-44 font-mono text-xs"
              aria-label={t("hooks.trigger.cronLabel")}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.interpreter")}
            </span>
            <Select
              value={draft.interpreter}
              onValueChange={(v) => onChange({ ...draft, interpreter: v })}
            >
              <SelectTrigger
                className="w-40"
                aria-label={t("hooks.trigger.interpreter")}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {options.map((opt) => (
                  <SelectItem
                    key={opt.key}
                    value={opt.key}
                    disabled={!opt.installed}
                  >
                    {t(`hooks.interp.${opt.key}`)}
                    {!opt.installed && (
                      <span className="ml-1.5 text-3xs text-muted-foreground">
                        {t("hooks.interp.notInstalled")}
                      </span>
                    )}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.interpreterPath")}
            </span>
            <Input
              value={draft.interpreterPath}
              onChange={(e) =>
                onChange({ ...draft, interpreterPath: e.target.value })
              }
              placeholder={
                selected?.installed && selected.path
                  ? t("hooks.trigger.interpreterPathAuto", {
                      path: selected.path,
                    })
                  : t("hooks.trigger.interpreterPathPlaceholder")
              }
              className="w-72 font-mono text-xs"
              aria-label={t("hooks.trigger.interpreterPath")}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.timezone")}
            </span>
            <Select
              value={draft.timezone}
              onValueChange={(v) => onChange({ ...draft, timezone: v })}
            >
              <SelectTrigger
                className="w-44"
                aria-label={t("hooks.trigger.timezone")}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TZ_OPTIONS.map((v) => (
                  <SelectItem key={v} value={v}>
                    {v}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        </div>
      </SectionCard>

      <SectionCard
        icon={Terminal}
        title={t("hooks.script.title")}
        subtitle={t("hooks.script.subtitle")}
      >
        <label className="flex flex-col gap-1">
          <span className="text-2xs text-muted-foreground">
            {t("hooks.script.name")}
          </span>
          <Input
            value={draft.name}
            onChange={(e) => onChange({ ...draft, name: e.target.value })}
            placeholder={t("hooks.script.namePlaceholder")}
            aria-label={t("hooks.script.name")}
          />
        </label>
        <Textarea
          value={draft.command}
          onChange={(e) => onChange({ ...draft, command: e.target.value })}
          placeholder={t("hooks.script.commandPlaceholder")}
          aria-label={t("hooks.script.title")}
          spellCheck={false}
          className="min-h-56 rounded-md border-border bg-code-surface font-mono text-xs leading-relaxed text-code-foreground"
        />
      </SectionCard>

      <SectionCard
        icon={KeyRound}
        title={t("hooks.env.title")}
        subtitle={t("hooks.env.subtitle")}
        action={
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 font-mono text-2xs"
            onClick={() =>
              setEnv([...draft.env, { key: "", value: "", secret: false }])
            }
          >
            <Plus className="mr-1 h-3.5 w-3.5" />
            {t("hooks.env.add")}
          </Button>
        }
      >
        {draft.env.length === 0 ? (
          <p className="py-1 text-xs text-muted-foreground">
            {t("hooks.env.empty")}
          </p>
        ) : (
          draft.env.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                value={row.key}
                onChange={(e) =>
                  setEnv(
                    draft.env.map((r, j) =>
                      j === i ? { ...r, key: e.target.value } : r,
                    ),
                  )
                }
                placeholder={t("hooks.env.keyPlaceholder")}
                aria-label={t("hooks.env.keyPlaceholder")}
                className="w-40 font-mono text-xs"
              />
              <Input
                type={row.secret ? "password" : "text"}
                value={row.value}
                onChange={(e) =>
                  setEnv(
                    draft.env.map((r, j) =>
                      j === i ? { ...r, value: e.target.value } : r,
                    ),
                  )
                }
                placeholder={t("hooks.env.valuePlaceholder")}
                aria-label={t("hooks.env.valuePlaceholder")}
                className="flex-1 font-mono text-xs"
              />
              <label className="flex items-center gap-1.5 text-2xs text-muted-foreground">
                <Switch
                  checked={row.secret}
                  onCheckedChange={(checked) =>
                    setEnv(
                      draft.env.map((r, j) =>
                        j === i ? { ...r, secret: checked } : r,
                      ),
                    )
                  }
                  aria-label={t("hooks.env.secret")}
                />
                {t("hooks.env.secret")}
              </label>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-8 w-8 text-muted-foreground"
                aria-label={t("hooks.env.remove")}
                onClick={() => setEnv(draft.env.filter((_, j) => j !== i))}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))
        )}
      </SectionCard>
    </div>
  );
}

// ── Run log tab (events + payload detail) ────────────────────────────────────

function RunLogTab({
  events,
  selectedEventId,
  onSelectEvent,
  t,
}: {
  events: HookEventItem[];
  selectedEventId: number | null;
  onSelectEvent: (id: number) => void;
  t: TFunction;
}) {
  const selected = events.find((e) => e.id === selectedEventId) ?? null;
  if (events.length === 0) {
    return (
      <p className="px-2 py-8 text-center text-sm text-muted-foreground">
        {t("hooks.log.empty")}
      </p>
    );
  }
  return (
    <div className="flex gap-4">
      <div className="flex w-80 shrink-0 flex-col gap-1">
        {events.map((ev) => (
          <button
            key={ev.id}
            type="button"
            onClick={() => onSelectEvent(ev.id)}
            aria-current={ev.id === selectedEventId ? "true" : undefined}
            className={cn(
              "flex flex-col gap-1 rounded-md border px-3 py-2.5 text-left transition-colors",
              ev.id === selectedEventId
                ? "border-primary bg-primary/5"
                : "border-border hover:bg-muted/50",
            )}
          >
            <span className="flex min-w-0 items-center gap-1.5 text-xs font-medium">
              {ev.kind === "failure" ? (
                <XCircle
                  className="h-3 w-3 shrink-0 text-status-error"
                  aria-hidden
                />
              ) : null}
              <span
                className={cn(
                  "truncate",
                  ev.kind === "failure"
                    ? "text-status-error"
                    : "text-foreground",
                )}
              >
                {ev.title}
              </span>
            </span>
            <span className="font-mono text-3xs text-muted-foreground">
              {t("hooks.log.receivedAt", {
                time: formatRelativeTime(ev.receivedAt, t),
              })}
            </span>
          </button>
        ))}
      </div>
      <div className="min-w-0 flex-1">
        {selected ? (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <span className="flex items-center gap-2">
                {selected.kind === "failure" ? (
                  <Badge variant="destructive" className="shrink-0">
                    {t("hooks.log.failureBadge")}
                  </Badge>
                ) : null}
                <span
                  className={cn(
                    "text-sm font-semibold",
                    selected.kind === "failure"
                      ? "text-status-error"
                      : "text-foreground",
                  )}
                >
                  {selected.title}
                </span>
              </span>
              {selected.dedupeKey ? (
                <span
                  data-selectable-text="true"
                  className="font-mono text-3xs text-muted-foreground"
                >
                  {t("hooks.log.dedupeKey")}: {selected.dedupeKey}
                </span>
              ) : null}
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-mono text-3xs text-muted-foreground">
                {t("hooks.log.payload")}
              </span>
              <pre
                data-selectable-text="true"
                className="overflow-x-auto rounded-md border border-border bg-code-surface p-3 font-mono text-2xs leading-relaxed text-code-muted-foreground"
              >
                {selected.payloadJson}
              </pre>
            </div>
          </div>
        ) : (
          <p className="px-2 py-8 text-center text-sm text-muted-foreground">
            {t("hooks.log.selectPrompt")}
          </p>
        )}
      </div>
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────

export function HooksPage() {
  const { t } = useTranslation();
  const {
    hooks,
    events,
    selectedId,
    draft,
    setDraft,
    activeTab,
    setActiveTab,
    loading,
    busy,
    running,
    flash,
    query,
    setQuery,
    runResult,
    selectedEventId,
    setSelectedEventId,
    deleteTarget,
    setDeleteTarget,
    interpreters,
    filtered,
    selectedHook,
    headerMeta,
    selectHook,
    startCreate,
    save,
    toggle,
    confirmDelete,
    run,
  } = useHooksPage();

  if (loading) {
    return (
      <div className="flex h-full min-w-0 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        {t("hooks.loading")}
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-1">
      <HooksSidebar
        hooks={hooks}
        filtered={filtered}
        query={query}
        selectedId={selectedId}
        onQueryChange={setQuery}
        onSelect={selectHook}
        onCreate={startCreate}
        t={t}
      />

      {/* Main */}
      <main className="flex min-w-0 flex-1 flex-col">
        {flash ? (
          <div
            className={cn(
              "flex items-center gap-2 border-b px-7 py-2 text-xs",
              flash.kind === "ok"
                ? "border-status-running/30 bg-status-running-bg text-status-running"
                : "border-status-error/30 text-status-error",
            )}
            role="status"
          >
            {flash.text}
          </div>
        ) : null}

        {!draft ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
            <p>{t("hooks.list.empty")}</p>
            <Button type="button" size="sm" onClick={startCreate}>
              <Plus className="mr-1 h-3.5 w-3.5" />
              {t("hooks.list.addAria")}
            </Button>
          </div>
        ) : (
          <>
            <HookDetailHeader
              draft={draft}
              headerMeta={headerMeta}
              selectedHook={selectedHook}
              selectedId={selectedId}
              running={running}
              busy={busy}
              onRun={run}
              onToggle={toggle}
              onDelete={setDeleteTarget}
              t={t}
            />

            {/* Tabs */}
            <div className="flex gap-1 border-b border-border px-7">
              {(["script", "runLog"] as const).map((tab) => (
                <button
                  key={tab}
                  type="button"
                  role="tab"
                  aria-selected={activeTab === tab}
                  onClick={() => setActiveTab(tab)}
                  className={cn(
                    "flex items-center gap-1.5 border-b-2 px-1.5 pb-2.5 pt-3 text-aux font-medium transition-colors",
                    activeTab === tab
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted-foreground",
                  )}
                >
                  {t(`hooks.tabs.${tab}`)}
                  {tab === "runLog" && events.length > 0 ? (
                    <Badge variant="secondary" className="font-mono text-3xs">
                      {events.length}
                    </Badge>
                  ) : null}
                </button>
              ))}
            </div>

            {/* Body */}
            <div className="min-h-0 flex-1 overflow-y-auto px-7 py-5">
              {activeTab === "script" ? (
                <div className="flex flex-col gap-4">
                  <ScriptTab
                    draft={draft}
                    onChange={setDraft}
                    interpreters={interpreters}
                    t={t}
                  />
                  {runResult ? (
                    <RunResultCard result={runResult} t={t} />
                  ) : null}
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      data-testid="hook-save"
                      onClick={save}
                      disabled={busy || !draft.name.trim()}
                    >
                      {busy ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Save className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      {draft.id == null
                        ? t("hooks.script.create")
                        : t("hooks.script.save")}
                    </Button>
                  </div>
                </div>
              ) : (
                <RunLogTab
                  events={events}
                  selectedEventId={selectedEventId}
                  onSelectEvent={setSelectedEventId}
                  t={t}
                />
              )}
            </div>
          </>
        )}
      </main>

      {/* Delete confirm */}
      <Dialog
        open={deleteTarget != null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("hooks.del.title")}</DialogTitle>
            <DialogDescription>
              {t("hooks.del.description", { name: deleteTarget?.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setDeleteTarget(null)}
            >
              {t("hooks.del.cancel")}
            </Button>
            <Button type="button" variant="destructive" onClick={confirmDelete}>
              {t("hooks.del.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
