import * as React from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  Braces,
  CheckCircle2,
  KeyRound,
  Loader2,
  MoreHorizontal,
  Play,
  Plus,
  Power,
  PowerOff,
  Save,
  Search,
  Terminal,
  Timer,
  Trash2,
  XCircle,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

// ── Domain types (mirror wailsjs/go/models hook_svc.*) ───────────────────────

type EnvVar = { key: string; value: string; secret: boolean };

type InterpreterOption = { key: string; path: string; installed: boolean };

type HookItem = {
  id: number;
  name: string;
  interpreter: string;
  interpreterPath: string;
  command: string;
  scheduleExpr: string;
  timezone: string;
  env: EnvVar[];
  enabled: boolean;
  nextRunAt: number;
  lastRunAt: number;
  lastStatus: string;
  lastError: string;
  lastDurationMs: number;
  totalCount: number;
  createtime: number;
  updatetime: number;
};

type HookEventItem = {
  id: number;
  hookId: number;
  kind: string; // "output" (script-produced) | "failure" (run-failure log)
  title: string;
  dedupeKey: string;
  payloadJson: string;
  receivedAt: number;
  createtime: number;
};

type LoadHooksResponse = { hooks: HookItem[]; events: HookEventItem[] };

type RunHookResult = {
  exitCode: number;
  durationMs: number;
  timedOut: boolean;
  stdout: string;
  stderr: string;
  parseError: string;
  events: HookEventItem[];
  newCount: number;
  dupCount: number;
  persisted: boolean;
};

type HookWriteRequest = {
  name: string;
  interpreter: string;
  interpreterPath: string;
  command: string;
  scheduleExpr: string;
  timezone: string;
  env: EnvVar[];
  enabled: boolean;
};

type HookBridge = {
  LoadHooks: (req: {
    hookId?: number;
    limit?: number;
  }) => Promise<LoadHooksResponse>;
  CreateHook: (req: HookWriteRequest) => Promise<HookItem>;
  UpdateHook: (req: HookWriteRequest & { id: number }) => Promise<HookItem>;
  DeleteHook: (id: number) => Promise<void>;
  ToggleHook: (id: number, enabled: boolean) => Promise<HookItem>;
  RunHook: (req: { id: number; dryRun: boolean }) => Promise<RunHookResult>;
  ProbeInterpreters: () => Promise<InterpreterOption[]>;
};

type Draft = {
  id: number | null; // null = creating a new hook
  name: string;
  interpreter: string;
  interpreterPath: string;
  command: string;
  scheduleExpr: string;
  timezone: string;
  env: EnvVar[];
  enabled: boolean;
};

type FlashState = { kind: "ok" | "err"; text: string } | null;
type HookTab = "script" | "runLog";
type HookStatus = "ok" | "failed" | "running" | "disabled" | "idle";

// ── Constants ────────────────────────────────────────────────────────────────

// Secret env values come back from the backend as "********" and round-trip
// unchanged through CreateHook/UpdateHook, where preserveSecrets keeps the
// stored value — so the UI never needs the plaintext.

const INTERP_META: Record<
  string,
  { abbrev: string; icon: LucideIcon; color: string }
> = {
  bash: { abbrev: "SH", icon: Terminal, color: "agent-8" },
  sh: { abbrev: "SH", icon: Terminal, color: "agent-8" },
  node: { abbrev: "JS", icon: Braces, color: "agent-1" },
  python: { abbrev: "PY", icon: Terminal, color: "agent-4" },
  pwsh: { abbrev: "PS7", icon: Terminal, color: "agent-3" },
  powershell: { abbrev: "PS", icon: Terminal, color: "agent-5" },
  cmd: { abbrev: "CMD", icon: Terminal, color: "agent-15" },
};

const TZ_OPTIONS = [
  "Asia/Shanghai",
  "UTC",
  "America/New_York",
  "Europe/London",
  "Asia/Tokyo",
];

function interpMeta(interpreter: string) {
  return (
    INTERP_META[interpreter] ?? {
      abbrev: interpreter.toUpperCase(),
      icon: Terminal,
      color: "agent-15",
    }
  );
}

// ── Bridge to the Wails App bindings ─────────────────────────────────────────

function getBridge() {
  return (window as unknown as { go?: { app?: { App?: HookBridge } } }).go?.app
    ?.App;
}

function getBridgeMethod<K extends keyof HookBridge>(name: K): HookBridge[K] {
  const bridge = getBridge();
  const method = bridge?.[name];
  if (typeof method !== "function") {
    throw new Error(`Wails method ${String(name)} is unavailable`);
  }
  return method.bind(bridge) as HookBridge[K];
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatRelativeTime(seconds: number, t: TFunction): string {
  if (!seconds) return t("hooks.time.never");
  const diff = Math.max(0, Math.floor(Date.now() / 1000) - seconds);
  if (diff < 60) return t("hooks.time.secondsAgo", { count: diff });
  if (diff < 3600)
    return t("hooks.time.minutesAgo", { count: Math.floor(diff / 60) });
  if (diff < 86400)
    return t("hooks.time.hoursAgo", { count: Math.floor(diff / 3600) });
  return t("hooks.time.daysAgo", { count: Math.floor(diff / 86400) });
}

function hookStatus(h: HookItem): HookStatus {
  if (!h.enabled) return "disabled";
  if (h.lastStatus === "failed") return "failed";
  if (h.lastStatus === "running") return "running";
  if (h.lastStatus === "ok") return "ok";
  return "idle";
}

function draftFromHook(h: HookItem): Draft {
  return {
    id: h.id,
    name: h.name,
    interpreter: h.interpreter,
    interpreterPath: h.interpreterPath ?? "",
    command: h.command,
    scheduleExpr: h.scheduleExpr,
    timezone: h.timezone || "Asia/Shanghai",
    env: (h.env ?? []).map((e) => ({ ...e })),
    enabled: h.enabled,
  };
}

function emptyDraft(t: TFunction): Draft {
  return {
    id: null,
    name: t("hooks.create.defaultName"),
    interpreter: "bash",
    interpreterPath: "",
    command: "",
    scheduleExpr: "*/5 * * * *",
    timezone: "Asia/Shanghai",
    env: [],
    enabled: true,
  };
}

function hookMatchesQuery(h: HookItem, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return `${h.name} ${h.interpreter} ${h.scheduleExpr}`
    .toLowerCase()
    .includes(q);
}

function runOk(result: RunHookResult): boolean {
  return result.exitCode === 0 && !result.parseError && !result.timedOut;
}

// ── Left list ────────────────────────────────────────────────────────────────

function HookListItem({
  hook,
  selected,
  onSelect,
  t,
}: {
  hook: HookItem;
  selected: boolean;
  onSelect: () => void;
  t: TFunction;
}) {
  const meta = interpMeta(hook.interpreter);
  const Icon = meta.icon;
  const status = hookStatus(hook);
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
        selected
          ? "border-l-2 border-primary bg-primary/5"
          : "hover:bg-muted/60",
      )}
    >
      <span
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-white"
        style={{ backgroundColor: `var(--color-${meta.color})` }}
      >
        <Icon className="h-[15px] w-[15px]" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-xs font-semibold text-foreground">
          {hook.name}
        </span>
        <span className="truncate font-mono text-3xs text-muted-foreground">
          {meta.abbrev} · {hook.scheduleExpr}
        </span>
      </span>
      {status === "disabled" ? (
        <span className="font-mono text-3xs font-bold text-muted-foreground/70">
          {t("hooks.list.disabled")}
        </span>
      ) : (
        <span
          className={cn(
            "h-[7px] w-[7px] shrink-0 rounded-full",
            status === "failed"
              ? "bg-status-error"
              : status === "idle"
                ? "bg-muted-foreground/40"
                : "bg-status-running",
          )}
        />
      )}
    </button>
  );
}

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
  const [hooks, setHooks] = React.useState<HookItem[]>([]);
  const [events, setEvents] = React.useState<HookEventItem[]>([]);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [draft, setDraft] = React.useState<Draft | null>(null);
  const [activeTab, setActiveTab] = React.useState<HookTab>("script");
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [running, setRunning] = React.useState(false);
  const [flash, setFlash] = React.useState<FlashState>(null);
  const [query, setQuery] = React.useState("");
  const [runResult, setRunResult] = React.useState<RunHookResult | null>(null);
  const [selectedEventId, setSelectedEventId] = React.useState<number | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] = React.useState<HookItem | null>(null);
  const [interpreters, setInterpreters] = React.useState<InterpreterOption[]>(
    [],
  );

  const flashOk = React.useCallback(
    (text: string) => setFlash({ kind: "ok", text }),
    [],
  );
  const flashErr = React.useCallback(
    (text: string) => setFlash({ kind: "err", text }),
    [],
  );

  const loadEvents = React.useCallback(async (hookId: number) => {
    try {
      const resp = await getBridgeMethod("LoadHooks")({ hookId, limit: 50 });
      setEvents(resp.events ?? []);
      setSelectedEventId(resp.events?.[0]?.id ?? null);
    } catch {
      setEvents([]);
    }
  }, []);

  const selectHook = React.useCallback(
    (hook: HookItem) => {
      setSelectedId(hook.id);
      setDraft(draftFromHook(hook));
      setRunResult(null);
      void loadEvents(hook.id);
    },
    [loadEvents],
  );

  const reload = React.useCallback(
    async (preferId?: number | null) => {
      const resp = await getBridgeMethod("LoadHooks")({
        hookId: 0,
        limit: 100,
      });
      const list = resp.hooks ?? [];
      setHooks(list);
      const target = list.find((h) => h.id === preferId) ?? list[0] ?? null;
      if (target) {
        setSelectedId(target.id);
        setDraft(draftFromHook(target));
        void loadEvents(target.id);
      } else {
        setSelectedId(null);
        setDraft(null);
      }
      return list;
    },
    [loadEvents],
  );

  React.useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const resp = await getBridgeMethod("LoadHooks")({
          hookId: 0,
          limit: 100,
        });
        if (!alive) return;
        const list = resp.hooks ?? [];
        setHooks(list);
        if (list[0]) {
          setSelectedId(list[0].id);
          setDraft(draftFromHook(list[0]));
          void loadEvents(list[0].id);
        }
      } catch {
        if (alive) flashErr(t("hooks.flash.saveFailed", { error: "load" }));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [loadEvents, flashErr, t]);

  React.useEffect(() => {
    getBridgeMethod("ProbeInterpreters")()
      .then(setInterpreters)
      .catch(() => setInterpreters([]));
  }, []);

  const startCreate = () => {
    setSelectedId(null);
    setDraft(emptyDraft(t));
    setRunResult(null);
    setActiveTab("script");
  };

  const save = async () => {
    if (!draft) return;
    const payload: HookWriteRequest = {
      name: draft.name,
      interpreter: draft.interpreter,
      interpreterPath: draft.interpreterPath,
      command: draft.command,
      scheduleExpr: draft.scheduleExpr,
      timezone: draft.timezone,
      env: draft.env,
      enabled: draft.enabled,
    };
    setBusy(true);
    try {
      let saved: HookItem;
      if (draft.id == null) {
        saved = await getBridgeMethod("CreateHook")(payload);
        flashOk(t("hooks.flash.created", { name: saved.name }));
      } else {
        saved = await getBridgeMethod("UpdateHook")({
          ...payload,
          id: draft.id,
        });
        flashOk(t("hooks.flash.updated", { name: saved.name }));
      }
      await reload(saved.id);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (hook: HookItem) => {
    setBusy(true);
    try {
      await getBridgeMethod("ToggleHook")(hook.id, !hook.enabled);
      flashOk(
        hook.enabled ? t("hooks.flash.disabled") : t("hooks.flash.enabled"),
      );
      await reload(hook.id);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setBusy(true);
    try {
      await getBridgeMethod("DeleteHook")(target.id);
      flashOk(t("hooks.flash.deleted", { name: target.name }));
      await reload(null);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const run = async () => {
    if (selectedId == null) return;
    setRunning(true);
    try {
      const result = await getBridgeMethod("RunHook")({
        id: selectedId,
        dryRun: true,
      });
      setRunResult(result);
      if (!runOk(result))
        flashErr(
          t("hooks.flash.runFailed", {
            error:
              result.parseError || result.stderr || `exit ${result.exitCode}`,
          }),
        );
    } catch (err) {
      flashErr(t("hooks.flash.runFailed", { error: String(err) }));
    } finally {
      setRunning(false);
    }
  };

  const filtered = hooks.filter((h) => hookMatchesQuery(h, query));
  const selectedHook = hooks.find((h) => h.id === selectedId) ?? null;
  const headerMeta = draft ? interpMeta(draft.interpreter) : null;

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
      {/* Left list */}
      <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-sidebar">
        <div className="flex flex-col gap-2.5 border-b border-border p-3.5">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-foreground">
              {t("hooks.title")}
            </span>
            <span className="font-mono text-2xs text-muted-foreground">
              {hooks.length}
            </span>
            <span className="flex-1" />
            <Button
              type="button"
              size="icon"
              className="h-6 w-6"
              aria-label={t("hooks.list.addAria")}
              data-testid="hook-create"
              onClick={startCreate}
            >
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
          <div className="flex items-center gap-2 rounded-md border border-input bg-input-bg px-2.5">
            <Search className="h-3.5 w-3.5 text-muted-foreground" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("hooks.list.search")}
              aria-label={t("hooks.list.search")}
              className="h-8 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
            />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {filtered.length === 0 ? (
            <div className="flex flex-col items-center gap-1 px-3 py-10 text-center">
              <p className="text-xs text-muted-foreground">
                {t("hooks.list.empty")}
              </p>
              <p className="text-2xs text-muted-foreground/70">
                {t("hooks.list.emptyHint")}
              </p>
            </div>
          ) : (
            <div className="flex flex-col gap-0.5">
              <span className="px-1.5 pb-1 pt-2 text-3xs font-semibold uppercase text-muted-foreground">
                {t("hooks.list.groupScheduled")}
              </span>
              {filtered.map((hook) => (
                <HookListItem
                  key={hook.id}
                  hook={hook}
                  selected={hook.id === selectedId}
                  onSelect={() => selectHook(hook)}
                  t={t}
                />
              ))}
            </div>
          )}
        </div>
      </aside>

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
            {/* Header */}
            <div className="flex flex-col gap-2.5 border-b border-border px-7 py-4">
              <div className="flex items-center gap-3.5">
                {headerMeta ? (
                  <span
                    className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-white"
                    style={{
                      backgroundColor: `var(--color-${headerMeta.color})`,
                    }}
                  >
                    <headerMeta.icon className="h-[18px] w-[18px]" />
                  </span>
                ) : null}
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-lg font-semibold text-foreground">
                      {draft.name}
                    </span>
                    <span className="text-muted-foreground">·</span>
                    <span className="text-aux font-medium text-muted-foreground">
                      {t("hooks.header.kindLabel", {
                        interp: t(`hooks.interp.${draft.interpreter}`),
                      })}
                    </span>
                    {selectedHook ? (
                      <Badge variant="secondary" className="font-mono text-3xs">
                        {t(`hooks.status.${hookStatus(selectedHook)}`)}
                      </Badge>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-2 font-mono text-3xs text-muted-foreground">
                    <span>{draft.scheduleExpr}</span>
                    {selectedHook ? (
                      <>
                        <span>·</span>
                        <span>
                          {selectedHook.lastRunAt
                            ? t("hooks.header.lastRun", {
                                time: formatRelativeTime(
                                  selectedHook.lastRunAt,
                                  t,
                                ),
                              })
                            : t("hooks.header.neverRun")}
                        </span>
                        <span>·</span>
                        <span>
                          {t("hooks.header.totalEvents", {
                            count: selectedHook.totalCount,
                          })}
                        </span>
                      </>
                    ) : null}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    onClick={run}
                    disabled={running || selectedId == null}
                  >
                    {running ? (
                      <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <Play className="mr-1.5 h-3.5 w-3.5" />
                    )}
                    {t("hooks.header.run")}
                  </Button>
                  {selectedHook ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => toggle(selectedHook)}
                      disabled={busy}
                    >
                      {selectedHook.enabled ? (
                        <PowerOff className="mr-1.5 h-3.5 w-3.5" />
                      ) : (
                        <Power className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      {selectedHook.enabled
                        ? t("hooks.header.disable")
                        : t("hooks.header.enable")}
                    </Button>
                  ) : null}
                  {selectedHook ? (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="h-8 w-8"
                          aria-label={t("hooks.header.more")}
                        >
                          <MoreHorizontal className="h-4 w-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          className="text-status-error"
                          onClick={() => setDeleteTarget(selectedHook)}
                        >
                          <Trash2 className="mr-2 h-3.5 w-3.5" />
                          {t("hooks.header.delete")}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  ) : null}
                </div>
              </div>
            </div>

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
