// 徽章与结果条：类型选择器及其徽标 / 只读形态、生效配置摘要、测试结果条与顶部 flash。
// 全部只吃 props，不碰端口，可在测试里单独渲染。
import * as React from "react";
import { AlertCircle, CheckCircle2, ChevronDown, Loader2 } from "lucide-react";

import { useUiTranslation as useTranslation } from "../i18n";
import { Button } from "../ui/button";
import { cn } from "../lib/utils";

import {
  truncateFlashText,
  type ResolvedModelTarget,
} from "./agent-backends-utils";
import {
  CLAUDE_TIERS,
  isCliBackend,
  routeConclusion,
  type BackendType,
  type CLIProbe,
  type ClaudeTier,
  type FlashState,
  type RouteTarget,
} from "./agent-backends-shared";
import { AgentBackendLogo } from "./ai-brand-logo";
import type { PickerProvider } from "./model-target-picker";

// 选择器里的展示顺序：三个 CLI 引擎在前（最常用且需要装命令行），内置与网关收尾。
// openclaw 排最后是因为它在两列网格里独占整行，避免出现空格子。
const BACKEND_TYPE_ORDER: BackendType[] = [
  "claudecode",
  "codex",
  "piagent",
  "builtin",
  "openclaw",
];

const backendTypeMeta: Record<
  BackendType,
  {
    disabled: boolean;
  }
> = {
  builtin: {
    disabled: false,
  },
  claudecode: {
    disabled: false,
  },
  codex: {
    disabled: false,
  },
  piagent: {
    disabled: false,
  },
  openclaw: {
    disabled: false,
  },
};

// 类型选择器：两列卡片，每张只放「logo + 名称 + 一个徽标」。
// 不放整句说明 —— 徽标只承载「选之前才有用、选之后就看不到」的事实
// （CLI 装没装 / 内置只能跑本机），其余前置条件选中后表单自己会呈现。
export function BackendTypePicker({
  value,
  onChange,
  probes,
  canCreateBuiltin,
}: {
  value: BackendType;
  onChange: (v: BackendType) => void;
  probes: Partial<Record<BackendType, CLIProbe>>;
  canCreateBuiltin: boolean;
}) {
  const { t } = useTranslation();
  const groupRef = React.useRef<HTMLDivElement>(null);
  const backendTypes = canCreateBuiltin
    ? BACKEND_TYPE_ORDER
    : BACKEND_TYPE_ORDER.filter((backendType) => backendType !== "builtin");

  // radiogroup 的键盘契约：方向键换选项并把焦点带过去（Tab 只进出整组）。
  function moveSelection(delta: number) {
    const from = backendTypes.indexOf(value);
    const total = backendTypes.length;
    const next = backendTypes[(from + delta + total) % total];
    onChange(next);
    groupRef.current
      ?.querySelector<HTMLButtonElement>(`[data-backend-type="${next}"]`)
      ?.focus();
  }

  return (
    <div
      ref={groupRef}
      role="radiogroup"
      aria-label={t("agentBackends.fields.type")}
      // 对话框是 w-full max-w-xl —— 窗口窄于 sm 时它跟着缩，两列会把
      // 「Claude Code CLI + 未安装」挤到截断，所以窄窗退回单列。
      className="grid grid-cols-1 gap-1.5 sm:grid-cols-2"
      onKeyDown={(e) => {
        if (e.key === "ArrowDown" || e.key === "ArrowRight") {
          e.preventDefault();
          moveSelection(1);
        } else if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
          e.preventDefault();
          moveSelection(-1);
        }
      }}
    >
      {backendTypes.map((backendType) => {
        const checked = value === backendType;
        return (
          <button
            key={backendType}
            type="button"
            role="radio"
            aria-checked={checked}
            data-backend-type={backendType}
            disabled={backendTypeMeta[backendType].disabled}
            // roving tabindex：整组只有选中项可 Tab 聚焦。
            tabIndex={checked ? 0 : -1}
            onClick={() => onChange(backendType)}
            className={cn(
              "flex min-w-0 items-center gap-2 rounded-md border px-2.5 py-2 text-left outline-none transition-colors",
              "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
              "disabled:pointer-events-none disabled:opacity-50",
              checked
                ? "border-primary bg-primary-soft"
                : "border-border bg-card hover:border-border-strong hover:bg-accent/60",
              // 5 个选项排两列会空一格,让网关独占末行收尾。单列时不能加 span-2:
              // 那会在 1 列的网格里撑出一条隐式列,把整行顶出容器。
              backendType === "openclaw" && "sm:col-span-2",
            )}
          >
            <span aria-hidden="true">
              <AgentBackendLogo backendType={backendType} className="size-4" />
            </span>
            <span className="min-w-0 flex-1 truncate text-xs font-semibold">
              {t(`agentBackends.backendType.${backendType}.label`)}
            </span>
            <BackendTypeBadge type={backendType} probe={probes[backendType]} />
          </button>
        );
      })}
    </div>
  );
}

function BackendTypeBadge({
  type,
  probe,
}: {
  type: BackendType;
  probe?: CLIProbe;
}) {
  const { t } = useTranslation();
  // 内置引擎的意外之处是「运行设备」会被禁用 —— 提前说，省得用户选完才发现派不出去。
  if (type === "builtin") {
    return (
      <span className="shrink-0 rounded-full border border-border bg-secondary px-1.5 text-2xs text-muted-foreground">
        {t("agentBackends.backendType.builtin.badge")}
      </span>
    );
  }
  // 网关不给徽标：它要填什么，选中之后表单自己会说。
  if (!isCliBackend(type) || !probe) return null;

  const tone =
    probe.state === "installed"
      ? "border-status-running/30 bg-status-running-bg text-status-running"
      : probe.state === "probing"
        ? "border-border bg-secondary text-muted-foreground"
        : "border-status-waiting/30 bg-status-waiting-bg text-status-waiting";
  return (
    <span
      // e2e 用它断言探测结论，避免拿 i18n 文案当定位符。
      data-probe-state={probe.state}
      className={cn(
        "flex shrink-0 items-center gap-1 rounded-full border px-1.5 text-2xs",
        tone,
      )}
      title={probe.state === "installed" ? probe.path : undefined}
    >
      {probe.state === "probing" ? (
        <Loader2 className="size-2.5 animate-spin" aria-hidden="true" />
      ) : null}
      {t(`agentBackends.backendType.probe.${probe.state}`)}
    </span>
  );
}

// 编辑态：类型创建后不可改。渲染整组再 disabled 只是白占地方，换成一行只读摘要。
export function BackendTypeReadonly({ type }: { type: BackendType }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-baseline gap-2">
        <span className="font-medium">{t("agentBackends.fields.type")}</span>
        <span className="text-2xs text-muted-foreground">
          {t("agentBackends.fields.typeLocked")}
        </span>
      </div>
      <div className="flex items-center gap-2 rounded-md border border-border bg-secondary px-2.5 py-2">
        <span aria-hidden="true">
          <AgentBackendLogo backendType={type} className="size-4" />
        </span>
        <span className="min-w-0 flex-1 truncate text-xs font-semibold">
          {t(`agentBackends.backendType.${type}.label`)}
        </span>
      </div>
    </div>
  );
}

export function EffectiveConfigSummary({
  type,
  deviceName,
  cliPath,
  resolvedMainTarget,
  customModel,
  routes,
  catalog,
  referenceCount,
  saveBlockedReason,
  openClawModel,
}: {
  type: BackendType;
  deviceName: string;
  cliPath: string;
  resolvedMainTarget: ResolvedModelTarget;
  customModel: string;
  routes: Record<ClaudeTier, RouteTarget>;
  catalog: PickerProvider[];
  referenceCount: number;
  saveBlockedReason: string | null;
  openClawModel: string;
}) {
  const { t } = useTranslation();
  const source =
    type === "openclaw"
      ? t("agentBackends.summary.sources.openclaw")
      : resolvedMainTarget.mode === "native"
        ? t("agentBackends.summary.sources.cli")
        : t("agentBackends.summary.sources.agentre", {
            provider: resolvedMainTarget.providerName,
          });
  const effectiveModel =
    type === "openclaw"
      ? openClawModel || t("agentBackends.openclaw.modelGatewayDefault")
      : resolvedMainTarget.mode === "native"
        ? customModel || t("agentBackends.summary.cliAccountModel")
        : resolvedMainTarget.modelId ||
          t("agentBackends.summary.unresolvedModel");
  const mode =
    type === "openclaw"
      ? t("agentBackends.summary.modeGateway")
      : resolvedMainTarget.mode === "provider-default"
        ? t("agentBackends.binding.modeFollow")
        : resolvedMainTarget.mode === "fixed"
          ? t("agentBackends.binding.modeFixed")
          : resolvedMainTarget.mode === "invalid"
            ? t("agentBackends.summary.modeInvalid")
            : t("agentBackends.summary.modeCli");
  return (
    <section
      data-testid="effective-config-summary"
      className={cn(
        "flex flex-col gap-2 rounded-lg border px-3 py-3 text-xs",
        saveBlockedReason
          ? "border-status-waiting/40 bg-status-waiting-bg"
          : "border-border bg-secondary/30",
      )}
    >
      <h3 className="font-semibold">{t("agentBackends.summary.title")}</h3>
      <dl className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-2 gap-y-1 text-2xs">
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.runtime")}
        </dt>
        {/* 运行位置要能回答「到底跑哪个可执行文件」，自定义 CLI 路径必须一起显示。 */}
        <dd data-testid="summary-runtime" className="truncate">
          {deviceName}
          {cliPath.trim() !== "" ? (
            <>
              <span
                className="mx-1 text-decorative-foreground"
                aria-hidden="true"
              >
                ·
              </span>
              <span className="font-mono">{cliPath.trim()}</span>
            </>
          ) : null}
        </dd>
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.source")}
        </dt>
        <dd className="truncate">{source}</dd>
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.model")}
        </dt>
        <dd className="truncate">
          {effectiveModel} · {mode}
        </dd>
        {type === "claudecode" ? (
          <>
            <dt className="text-muted-foreground">
              {t("agentBackends.summary.routes")}
            </dt>
            <dd className="min-w-0">
              {CLAUDE_TIERS.map((tier) => (
                <span key={tier} className="mr-2 inline-block">
                  {tier}:{" "}
                  {routeConclusion(
                    t,
                    routes[tier],
                    resolvedMainTarget,
                    catalog,
                  )}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.references")}
        </dt>
        <dd>
          {t("agentBackends.summary.referenceCount", { count: referenceCount })}
        </dd>
      </dl>
      {/* 校验结论独立成一整行:通过时给正向确认，不通过时把原因说完整。 */}
      {saveBlockedReason ? (
        <p className="flex items-center gap-1.5 rounded-md bg-status-waiting-bg px-2 py-1.5 text-2xs text-status-waiting">
          <AlertCircle className="size-3 shrink-0" aria-hidden="true" />
          {t("agentBackends.summary.cannotSaveReason", {
            reason: saveBlockedReason,
          })}
        </p>
      ) : (
        <p className="flex items-center gap-1.5 rounded-md bg-status-running-bg px-2 py-1.5 text-2xs font-medium text-status-running">
          <CheckCircle2 className="size-3 shrink-0" aria-hidden="true" />
          {t("agentBackends.summary.saveReady")}
        </p>
      )}
    </section>
  );
}

export function TestResultPill({ state }: { state: FlashState }) {
  if (!state) return null;
  const ok = state.kind === "ok";
  const { display, full, truncated } = truncateFlashText(state.text);
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
      <span
        className="min-w-0 flex-1 break-words"
        title={truncated ? full : undefined}
      >
        {display}
      </span>
    </div>
  );
}

export function FlashBanner({
  state,
  onDismiss,
}: {
  state: FlashState;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  if (!state) return null;
  const ok = state.kind === "ok";
  const { display, full, truncated } = truncateFlashText(state.text);
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-4 py-2 text-xs",
        ok
          ? "bg-status-running-bg text-status-running"
          : "bg-destructive-soft text-destructive",
      )}
      role="status"
    >
      {ok ? (
        <CheckCircle2 className="size-3.5" />
      ) : (
        <AlertCircle className="size-3.5" />
      )}
      <span
        className="min-w-0 flex-1 truncate"
        title={truncated ? full : undefined}
      >
        {display}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={onDismiss}
        aria-label={t("agentBackends.flash.close")}
      >
        <ChevronDown className="size-3.5 rotate-45" aria-hidden="true" />
      </Button>
    </div>
  );
}
