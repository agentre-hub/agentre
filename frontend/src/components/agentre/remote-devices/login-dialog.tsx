import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { cn } from "@/lib/utils";
import { copyTextWithToast } from "@agentre-ai/agentre-ui";

import { AgentreDialog } from "../app-dialog";
import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";
import { formatCountdown, friendlyLoginError } from "./format";

/** 官方 Hub 地址 —— 新用户 / 未记住自建地址时登录的默认目标。 */
export const DEFAULT_SERVER_URL = "https://app.agentrehub.com/";

type ServerMode = "official" | "custom";

// 归一化只去首尾空白与尾部斜杠,用于判断「这个 URL 是不是官方地址」。
function normalizeServerUrl(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

function isOfficialUrl(url: string): boolean {
  return normalizeServerUrl(url) === normalizeServerUrl(DEFAULT_SERVER_URL);
}

function resolveServerUrl(mode: ServerMode, url: string): string {
  return mode === "official" ? DEFAULT_SERVER_URL : url.trim();
}

// URL 记忆策略(用户已确认):默认官方;但上次连过自建服务器时,重开对话框记住并
// 选中「自定义」;若上次就是官方(或新装无记录)则默认官方。
function initialMode(initialUrl: string): { mode: ServerMode; url: string } {
  if (initialUrl && !isOfficialUrl(initialUrl)) {
    return { mode: "custom", url: initialUrl.trim() };
  }
  return { mode: "official", url: "" };
}

// 本地类型 — mirrors server_svc.StartLoginResult; avoids transitive
// wailsjs import (same precedent as AddRequest in device-pairing-form.tsx).
export type StartLoginResult = {
  DeviceCode: string;
  UserCode: string;
  VerificationURI: string;
  VerificationURIComplete: string;
  Interval: number;
  ExpiresIn: number;
};

type Phase = "form" | "starting" | "waiting";

// RFC 8628 device flow: Interval is the server-mandated poll cadence in
// seconds; a floor guards against a degenerate 0/negative value hammering
// the server.
const MIN_INTERVAL_MS = 1000;
const FALLBACK_INTERVAL_S = 5;

type Props = {
  open: boolean;
  /** Prefills the URL field from the last-used server_state row, if any. */
  initialUrl: string;
  onClose: () => void;
  /** Called once PollLoginToken reports completion, before onClose. */
  onLoggedIn: () => void;
  checkURL: (url: string) => Promise<string>;
  startLogin: (url: string) => Promise<StartLoginResult>;
  pollLoginToken: (deviceCode: string) => Promise<boolean>;
  cancelLogin: () => Promise<void>;
};

function ServerOption({
  value,
  mode,
  title,
  desc,
  badge,
  disabled,
  onSelect,
}: {
  value: ServerMode;
  mode: ServerMode;
  title: string;
  desc: string;
  badge?: string;
  disabled?: boolean;
  onSelect: (value: ServerMode) => void;
}) {
  const selected = mode === value;
  return (
    <label
      onClick={() => onSelect(value)}
      className={cn(
        "flex cursor-pointer items-center gap-3 rounded-lg border px-3.5 py-3.5",
        selected
          ? "border-primary bg-primary-soft"
          : "border-border bg-card hover:bg-muted",
        disabled && "pointer-events-none opacity-60",
      )}
    >
      <RadioGroupItem value={value} className="shrink-0" />
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-xs text-muted-foreground">{desc}</span>
      </span>
      {badge ? (
        <span className="ml-auto shrink-0 rounded-full bg-status-running-bg px-2 py-0.5 text-[11px] font-semibold text-status-running">
          {badge}
        </span>
      ) : null}
    </label>
  );
}

export function LoginDialog({
  open,
  initialUrl,
  onClose,
  onLoggedIn,
  checkURL,
  startLogin,
  pollLoginToken,
  cancelLogin,
}: Props) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<ServerMode>(
    () => initialMode(initialUrl).mode,
  );
  const [url, setUrl] = useState(() => initialMode(initialUrl).url);
  const [phase, setPhase] = useState<Phase>("form");
  const [login, setLogin] = useState<StartLoginResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [remaining, setRemaining] = useState(0);
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | null>(null);
  const deadlineRef = useRef<number | null>(null);
  // attemptRef 标识「当前这一次登录尝试」。清 interval 只挡得住**还没发出**的轮询;
  // 已经在飞的那一次 pollLoginToken 照样会 resolve/reject,它落定时必须先确认自己
  // 还属于当前这次尝试 —— 否则一次已经取消(且已发过 cancelLogin)的登录会凭一个
  // 迟到的 true 跑完 onLoggedIn(),或者把错误写进一个已经关掉的对话框。
  const attemptRef = useRef(0);

  const stopPolling = () => {
    attemptRef.current += 1;
    if (timerRef.current !== null) {
      window.clearInterval(timerRef.current);
      timerRef.current = null;
    }
    deadlineRef.current = null;
  };

  // Unmount safety net — every real close path already routes through
  // handleClose(), but guard against a leaked timer if the panel ever
  // unmounts the dialog outright instead of flipping `open`.
  useEffect(() => stopPolling, []);

  useEffect(() => {
    if (!open) {
      stopPolling();
      setPhase("form");
      setLogin(null);
      setError(null);
    } else {
      // 打开瞬间按当前 initialUrl 重算默认选择(URL 记忆)。
      const init = initialMode(initialUrl);
      setMode(init.mode);
      setUrl(init.url);
    }
    setRemaining(0);
    setCopied(false);
    // Re-sync only on the open/closed transition; initialUrl is read at that
    // moment rather than tracked continuously (same as TLSTrustDialog).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // 等待期每秒刷新倒计时(剩余秒数由 deadlineRef 反推,过期归零停住)。
  useEffect(() => {
    if (phase !== "waiting" || !login) return;
    const id = window.setInterval(() => {
      if (deadlineRef.current !== null) {
        setRemaining(
          Math.max(0, Math.round((deadlineRef.current - Date.now()) / 1000)),
        );
      }
    }, 1000);
    return () => window.clearInterval(id);
  }, [phase, login]);

  const poll = async (deviceCode: string, attempt: number) => {
    try {
      const done = await pollLoginToken(deviceCode);
      if (attempt !== attemptRef.current) return; // 取消 / 重开已经作废了这一次
      if (done) {
        stopPolling();
        onLoggedIn();
        onClose();
        return;
      }
      if (deadlineRef.current !== null && Date.now() >= deadlineRef.current) {
        stopPolling();
        setPhase("form");
        setError(t("remoteDevices.login.errors.expired"));
      }
    } catch (e: unknown) {
      if (attempt !== attemptRef.current) return;
      stopPolling();
      setPhase("form");
      setError(friendlyLoginError(e, t));
    }
  };

  const start = async () => {
    stopPolling(); // 作废上一次尝试还在飞的轮询,再开新的一次
    // 这一次尝试的身份**在起点就定下**,不能等两次 await 回来再读:stopPolling
    // (卸载清理 / 关闭 / 再点一次)只清得掉**已经存在**的 timer,清不掉一次还在
    // 飞的 start。若等 await 之后才读 attemptRef,读到的正是那次清理刚 +1 出来的
    // 新值,身份校验于是放行,而这个 interval 已经没有任何人能清 —— 轮询会一直
    // 跑到进程结束,一个迟到的 true 还会把 onLoggedIn/onClose 打进已经卸载的父组件。
    const attempt = attemptRef.current;
    const stale = () => attempt !== attemptRef.current;
    setError(null);
    setPhase("starting");
    const target = resolveServerUrl(mode, url);
    try {
      await checkURL(target);
    } catch (e: unknown) {
      if (stale()) return;
      setPhase("form");
      setError(friendlyLoginError(e, t));
      return;
    }
    if (stale()) return;
    try {
      const res = await startLogin(target);
      if (stale()) return;
      setLogin(res);
      setPhase("waiting");
      setRemaining(Math.max(0, res.ExpiresIn || 0));
      deadlineRef.current = Date.now() + Math.max(0, res.ExpiresIn || 0) * 1000;
      const intervalMs =
        Math.max(1, res.Interval || FALLBACK_INTERVAL_S) * 1000;
      timerRef.current = window.setInterval(
        () => void poll(res.DeviceCode, attempt),
        Math.max(MIN_INTERVAL_MS, intervalMs),
      );
    } catch (e: unknown) {
      if (stale()) return;
      setPhase("form");
      setError(friendlyLoginError(e, t));
    }
  };

  const copyCode = async () => {
    if (!login) return;
    const ok = await copyTextWithToast(login.UserCode, {
      successTitle: t("remoteDevices.login.actions.copied"),
    });
    if (ok) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    }
  };

  const handleClose = () => {
    if (phase === "starting") return; // mid-flight — same guard as the pairing form's submitting.
    const wasWaiting = phase === "waiting";
    stopPolling();
    setPhase("form");
    setLogin(null);
    setError(null);
    if (wasWaiting) {
      void cancelLogin().catch(() => {});
    }
    onClose();
  };

  const canSubmit =
    (mode === "official" || url.trim().length > 0) && phase !== "starting";

  return (
    <AgentreDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) handleClose();
      }}
      title={t("remoteDevices.login.title")}
      description={t("remoteDevices.login.description")}
      contentClassName="sm:max-w-[480px]"
      bodyClassName="flex flex-col gap-3.5"
      footer={
        <>
          <Button
            variant="ghost"
            onClick={handleClose}
            disabled={phase === "starting"}
          >
            {t("common.cancel")}
          </Button>
          {phase !== "waiting" ? (
            <Button onClick={start} disabled={!canSubmit}>
              {phase === "starting"
                ? t("remoteDevices.login.actions.connecting")
                : t("remoteDevices.login.actions.continue")}
            </Button>
          ) : null}
        </>
      }
    >
      {phase === "waiting" && login ? (
        <div className="flex flex-col gap-3">
          <div className="flex flex-col items-center gap-2 rounded-md bg-secondary/50 py-4">
            <span className="text-xs text-muted-foreground">
              {t("remoteDevices.login.waiting.userCode")}
            </span>
            <div className="flex items-center gap-2">
              <span className="font-mono text-2xl font-semibold tracking-widest">
                {login.UserCode}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={copyCode}
                className="gap-1 text-muted-foreground"
              >
                {copied ? (
                  <Check className="h-3.5 w-3.5 text-status-running" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
                {copied
                  ? t("remoteDevices.login.actions.copied")
                  : t("remoteDevices.login.actions.copyCode")}
              </Button>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-muted-foreground">
              {t("remoteDevices.login.waiting.verificationUrl")}
            </span>
            <code className="break-all text-xs">{login.VerificationURI}</code>
          </div>
          <Button
            type="button"
            variant="secondary"
            className="w-full"
            onClick={() =>
              BrowserOpenURL(
                login.VerificationURIComplete || login.VerificationURI,
              )
            }
          >
            {t("remoteDevices.login.actions.openBrowser")}
          </Button>
          <p className="text-center text-xs text-muted-foreground">
            {t("remoteDevices.login.waiting.expiresIn", {
              time: formatCountdown(remaining),
            })}
          </p>
          <p className="text-center text-xs text-muted-foreground">
            {t("remoteDevices.login.waiting.autoClose")}
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <RadioGroup
            value={mode}
            onValueChange={(v) => setMode(v as ServerMode)}
            className="gap-2"
          >
            <ServerOption
              value="official"
              mode={mode}
              title={t("remoteDevices.login.servers.official")}
              desc={t("remoteDevices.login.servers.officialDesc")}
              badge={t("remoteDevices.login.servers.recommended")}
              disabled={phase === "starting"}
              onSelect={setMode}
            />
            <ServerOption
              value="custom"
              mode={mode}
              title={t("remoteDevices.login.servers.custom")}
              desc={t("remoteDevices.login.servers.customDesc")}
              disabled={phase === "starting"}
              onSelect={setMode}
            />
          </RadioGroup>
          {mode === "custom" ? (
            <label className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">
                {t("remoteDevices.login.fields.url")}
              </span>
              <Input
                value={url}
                onChange={(e) => setUrl(e.target.value.trim())}
                placeholder="https://hub.example.com"
                disabled={phase === "starting"}
              />
            </label>
          ) : null}
        </div>
      )}

      {error ? <div className="text-sm text-destructive">{error}</div> : null}
    </AgentreDialog>
  );
}
