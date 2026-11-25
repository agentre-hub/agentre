import * as React from "react";
import {
  ArrowLeft,
  ExternalLink,
  Maximize2,
  Minimize2,
  RotateCw,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { Button, Input, cn } from "@agentre-hub/agentre-ui";
import { openSettings } from "@/stores/settings-dialog-store";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import { ProbeSessionWebPreview } from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";
import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";

import {
  currentUrl,
  selectActiveTab,
  useWebPreviewStore,
  type PreviewTab,
} from "./web-preview-store";
import { ToolWindowTitleBarSlot } from "./tool-window-frame";

/**
 * iframe 挂上后多久没触发 load 就判为打不开。WKWebView 里连接被拒既不触发 load 也不触发
 * error（T1 探针），所以只能靠计时。
 */
export const WEB_PREVIEW_LOAD_TIMEOUT_MS = 8000;

type Props = {
  sessionId: number;
  /** 会话在远端设备上执行。 */
  remote: boolean;
  /** 执行设备名；本机会话为空。 */
  deviceName: string;
  /**
   * 立刻重取会话的进程与端口（端口标签的来源）。「重新加载」一个端口已不再监听的标签
   * 时调用：不等下一次轮询，端口回来时标签随之恢复并重新预检。
   */
  onRefreshPorts?: () => void;
};

type ProbeResult =
  | { kind: "probe"; probe: app.WebPreviewProbe }
  | { kind: "error"; message: string };

/**
 * 对当前地址做一次内嵌预检（Go 侧解析会话地址并探响应头）。结果连同它对应的请求键一起
 * 存：键变了（换标签 / 换地址 / 刷新）旧结果自然作废，不需要在 effect 里同步重置状态。
 */
function useProbe(
  sessionId: number,
  url: string | null,
  requestKey: string,
): ProbeResult | "loading" | null {
  const [settled, setSettled] = React.useState<{
    key: string;
    result: ProbeResult;
  } | null>(null);

  React.useEffect(() => {
    if (url === null) return;
    let cancelled = false;
    ProbeSessionWebPreview(sessionId, url).then(
      (probe) => {
        if (!cancelled)
          setSettled({ key: requestKey, result: { kind: "probe", probe } });
      },
      (err: unknown) => {
        if (cancelled) return;
        setSettled({
          key: requestKey,
          result: {
            kind: "error",
            message: err instanceof Error ? err.message : String(err),
          },
        });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [sessionId, url, requestKey]);

  if (url === null) return null;
  return settled?.key === requestKey ? settled.result : "loading";
}

/**
 * 端口转发这一层报的失败种类（portforwardhost.FailureKind 的 token）→ 原因里的那句细节。
 * Go 只交种类、不出给人看的句子；没有专门文案的种类原样显示 token。
 */
function forwardFailureDetail(t: TFunction, kind: string): string {
  switch (kind) {
    case "tls-verification":
      return t("webPreviewToolWindow.forwardFailures.tlsVerification");
    case "no-listener":
      return t("webPreviewToolWindow.forwardFailures.noListener");
    case "name-resolution":
      return t("webPreviewToolWindow.forwardFailures.nameResolution");
    case "device-unreachable":
      return t("webPreviewToolWindow.forwardFailures.deviceUnreachable");
    default:
      return kind;
  }
}

/** 预检结论 → 文案。key 写成静态字面量，i18n 覆盖守卫才数得到它们。 */
function reasonMessage(t: TFunction, reason: string, detail: string): string {
  switch (reason) {
    case "frameBlocked":
      return t("webPreviewToolWindow.reasons.frameBlocked", { detail });
    case "certificate":
      return t("webPreviewToolWindow.reasons.certificate", { detail });
    case "forwardFailed":
      return t("webPreviewToolWindow.reasons.forwardFailed", { detail });
    default:
      return t("webPreviewToolWindow.reasons.unreachable", { detail });
  }
}

const NO_DRAFT = { for: "", text: "" };

const IFRAME_SANDBOX = [
  "allow-scripts",
  "allow-same-origin",
  "allow-forms",
  "allow-modals",
  "allow-popups",
  "allow-popups-to-escape-sandbox",
  "allow-downloads",
].join(" ");

function isHttpUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

function tabLabel(tab: PreviewTab): string {
  if (tab.port !== null) {
    return tab.command ? `:${tab.port} ${tab.command}` : `:${tab.port}`;
  }
  try {
    return new URL(currentUrl(tab)).host;
  } catch {
    return currentUrl(tab);
  }
}

/**
 * 「网页预览」工具窗（spec「网页预览工具窗」）：会话监听的端口自动成标签，地址栏可输入
 * 任意 http(s) 地址。地址经 Go 侧预检后才挂 iframe——父页面看不到 X-Frame-Options /
 * frame-ancestors 拦截与证书失败，只能由预检给出原因。
 */
export function WebPreviewToolWindow({
  sessionId,
  remote,
  deviceName,
  onRefreshPorts,
}: Props) {
  const { t } = useTranslation();
  const session = useWebPreviewStore((s) => s.bySession[sessionId]);
  const actions = useWebPreviewStore.getState;
  const maximized = useToolWindowLayout((s) => s.maximized === "webPreview");
  const toggleMaximize = useToolWindowLayout((s) => s.toggleMaximize);
  const tab = selectActiveTab(session);

  const url = tab ? currentUrl(tab) : null;
  const gone = tab !== null && tab.port !== null && !tab.listening;
  const requestKey = tab
    ? `${sessionId}|${tab.key}|${url}|${tab.reloadSeq}`
    : "";
  const result = useProbe(sessionId, gone ? null : url, requestKey);

  // 8 秒没 load：记下是哪一次请求，键变了自动作废。
  const [noLoadKey, setNoLoadKey] = React.useState<string | null>(null);
  const noLoad = noLoadKey === requestKey;

  // 地址栏草稿只在输入框里有焦点时存在：提交或失焦即作废，回到显示当前标签的当前地址。
  const [draft, setDraft] = React.useState(NO_DRAFT);
  const [invalid, setInvalid] = React.useState(false);
  const draftKey = tab ? `${tab.key}|${tab.index}|${url}` : "";
  const addressText = draft.for === draftKey ? draft.text : (url ?? "");

  const probe =
    result && result !== "loading" && result.kind === "probe"
      ? result.probe
      : null;
  // 「在浏览器打开」要打开的地址：预检解析出的（远端会话是本机转发地址）；还没解析出时
  // 本机会话退回原地址，远端会话不退回——原地址打开的是桌面本机的同端口。
  const openTarget = probe?.url ?? (!remote && url !== null ? url : null);
  const forwarded = probe?.forwarded === true;

  const openInBrowser = () => {
    if (openTarget !== null) BrowserOpenURL(openTarget);
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!tab) return;
    const text = addressText.trim();
    if (!isHttpUrl(text)) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    setDraft(NO_DRAFT);
    actions().navigate(sessionId, tab.key, text);
  };

  const tabs = session?.tabs ?? [];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 标签在工具窗的标题栏里（spec「工具窗标题栏」）。 */}
      {tabs.length > 0 ? (
        <ToolWindowTitleBarSlot>
          <div
            role="tablist"
            aria-label={t("webPreviewToolWindow.label")}
            className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto px-1"
          >
            {tabs.map((tb) => (
              <div
                key={tb.key}
                className={cn(
                  "flex max-w-48 shrink-0 items-center rounded-md text-xs",
                  tb.key === tab?.key
                    ? "bg-rail-accent text-foreground"
                    : "text-muted-foreground hover:bg-rail-accent",
                )}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={tb.key === tab?.key}
                  title={tabLabel(tb)}
                  onClick={() => actions().activate(sessionId, tb.key)}
                  className="min-w-0 truncate rounded-md px-2 py-0.5 font-mono outline-none focus-visible:focus-outline-inset"
                >
                  {tabLabel(tb)}
                </button>
                {tb.port === null ? (
                  <button
                    type="button"
                    aria-label={t("webPreviewToolWindow.closeTab")}
                    title={t("webPreviewToolWindow.closeTab")}
                    onClick={() => actions().closeTab(sessionId, tb.key)}
                    className="mr-1 inline-flex size-4 items-center justify-center rounded outline-none hover:bg-accent focus-visible:focus-outline"
                  >
                    <X className="size-3" aria-hidden="true" />
                  </button>
                ) : null}
              </div>
            ))}
          </div>
        </ToolWindowTitleBarSlot>
      ) : null}

      {tab === null ? (
        <p className="px-4 py-6 text-center text-xs text-muted-foreground">
          {t("webPreviewToolWindow.empty")}
        </p>
      ) : (
        <>
          <form
            onSubmit={submit}
            className="flex shrink-0 items-center gap-1 px-2 pb-1"
          >
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t("webPreviewToolWindow.back")}
              title={t("webPreviewToolWindow.back")}
              disabled={tab.index === 0}
              onClick={() => actions().back(sessionId, tab.key)}
            >
              <ArrowLeft aria-hidden="true" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t("webPreviewToolWindow.refresh")}
              title={t("webPreviewToolWindow.refresh")}
              onClick={() => actions().reload(sessionId, tab.key)}
            >
              <RotateCw aria-hidden="true" />
            </Button>
            <Input
              value={addressText}
              onChange={(e) => {
                setInvalid(false);
                setDraft({ for: draftKey, text: e.target.value });
              }}
              onBlur={() => {
                setInvalid(false);
                setDraft(NO_DRAFT);
              }}
              aria-label={t("webPreviewToolWindow.address")}
              aria-invalid={invalid}
              spellCheck={false}
              className="h-6 min-w-0 flex-1 font-mono text-xs"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t("webPreviewToolWindow.openInBrowser")}
              title={t("webPreviewToolWindow.openInBrowser")}
              disabled={openTarget === null}
              onClick={openInBrowser}
            >
              <ExternalLink aria-hidden="true" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={
                maximized
                  ? t("webPreviewToolWindow.restore")
                  : t("webPreviewToolWindow.fill")
              }
              title={
                maximized
                  ? t("webPreviewToolWindow.restore")
                  : t("webPreviewToolWindow.fill")
              }
              onClick={() => toggleMaximize("webPreview")}
            >
              {maximized ? (
                <Minimize2 aria-hidden="true" />
              ) : (
                <Maximize2 aria-hidden="true" />
              )}
            </Button>
          </form>
          {invalid ? (
            <p className="px-3 pb-1 text-2xs text-destructive-text">
              {t("webPreviewToolWindow.invalidUrl")}
            </p>
          ) : null}
          {forwarded && deviceName ? (
            <p className="shrink-0 truncate px-3 pb-1 text-2xs text-muted-foreground">
              <span title={probe?.url}>
                {t("webPreviewToolWindow.forwarded", { device: deviceName })}
              </span>
            </p>
          ) : null}

          <div className="relative flex min-h-0 flex-1 flex-col">
            {gone ? (
              <Notice message={t("webPreviewToolWindow.portGone")}>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    actions().reload(sessionId, tab.key);
                    onRefreshPorts?.();
                  }}
                >
                  {t("webPreviewToolWindow.reload")}
                </Button>
              </Notice>
            ) : result === "loading" || result === null ? (
              <p className="px-4 py-6 text-center text-xs text-muted-foreground">
                {t("webPreviewToolWindow.loading")}
              </p>
            ) : result.kind === "error" ? (
              <Failure
                message={t("webPreviewToolWindow.reasons.unreachable", {
                  detail: result.message,
                })}
                onOpenInBrowser={openTarget === null ? null : openInBrowser}
              />
            ) : !result.probe.embeddable ? (
              <Failure
                message={reasonMessage(
                  t,
                  result.probe.reason,
                  result.probe.forwardFailure
                    ? forwardFailureDetail(t, result.probe.forwardFailure)
                    : result.probe.detail,
                )}
                onOpenInBrowser={openInBrowser}
                onOpenForwardSettings={
                  result.probe.forwarded &&
                  (result.probe.reason === "certificate" ||
                    result.probe.reason === "forwardFailed")
                    ? () => openSettings("remote-devices")
                    : null
                }
              />
            ) : noLoad ? (
              <Failure
                message={t("webPreviewToolWindow.reasons.noLoad")}
                onOpenInBrowser={openInBrowser}
              />
            ) : (
              <PreviewFrame
                key={requestKey}
                title={tabLabel(tab)}
                src={result.probe.url}
                onNoLoad={() => setNoLoadKey(requestKey)}
              />
            )}
          </div>
        </>
      )}
    </div>
  );
}

/** 挂 iframe 并计时：超时仍没 load 就上报。 */
function PreviewFrame({
  title,
  src,
  onNoLoad,
}: {
  title: string;
  src: string;
  onNoLoad: () => void;
}) {
  const loaded = React.useRef(false);
  // 计时只在挂载时起一次：父组件重渲染带来的新回调不该让它重新计时。
  const noLoadRef = React.useRef(onNoLoad);
  React.useEffect(() => {
    noLoadRef.current = onNoLoad;
  });
  React.useEffect(() => {
    const timer = setTimeout(() => {
      if (!loaded.current) noLoadRef.current();
    }, WEB_PREVIEW_LOAD_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, []);
  return (
    <iframe
      title={title}
      src={src}
      // 嵌进来的是任意 http(s) 页面：脚本、表单、弹窗、下载照常，但不给顶层导航——
      // 它不能把整个应用窗口导航到别处。
      sandbox={IFRAME_SANDBOX}
      onLoad={() => {
        loaded.current = true;
      }}
      // 被嵌页面没设背景色时按浏览器默认画布色显示（系统色，不是主题 token）。
      style={{ backgroundColor: "Canvas", colorScheme: "light" }}
      className="min-h-0 w-full flex-1 border-0"
    />
  );
}

function Notice({
  message,
  children,
}: {
  message: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-4 py-6 text-center text-xs text-muted-foreground">
      <p data-selectable-text="true">{message}</p>
      {children}
    </div>
  );
}

function Failure({
  message,
  onOpenInBrowser,
  onOpenForwardSettings = null,
}: {
  message: string;
  onOpenInBrowser: (() => void) | null;
  onOpenForwardSettings?: (() => void) | null;
}) {
  const { t } = useTranslation();
  return (
    <Notice message={message}>
      <div className="flex flex-wrap justify-center gap-2">
        {onOpenInBrowser ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={onOpenInBrowser}
          >
            <ExternalLink aria-hidden="true" />
            {t("webPreviewToolWindow.openInBrowser")}
          </Button>
        ) : null}
        {onOpenForwardSettings ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={onOpenForwardSettings}
          >
            {t("webPreviewToolWindow.openForwardSettings")}
          </Button>
        ) : null}
      </div>
    </Notice>
  );
}
