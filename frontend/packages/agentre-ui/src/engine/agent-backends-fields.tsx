// 编辑器里的受控字段控件：模型绑定区块与它的 Picker/分级路由，以及 CLI 路径、沙箱、
// 审批、思考力度、默认模型、默认权限模式、环境变量各字段。
// 全部只吃 props，不碰端口，可在测试里单独渲染。
import * as React from "react";
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Loader2,
  Plus,
  Radar,
  X,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../i18n";
import { Alert, AlertDescription, AlertTitle } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { cn } from "../lib/utils";

import type { ResolvedModelTarget } from "./agent-backends-utils";
import {
  CLAUDE_TIERS,
  RESERVED_ENV_KEYS,
  cliBinaryName,
  isCliBackend,
  routeConclusion,
  type ApprovalValue,
  type BackendType,
  type ClaudeTier,
  type EnvEntry,
  type Provider,
  type ReasoningEffortValue,
  type RouteTarget,
  type SandboxValue,
  type Translate,
} from "./agent-backends-shared";
import { LlmProviderLogo } from "./ai-brand-logo";
import { ModelTargetPicker, type PickerProvider } from "./model-target-picker";

// codex CLI 支持到 xhigh；UI 不暴露 max，避免「保存了 max 实际上下发 high」的迷惑。
// 类型切到 codex 时若历史值是 max，会自动降为 high（buildDraft / handleTypeChange）。
const REASONING_EFFORTS_FULL: ReasoningEffortValue[] = [
  "",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];
const REASONING_EFFORTS_CODEX: ReasoningEffortValue[] = [
  "",
  "low",
  "medium",
  "high",
  "xhigh",
];

const APPROVAL_OPTIONS: {
  value: Exclude<ApprovalValue, "">;
}[] = [{ value: "untrusted" }, { value: "on-request" }, { value: "never" }];

const SANDBOX_OPTIONS: {
  value: Exclude<SandboxValue, "">;
  label: string;
}[] = [
  { value: "read-only", label: "read-only" },
  { value: "workspace-write", label: "workspace-write" },
  { value: "danger-full-access", label: "danger-full-access" },
];

function ProviderConfigureCta({ onConfigure }: { onConfigure?: () => void }) {
  const { t } = useTranslation();
  return (
    <Button
      type="button"
      size="sm"
      className="mt-1.5 h-7 px-2.5 text-2xs"
      onClick={onConfigure}
    >
      {t("agentBackends.provider.configureCta")}
    </Button>
  );
}

// 触发按钮主行：品牌标识 + 供应商名 + 跟随/固定徽标 —— 和列表行的绑定面包屑
// （BackendRowBinding）同一套处理，让「绑了谁、是不是跟随」在收起状态下也一眼可读。
// 按钮的无障碍名由 ModelTargetPicker 的 aria-label 决定，这里只管视觉主行。
function BindingTriggerLabel({ target }: { target: ResolvedModelTarget }) {
  const { t } = useTranslation();
  if (target.mode === "native") {
    return (
      <span className="min-w-0 truncate">
        {t("agentBackends.binding.cliLogin")}
      </span>
    );
  }
  if (target.mode === "invalid") {
    return (
      <span className="min-w-0 truncate">
        {t("agentBackends.binding.invalidTarget", {
          target: target.providerName || target.modelId,
        })}
      </span>
    );
  }
  const follow = target.mode === "provider-default";
  return (
    <>
      <LlmProviderLogo
        providerType={target.providerType}
        providerName={target.providerName}
        className="size-3.5 shrink-0"
      />
      <span className="min-w-0 truncate font-medium">
        {target.providerName}
      </span>
      <Badge
        data-testid="binding-mode-chip"
        variant="secondary"
        className={cn(
          "shrink-0 rounded-sm px-1 py-0 text-2xs font-normal",
          follow && "bg-primary-soft text-primary-text",
        )}
      >
        {follow
          ? t("agentBackends.binding.modeFollow")
          : t("agentBackends.binding.modeFixed")}
      </Badge>
    </>
  );
}

function bindingTriggerSub(t: Translate, target: ResolvedModelTarget): string {
  if (target.mode === "native") return t("agentBackends.binding.cliResolution");
  if (target.mode === "invalid")
    return t("agentBackends.binding.invalidResolution");
  return target.mode === "provider-default"
    ? t("agentBackends.binding.followResolution", { model: target.modelId })
    : t("agentBackends.binding.fixedResolution", { model: target.modelId });
}

// 分级路由 Picker 顶部「继承主绑定」项的解析副行 —— 与会话场景同一形态：箭头点出
// 「解析到」，品牌标识让主绑定的供应商一眼可认，模型 ID 单独走等宽。CLI 登录态 / 失效
// 目标没有可认的供应商，回落既有纯文字说明（宁可少画一个标识，也不画半个空标识）。
function bindingSpecialSublabel(
  t: Translate,
  target: ResolvedModelTarget,
): React.ReactNode {
  if (
    target.mode === "native" ||
    target.mode === "invalid" ||
    !target.providerType
  ) {
    return bindingTriggerSub(t, target);
  }
  return (
    <span
      data-testid="special-resolution"
      className="flex min-w-0 items-center gap-1"
    >
      <span aria-hidden="true">→</span>
      <LlmProviderLogo
        providerType={target.providerType}
        providerName={target.providerName}
        className="size-3.5"
      />
      <span className="min-w-0 truncate">
        {target.providerName}
        {target.modelId ? (
          <>
            {" · "}
            <span className="font-mono">{target.modelId}</span>
          </>
        ) : null}
      </span>
    </span>
  );
}

export function ModelBindingSection({
  type,
  providers,
  value,
  modelKey,
  onTargetChange,
  onSyncProvider,
  invalid,
  piAgentModelMissing,
  editing,
  onOpenLlmProviders,
  catalog,
  catalogLoading,
  catalogError,
  executionLocation,
  supportsFixedModel,
  remoteCatalog,
  routes,
  onRoutesChange,
  customModel,
  onCustomModelChange,
  resolvedMainTarget,
  openPickerOnMount,
}: {
  type: BackendType;
  providers: Provider[];
  value: string;
  modelKey: string;
  onTargetChange: (target: RouteTarget) => void;
  onSyncProvider?: (provider: PickerProvider) => void;
  invalid: boolean;
  piAgentModelMissing: boolean;
  editing: boolean;
  onOpenLlmProviders?: () => void;
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  executionLocation: string;
  supportsFixedModel: boolean;
  remoteCatalog?: PickerProvider[];
  routes: Record<ClaudeTier, RouteTarget>;
  onRoutesChange: (routes: Record<ClaudeTier, RouteTarget>) => void;
  customModel: string;
  onCustomModelChange: (value: string) => void;
  resolvedMainTarget: ResolvedModelTarget;
  openPickerOnMount: boolean;
}) {
  const { t } = useTranslation();
  const cliLogin = resolvedMainTarget.mode === "native";
  return (
    <section
      data-testid="model-binding-block"
      className="flex flex-col gap-3 rounded-lg border border-border bg-secondary/30 p-3"
    >
      {/* 区块标题就是这项配置的唯一标题，下面的 Picker 直接是它的第一项，不再重复一遍
          字段名 —— 「分级路由 / 自定义模型从属于主绑定」也由区块嵌套自己讲清楚，不另起
          一段说明（mockup ?view=backend 的区块头就只有标题）。 */}
      <div className="flex min-w-0 items-center gap-1.5">
        <h3 className="text-xs font-semibold">
          {t("agentBackends.binding.label")}
        </h3>
        {isCliBackend(type) ? (
          <span className="font-mono text-2xs text-muted-foreground">
            {t("agentBackends.provider.optionalSuffix")}
          </span>
        ) : null}
      </div>
      <ModelTargetField
        type={type}
        providers={providers}
        value={value}
        modelKey={modelKey}
        onTargetChange={onTargetChange}
        onSyncProvider={onSyncProvider}
        invalid={invalid}
        piAgentModelMissing={piAgentModelMissing}
        editing={editing}
        onOpenLlmProviders={onOpenLlmProviders}
        catalog={catalog}
        catalogLoading={catalogLoading}
        catalogError={catalogError}
        executionLocation={executionLocation}
        supportsFixedModel={supportsFixedModel}
        remoteCatalog={remoteCatalog}
        resolvedTarget={resolvedMainTarget}
        openPickerOnMount={openPickerOnMount}
      />
      {type === "claudecode" ? (
        <>
          {cliLogin ? (
            <DefaultModelField
              value={customModel}
              onChange={onCustomModelChange}
              description={t("agentBackends.defaultModel.cliOnlyDescription")}
            />
          ) : (
            <p className="text-2xs text-muted-foreground">
              {t("agentBackends.defaultModel.ignoredDescription")}
            </p>
          )}
          <ModelRoutesField
            catalog={catalog}
            catalogLoading={catalogLoading}
            catalogError={catalogError}
            routes={routes}
            onChange={onRoutesChange}
            executionLocation={executionLocation}
            supportsFixedModel={supportsFixedModel}
            remoteCatalog={remoteCatalog}
            mainTarget={resolvedMainTarget}
          />
        </>
      ) : null}
    </section>
  );
}

function ModelTargetField({
  type,
  providers,
  value,
  modelKey,
  onTargetChange,
  onSyncProvider,
  invalid,
  piAgentModelMissing,
  editing,
  onOpenLlmProviders,
  catalog,
  catalogLoading,
  catalogError,
  executionLocation,
  supportsFixedModel = true,
  remoteCatalog,
  resolvedTarget,
  openPickerOnMount = false,
}: {
  type: BackendType;
  providers: Provider[];
  value: string;
  modelKey: string;
  onTargetChange: (t: { providerKey: string; modelKey: string }) => void;
  onSyncProvider?: (provider: PickerProvider) => void;
  invalid: boolean;
  piAgentModelMissing: boolean;
  editing: boolean;
  onOpenLlmProviders?: () => void;
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  executionLocation: string;
  supportsFixedModel?: boolean;
  remoteCatalog?: PickerProvider[];
  resolvedTarget: ResolvedModelTarget;
  openPickerOnMount?: boolean;
}) {
  const { t } = useTranslation();
  // claudecode / codex / piagent 允许「不关联」走 CLI 自身登录；builtin 必填。
  const optional = isCliBackend(type);
  // Match by providerKey (preferred) or fall back to string id for legacy data.
  const matchesProvider = (p: Provider) =>
    (p.providerKey && p.providerKey === value) || String(p.id) === value;
  const stale = editing && value !== "" && !providers.some(matchesProvider);
  const empty = providers.length === 0;
  const selected = { providerKey: value, modelKey };
  const noCompatibleDescription = t("modelTargetPicker.noCompatibleProviders");

  if (empty && !optional) {
    return (
      <div className="flex flex-col gap-1.5 text-xs">
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.emptyTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.noneDescription")}
            {onOpenLlmProviders ? (
              <ProviderConfigureCta onConfigure={onOpenLlmProviders} />
            ) : null}
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5 text-xs">
      {stale ? (
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.staleTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.staleDescription", {
              optionalClause: optional
                ? t("agentBackends.provider.staleOptionalClause")
                : "",
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      {empty && optional ? (
        <Alert className="border-border bg-secondary text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.noMatchTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {noCompatibleDescription}
            {onOpenLlmProviders ? (
              <ProviderConfigureCta onConfigure={onOpenLlmProviders} />
            ) : null}
          </AlertDescription>
        </Alert>
      ) : (
        <ModelTargetPicker
          scenario="backend"
          backendType={type}
          executionLocation={executionLocation}
          selected={selected}
          onChange={(target) =>
            onTargetChange({
              providerKey: target.providerKey,
              modelKey: target.modelKey,
            })
          }
          onSyncProvider={onSyncProvider}
          catalog={catalog}
          loading={catalogLoading}
          error={catalogError}
          invalid={invalid}
          openOnMount={openPickerOnMount}
          supportsFixedModel={supportsFixedModel}
          remoteCatalog={remoteCatalog}
          triggerLabel={<BindingTriggerLabel target={resolvedTarget} />}
          triggerSub={bindingTriggerSub(t, resolvedTarget)}
          aria-label={t("agentBackends.binding.label")}
        />
      )}
      {piAgentModelMissing ? (
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.modelRequiredTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.modelRequiredDescription")}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

export function CliPathField({
  type,
  value,
  onChange,
  onDetect,
  detecting,
  missMessage,
}: {
  type: BackendType;
  value: string;
  onChange: (v: string) => void;
  onDetect: () => void;
  detecting: boolean;
  missMessage: string | null;
}) {
  const { t } = useTranslation();
  const bin = cliBinaryName(type);
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.cli.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {value.trim() === ""
            ? t("agentBackends.cli.emptyHint", { bin })
            : t("agentBackends.cli.explicitHint")}
        </span>
      </div>
      <div className="flex items-center gap-1.5">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`/usr/local/bin/${bin}`}
          className="font-mono"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 shrink-0 gap-1 px-2 text-2xs"
          onClick={onDetect}
          disabled={detecting}
          aria-label={t("agentBackends.cli.detect")}
          title={t("agentBackends.cli.detectTitle", { bin })}
        >
          {detecting ? (
            <Loader2 className="size-3 animate-spin" aria-hidden="true" />
          ) : (
            <Radar className="size-3" aria-hidden="true" />
          )}
          {t("agentBackends.cli.detect")}
        </Button>
      </div>
      {missMessage ? (
        <span className="font-mono text-2xs text-status-waiting">
          {missMessage}
        </span>
      ) : null}
    </div>
  );
}

function ModelRoutesField({
  catalog,
  catalogLoading,
  catalogError,
  routes,
  onChange,
  executionLocation,
  supportsFixedModel = true,
  remoteCatalog,
  mainTarget,
}: {
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  routes: Record<ClaudeTier, RouteTarget>;
  onChange: (r: Record<ClaudeTier, RouteTarget>) => void;
  executionLocation: string;
  supportsFixedModel?: boolean;
  remoteCatalog?: PickerProvider[];
  mainTarget: ResolvedModelTarget;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const summary = CLAUDE_TIERS.map(
    (tier) =>
      `${tier}: ${routeConclusion(t, routes[tier], mainTarget, catalog)}`,
  ).join(" · ");
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-background px-2.5 py-2 text-xs">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex min-w-0 cursor-pointer items-center gap-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        {open ? (
          <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />
        )}
        {/* 标签不换行、摘要吃掉剩余宽度：默认状态下摘要才是这一行真正要读的内容。 */}
        <span className="shrink-0 font-medium">
          {t("agentBackends.modelRoutes.label")}
        </span>
        <span className="min-w-0 flex-1 truncate text-2xs text-muted-foreground">
          {summary}
        </span>
        <Badge
          variant="secondary"
          className="shrink-0 rounded-sm px-1.5 py-0 text-2xs font-normal"
        >
          {t("agentBackends.modelRoutes.defaultChip")}
        </Badge>
      </button>
      {open ? (
        <div className="flex flex-col gap-1.5 pt-1.5">
          <p className="text-2xs text-muted-foreground">
            {t("agentBackends.modelRoutes.hint")}
          </p>
          {CLAUDE_TIERS.map((tier) => {
            const route = routes[tier] ?? { providerKey: "", modelKey: "" };
            const tierInvalid =
              route.providerKey !== "" &&
              !catalog.some(
                (p) =>
                  p.providerKey === route.providerKey &&
                  (route.modelKey === ""
                    ? !!p.defaultModel
                    : p.models.some(
                        (m) => m.modelKey === route.modelKey && m.enabled,
                      )),
              );
            return (
              <div
                key={tier}
                className="grid grid-cols-[64px_1fr] items-center gap-2"
              >
                <Badge
                  variant="secondary"
                  className="justify-self-start rounded-sm px-1.5 py-0.5 font-mono text-2xs"
                >
                  {tier}
                </Badge>
                <ModelTargetPicker
                  scenario="route"
                  backendType="claudecode"
                  executionLocation={executionLocation}
                  selected={route}
                  onChange={(target) =>
                    onChange({
                      ...routes,
                      [tier]: {
                        providerKey: target.providerKey,
                        modelKey: target.modelKey,
                      },
                    })
                  }
                  catalog={catalog}
                  loading={catalogLoading}
                  error={catalogError}
                  invalid={tierInvalid}
                  supportsFixedModel={supportsFixedModel}
                  remoteCatalog={remoteCatalog}
                  specialSublabel={bindingSpecialSublabel(t, mainTarget)}
                  triggerSub={routeConclusion(t, route, mainTarget, catalog)}
                  compact
                  aria-label={t("agentBackends.modelRoutes.tierAria", { tier })}
                />
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

export function SandboxField({
  value,
  onChange,
}: {
  value: SandboxValue;
  onChange: (v: SandboxValue) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.sandbox.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.sandbox.hint")}
        </span>
      </div>
      <div className="grid grid-cols-3 gap-1 rounded-md border border-border bg-secondary p-0.5">
        {SANDBOX_OPTIONS.map((opt) => {
          const active = value === opt.value;
          return (
            <button
              key={opt.value}
              type="button"
              onClick={() => onChange(active ? "" : opt.value)}
              aria-pressed={active}
              className={cn(
                "rounded-[5px] px-2 py-1.5 font-mono text-2xs transition-colors",
                active
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {opt.label}
            </button>
          );
        })}
      </div>
      {value === "" ? (
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.sandbox.defaultHint")}
        </span>
      ) : null}
    </div>
  );
}

export function ApprovalField({
  value,
  onChange,
}: {
  value: ApprovalValue;
  onChange: (v: ApprovalValue) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.approval.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.approval.hint")}
        </span>
      </div>
      <Select
        value={value === "" ? "never" : value}
        onValueChange={(v) => onChange(v as ApprovalValue)}
      >
        <SelectTrigger aria-label={t("agentBackends.approval.label")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {APPROVAL_OPTIONS.map((opt) => (
            <SelectItem key={opt.value} value={opt.value}>
              <span className="inline-flex items-center gap-2">
                <span className="font-mono text-2xs">{opt.value}</span>
                <span className="text-muted-foreground">
                  {t(`agentBackends.approval.options.${opt.value}`)}
                </span>
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

// ReasoningEffortField shadcn Select 把"思考力度"以六档（默认 + low/medium/high/xhigh/max）
// 暴露给用户。codex 类型下展示到 xhigh，隐藏 max——max 在底层会 clamp 到 high，
// UI 直接隐藏避免「保存了 max 实际上等于 high」的迷惑。
//
// Select 不接受空字符串作为 SelectItem value，所以把 "" 映射为字面量 "default"，
// 在 onValueChange 回传时再翻译回 ""，与后端枚举对齐。
export function ReasoningEffortField({
  type,
  value,
  onChange,
}: {
  type: BackendType;
  value: ReasoningEffortValue;
  onChange: (v: ReasoningEffortValue) => void;
}) {
  const { t } = useTranslation();
  const options =
    type === "codex" ? REASONING_EFFORTS_CODEX : REASONING_EFFORTS_FULL;
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">
          {t("agentBackends.reasoning.label")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          reasoning_effort
        </span>
      </div>
      <Select
        value={value === "" ? "default" : value}
        onValueChange={(v) =>
          onChange((v === "default" ? "" : v) as ReasoningEffortValue)
        }
      >
        <SelectTrigger aria-label={t("agentBackends.reasoning.label")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((opt) => (
            <SelectItem
              key={opt || "default"}
              value={opt === "" ? "default" : opt}
            >
              <span className="inline-flex items-center gap-2">
                <span className="font-mono text-2xs">{opt || "default"}</span>
                <span className="text-muted-foreground">
                  {t(`agentBackends.reasoning.options.${opt || "default"}`)}
                </span>
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {type === "codex" ? (
        <span className="text-2xs text-muted-foreground">
          {t("agentBackends.reasoning.codexHint")}
        </span>
      ) : null}
    </div>
  );
}

// DefaultModelField 是 claudecode 的「自定义模型」配置：spawn claude 子进程时
// 下发的 --model 值。主要用于走 CLI 自身登录态（未绑 provider）时指定模型
// （如 claude-fable-5）；绑了 provider 时 provider.Model 优先，本字段仅兜底。
// 纯自由文本，CLI 既收别名（fable/opus/sonnet）也收完整模型名。
function DefaultModelField({
  value,
  onChange,
  description,
}: {
  value: string;
  onChange: (v: string) => void;
  description: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">
          {t("agentBackends.defaultModel.label")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.defaultModel.hint")}
        </span>
      </div>
      <Input
        aria-label={t("agentBackends.defaultModel.label")}
        value={value}
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        placeholder={t("agentBackends.defaultModel.placeholder")}
        onChange={(e) => onChange(e.currentTarget.value)}
        className="h-9 font-mono text-xs"
      />
      <span className="text-2xs text-muted-foreground">{description}</span>
    </div>
  );
}

// DefaultPermissionModeField 是 claudecode 的「新会话默认起手 mode」配置。
// 取值：
//   - "" → 走 pkg/claudecode 兜底（acceptEdits）。
//   - default / acceptEdits / plan → 普通模式。
//   - bypassPermissions → 起手即跳审批；CLI spawn 时下发 --permission-mode bypassPermissions，
//     runtime 仍可在 4 档之间自由切换（实测 claude v2.1.x 行为）。
//
// 用 shadcn Select 而不是 Switch：4 档枚举 + 危险等级递进的视觉提示。
//
// 远端 + bypass 的额外坑：claude CLI 内部把 --permission-mode bypassPermissions
// 当作 --dangerously-skip-permissions 走 root 检查，若 agentred 以 root/sudo
// 运行会被 CLI 直接拒掉。设 IS_SANDBOX=1 可让 CLI 跳过该检查（CLI 自带的
// 沙箱逃生口）。此处展示提示 + 一键填到 env_json。
export function DefaultPermissionModeField({
  value,
  onChange,
  isRemote,
  canEditEnvJSON,
  hasIsSandbox,
  onAddIsSandbox,
}: {
  value: string;
  onChange: (v: string) => void;
  isRemote: boolean;
  canEditEnvJSON: boolean;
  hasIsSandbox: boolean;
  onAddIsSandbox: () => void;
}) {
  const { t } = useTranslation();
  const isBypass = value === "bypassPermissions";
  const showRootHint = isBypass && isRemote;
  return (
    <div
      className={cn(
        "flex flex-col gap-1.5 rounded-md border px-3 py-2 text-xs",
        isBypass
          ? "border-destructive/40 bg-destructive-soft"
          : "border-border bg-secondary/40",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span
            className={cn(
              "inline-flex items-center gap-1.5 font-medium",
              isBypass ? "text-destructive" : "",
            )}
          >
            {isBypass ? (
              <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
            ) : null}
            {t("agentBackends.permission.label")}
          </span>
          <span className="font-mono text-2xs text-muted-foreground">
            {t("agentBackends.permission.hint")}
          </span>
        </div>
        <Select
          value={value === "" ? "__inherit__" : value}
          onValueChange={(v) => onChange(v === "__inherit__" ? "" : v)}
        >
          <SelectTrigger
            aria-label={t("agentBackends.permission.label")}
            className="h-7 w-[170px] text-2xs"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__inherit__">
              <span className="text-muted-foreground">
                {t("agentBackends.permission.options.inherit")}
              </span>
            </SelectItem>
            <SelectItem value="default">
              {t("agentBackends.permission.options.default")}
            </SelectItem>
            <SelectItem value="acceptEdits">
              {t("agentBackends.permission.options.acceptEdits")}
            </SelectItem>
            <SelectItem value="plan">
              {t("agentBackends.permission.options.plan")}
            </SelectItem>
            <SelectItem value="bypassPermissions">
              {t("agentBackends.permission.options.bypassPermissions")}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
      {isBypass ? (
        <span className="text-2xs text-destructive">
          {t("agentBackends.permission.bypassWarning")}
        </span>
      ) : null}
      {showRootHint && canEditEnvJSON ? (
        <div className="flex flex-wrap items-center gap-2 rounded border border-status-waiting/40 bg-status-waiting-bg px-2 py-1.5 text-2xs text-status-waiting">
          <span className="min-w-0 flex-1">
            {t("agentBackends.permission.remoteRootHintPrefix")}{" "}
            <span className="font-mono">IS_SANDBOX=1</span>{" "}
            {t("agentBackends.permission.remoteRootHintSuffix")}
          </span>
          {hasIsSandbox ? (
            <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
              <CheckCircle2 className="size-3" aria-hidden="true" />
              {t("agentBackends.permission.isSandboxConfigured")}
            </span>
          ) : (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-6 gap-1 px-2 text-2xs"
              onClick={onAddIsSandbox}
            >
              <Plus className="size-3" aria-hidden="true" />
              {t("agentBackends.permission.addIsSandbox")}
            </Button>
          )}
        </div>
      ) : null}
    </div>
  );
}

export function EnvJsonField({
  entries,
  onChange,
  open,
  onToggle,
  reservedOffenders,
}: {
  entries: EnvEntry[];
  onChange: (next: EnvEntry[]) => void;
  open: boolean;
  onToggle: () => void;
  reservedOffenders: string[];
}) {
  const { t } = useTranslation();
  const filledCount = entries.filter((e) => e.key.trim() !== "").length;
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-secondary/40 px-3 py-2 text-xs">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex items-center justify-between gap-2 text-left"
      >
        <span className="inline-flex items-center gap-1.5 font-medium">
          {open ? (
            <ChevronDown className="size-3.5" aria-hidden="true" />
          ) : (
            <ChevronRight className="size-3.5" aria-hidden="true" />
          )}
          {t("agentBackends.env.title")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.env.count", { count: filledCount })}
        </span>
      </button>
      {open ? (
        <div className="flex flex-col gap-1.5 pt-1.5">
          {reservedOffenders.length > 0 ? (
            <div className="rounded-sm bg-destructive-soft px-2 py-1 text-2xs text-destructive">
              {t("agentBackends.env.reservedDisabled", {
                keys: reservedOffenders.join(", "),
              })}
            </div>
          ) : null}
          {entries.length === 0 ? (
            <span className="text-2xs text-muted-foreground">
              {t("agentBackends.env.empty")}
            </span>
          ) : null}
          {entries.map((entry, i) => {
            const trimmed = entry.key.trim();
            const isReserved = trimmed !== "" && RESERVED_ENV_KEYS.has(trimmed);
            return (
              <div
                key={i}
                className="grid grid-cols-[1fr_1fr_28px] items-center gap-1.5"
              >
                <Input
                  value={entry.key}
                  onChange={(ev) =>
                    onChange(
                      entries.map((x, j) =>
                        j === i ? { ...x, key: ev.target.value } : x,
                      ),
                    )
                  }
                  placeholder="KEY"
                  className={cn(
                    "h-7 font-mono text-2xs",
                    isReserved && "border-destructive",
                  )}
                />
                <Input
                  value={entry.value}
                  onChange={(ev) =>
                    onChange(
                      entries.map((x, j) =>
                        j === i ? { ...x, value: ev.target.value } : x,
                      ),
                    )
                  }
                  placeholder="VALUE"
                  className="h-7 font-mono text-2xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={t("agentBackends.env.deleteKey")}
                  onClick={() => onChange(entries.filter((_, j) => j !== i))}
                >
                  <X data-icon="only" aria-hidden="true" />
                </Button>
              </div>
            );
          })}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 self-start gap-1 px-2 text-2xs"
            onClick={() => onChange([...entries, { key: "", value: "" }])}
          >
            <Plus className="size-3" aria-hidden="true" />
            {t("common.add")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
