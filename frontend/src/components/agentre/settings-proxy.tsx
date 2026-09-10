import * as React from "react";
import type { TFunction } from "i18next";
import {
  AlertCircle,
  CheckCircle2,
  Copy,
  Info,
  Loader2,
  RotateCcw,
  ShieldCheck,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Alert,
  AlertDescription,
  AlertTitle,
  Badge,
  Button,
  Input,
  copyTextWithToast,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  GetAppSetting,
  GetGatewayStatus,
  RestartGateway,
  UpdateAppSettings,
} from "../../../wailsjs/go/app/App";
import { app_settings_svc, httpgateway } from "../../../wailsjs/go/models";

type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

const KEY_HOST = "proxy.listen_host";
const KEY_PORT = "proxy.listen_port";

type RouteRow = {
  method: string;
  path: string;
  descriptionKey: string;
  badgeKey?: string;
};

const ROUTE_ROWS: RouteRow[] = [
  {
    method: "POST",
    path: "/v1/messages",
    descriptionKey: "settingsProxy.routes.anthropicMessages",
  },
  {
    method: "POST",
    path: "/v1/responses",
    descriptionKey: "settingsProxy.routes.openAIResponses",
  },
  {
    method: "POST",
    path: "/v1/chat/completions",
    descriptionKey: "settingsProxy.routes.chatCompletions",
  },
  {
    method: "ALL",
    path: "/mcp/*",
    descriptionKey: "settingsProxy.routes.mcpReserved",
    badgeKey: "settingsProxy.routes.reserved",
  },
];

function validateHost(v: string, t: TFunction): string | null {
  const s = v.trim();
  if (s === "") return t("settingsProxy.validation.hostRequired");
  // 简单 IPv4 / 主机名校验：交由后端二次校验
  if (!/^[0-9a-zA-Z.\-:]+$/.test(s)) {
    return t("settingsProxy.validation.hostInvalid");
  }
  return null;
}

function validatePort(v: string, t: TFunction): string | null {
  const s = v.trim();
  if (s === "") return t("settingsProxy.validation.portRequired");
  if (!/^\d+$/.test(s)) return t("settingsProxy.validation.portNumeric");
  const n = Number(s);
  if (n < 0 || n > 65535) {
    return t("settingsProxy.validation.portRange");
  }
  return null;
}

export function SettingsProxyPanel() {
  const { t } = useTranslation();
  const [host, setHost] = React.useState("");
  const [port, setPort] = React.useState("");
  const [savedHost, setSavedHost] = React.useState("");
  const [savedPort, setSavedPort] = React.useState("");
  const [status, setStatus] = React.useState<httpgateway.GatewayStatus | null>(
    null,
  );
  const [loading, setLoading] = React.useState(true);
  const [applying, setApplying] = React.useState(false);
  const [restarting, setRestarting] = React.useState(false);
  const [flash, setFlash] = React.useState<FlashState>(null);

  React.useEffect(() => {
    let mounted = true;
    Promise.all([
      GetAppSetting({ key: KEY_HOST }),
      GetAppSetting({ key: KEY_PORT }),
      GetGatewayStatus(),
    ])
      .then(([h, p, s]) => {
        if (!mounted) return;
        setHost(h?.value ?? "");
        setPort(p?.value ?? "");
        setSavedHost(h?.value ?? "");
        setSavedPort(p?.value ?? "");
        setStatus(s ?? null);
      })
      .catch((err: unknown) => {
        if (!mounted) return;
        setFlash({ kind: "err", text: messageFromError(err, t) });
      })
      .finally(() => {
        if (!mounted) return;
        setLoading(false);
      });
    return () => {
      mounted = false;
    };
  }, [t]);

  const hostErr = validateHost(host, t);
  const portErr = validatePort(port, t);
  const dirty = host !== savedHost || port !== savedPort;
  const canApply = !applying && !restarting && dirty && !hostErr && !portErr;

  async function handleApply() {
    if (!canApply) return;
    setApplying(true);
    setFlash(null);
    try {
      await UpdateAppSettings({
        entries: [
          { key: KEY_HOST, value: host.trim() },
          { key: KEY_PORT, value: port.trim() },
        ],
      } as app_settings_svc.UpdateRequest);
      const s = await GetGatewayStatus();
      setStatus(s ?? null);
      setSavedHost(host.trim());
      setSavedPort(port.trim());
      if (s?.status === "running") {
        setFlash({ kind: "ok", text: t("settingsProxy.flash.applied") });
      } else {
        setFlash({
          kind: "err",
          text: t("settingsProxy.flash.savedButStopped", {
            reason: s?.reason || t("common.errorOccurred"),
          }),
        });
      }
    } catch (err) {
      setFlash({ kind: "err", text: messageFromError(err, t) });
    } finally {
      setApplying(false);
    }
  }

  async function handleRestart() {
    if (restarting || applying) return;
    setRestarting(true);
    setFlash(null);
    try {
      const res = await RestartGateway();
      setStatus(res?.status ?? null);
      if (res?.status?.status === "running") {
        setFlash({ kind: "ok", text: t("settingsProxy.flash.restarted") });
      } else {
        setFlash({
          kind: "err",
          text: t("settingsProxy.flash.restartFailed", {
            reason: res?.status?.reason || t("common.errorOccurred"),
          }),
        });
      }
    } catch (err) {
      setFlash({ kind: "err", text: messageFromError(err, t) });
    } finally {
      setRestarting(false);
    }
  }

  async function handleCopy() {
    if (!status?.listenURL) return;
    await copyTextWithToast(status.listenURL, {
      errorTitle: t("settingsProxy.flash.copyUrlFailed"),
      successTitle: t("settingsProxy.flash.copyUrlDone"),
    });
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {flash ? (
        <FlashBanner state={flash} onDismiss={() => setFlash(null)} />
      ) : null}

      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
          <div className="flex min-w-0 flex-col gap-0.5">
            <h2 className="text-sm font-semibold">
              {t("settingsProxy.listener.title")}
            </h2>
            <p className="text-2xs leading-relaxed text-muted-foreground">
              {t("settingsProxy.listener.description")}
            </p>
          </div>
          <StatusPill status={status} loading={loading} />
        </header>

        <div className="grid grid-cols-1 gap-3 px-4 py-4 sm:grid-cols-3">
          <FormField
            label={t("settingsProxy.listener.host")}
            hint={t("settingsProxy.listener.hostHint")}
            error={dirty ? hostErr : null}
          >
            <Input
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="127.0.0.1"
              className="font-mono"
              disabled={loading}
            />
          </FormField>
          <FormField
            label={t("settingsProxy.listener.port")}
            hint={t("settingsProxy.listener.portHint")}
            error={dirty ? portErr : null}
          >
            <Input
              value={port}
              onChange={(e) => setPort(e.target.value)}
              placeholder="52401"
              className="font-mono"
              disabled={loading}
              inputMode="numeric"
            />
          </FormField>
          <FormField
            label={t("settingsProxy.listener.currentUrl")}
            hint={t("settingsProxy.listener.currentUrlHint")}
          >
            <UrlReadout status={status} loading={loading} />
          </FormField>
        </div>

        <footer className="flex flex-wrap items-center justify-end gap-2 border-t border-border bg-secondary/30 px-4 py-3">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleCopy}
            disabled={!status?.listenURL || loading}
            className="h-8 gap-1.5 px-3 text-xs"
          >
            <Copy className="size-3.5" aria-hidden="true" />
            {t("settingsProxy.actions.copyUrl")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleRestart}
            disabled={loading || restarting || applying}
            className="h-8 gap-1.5 px-3 text-xs"
          >
            {restarting ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : (
              <RotateCcw className="size-3.5" aria-hidden="true" />
            )}
            {t("settingsProxy.actions.restart")}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={handleApply}
            disabled={!canApply}
            className="h-8 gap-1.5 px-3 text-xs"
          >
            {applying ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {t("settingsProxy.actions.applyAndRestart")}
          </Button>
        </footer>
      </section>

      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
          <div className="flex min-w-0 flex-col gap-0.5">
            <h2 className="text-sm font-semibold">
              {t("settingsProxy.routes.title")}
            </h2>
            <p className="text-2xs leading-relaxed text-muted-foreground">
              {t("settingsProxy.routes.description")}
            </p>
          </div>
          <Badge
            variant="secondary"
            className="rounded-sm px-1.5 py-0 font-mono text-2xs"
          >
            {t("settingsProxy.routes.count", {
              count: status?.routes?.length ?? 0,
            })}
          </Badge>
        </header>
        <ul className="divide-y divide-border">
          {ROUTE_ROWS.map((r) => (
            <li
              key={r.path}
              className="flex items-center gap-3 px-4 py-2.5 text-xs"
            >
              <Badge
                variant="secondary"
                className="w-[44px] shrink-0 justify-center rounded-sm px-1 py-0 font-mono text-2xs"
              >
                {r.method}
              </Badge>
              <span
                data-selectable-text="true"
                className="w-[180px] shrink-0 truncate font-mono text-xs"
              >
                {r.path}
              </span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">
                {t(r.descriptionKey)}
              </span>
              {r.badgeKey ? (
                <Badge
                  variant="secondary"
                  className="rounded-sm px-1.5 py-0 font-mono text-2xs"
                >
                  {t(r.badgeKey)}
                </Badge>
              ) : null}
            </li>
          ))}
        </ul>
      </section>

      <Alert className="border-primary-text/30 bg-primary-soft text-primary-text">
        <ShieldCheck className="size-4" aria-hidden="true" />
        <AlertTitle className="text-xs font-semibold">
          {t("settingsProxy.security.title")}
        </AlertTitle>
        <AlertDescription className="text-2xs leading-relaxed">
          {t("settingsProxy.security.description")}
        </AlertDescription>
      </Alert>
    </div>
  );
}

function StatusPill({
  status,
  loading,
}: {
  status: httpgateway.GatewayStatus | null;
  loading: boolean;
}) {
  const { t } = useTranslation();
  if (loading) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-sm bg-secondary px-2 py-0.5 font-mono text-2xs text-muted-foreground">
        <Loader2 className="size-3 animate-spin" aria-hidden="true" />
        {t("common.loading")}
      </span>
    );
  }
  const running = status?.status === "running";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-sm px-2 py-0.5 font-mono text-2xs",
        running
          ? "bg-status-running-bg text-status-running"
          : "bg-destructive-soft text-destructive",
      )}
    >
      <span
        className={cn(
          "size-1.5 rounded-full",
          running ? "bg-status-running" : "bg-destructive",
        )}
        aria-hidden="true"
      />
      {running
        ? t("settingsProxy.status.running")
        : t("settingsProxy.status.stopped")}
    </span>
  );
}

function FormField({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string | null;
  children: React.ReactNode;
}) {
  return (
    <label className="flex min-w-0 flex-col gap-1.5 text-xs">
      <span className="font-medium">{label}</span>
      {children}
      {error ? (
        <span className="text-2xs text-destructive">{error}</span>
      ) : hint ? (
        <span className="font-mono text-2xs text-muted-foreground">{hint}</span>
      ) : null}
    </label>
  );
}

function UrlReadout({
  status,
  loading,
}: {
  status: httpgateway.GatewayStatus | null;
  loading: boolean;
}) {
  const { t } = useTranslation();
  if (loading) {
    return (
      <div className="flex h-9 items-center rounded-md border border-border bg-secondary/40 px-3 font-mono text-2xs text-muted-foreground">
        {t("common.loading")}
      </div>
    );
  }
  if (status?.status === "running" && status.listenURL) {
    return (
      <div
        data-selectable-text="true"
        className="flex h-9 items-center truncate rounded-md border border-border bg-secondary/40 px-3 font-mono text-2xs"
      >
        {status.listenURL}
      </div>
    );
  }
  return (
    <div className="flex h-9 items-center truncate rounded-md border border-destructive/40 bg-destructive-soft px-3 font-mono text-2xs text-destructive">
      {t("settingsProxy.status.notStarted", {
        reason: status?.reason ? ` · ${status.reason}` : "",
      })}
    </div>
  );
}

function FlashBanner({
  state,
  onDismiss,
}: {
  state: Exclude<FlashState, null>;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  const ok = state.kind === "ok";
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-xs",
        ok
          ? "border-status-running/40 bg-status-running-bg text-status-running"
          : "border-destructive/40 bg-destructive-soft text-destructive",
      )}
      role="status"
    >
      {ok ? (
        <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      ) : (
        <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      )}
      <span className="min-w-0 flex-1 break-words">{state.text}</span>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={onDismiss}
        aria-label={t("chatPanel.notice.close")}
      >
        <Info data-icon="only" aria-hidden="true" />
      </Button>
    </div>
  );
}

function messageFromError(err: unknown, t: TFunction): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  try {
    return JSON.stringify(err);
  } catch {
    return t("common.errorOccurred");
  }
}
