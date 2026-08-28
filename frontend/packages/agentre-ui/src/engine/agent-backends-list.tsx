// 后端列表一侧：空态、单行，以及行内的绑定面包屑与运行位置文案。
// 全部只吃 props，不碰端口，可在测试里单独渲染。
import * as React from "react";
import {
  AlertCircle,
  Lock,
  Pencil,
  Plus,
  Puzzle,
  SendHorizontal,
  Trash2,
  X,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../i18n";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { cn } from "../lib/utils";

import { backendDeviceLocation } from "./agent-backends-utils";
import {
  isCliBackend,
  type Backend,
  type BackendType,
  type Translate,
} from "./agent-backends-shared";
import {
  AgentBackendLogo,
  LlmModelLogo,
  LlmProviderLogo,
} from "./ai-brand-logo";

export function AgentBackendsEmptyState({
  onCreate,
  onOpenLlmProviders,
}: {
  onCreate: () => void;
  onOpenLlmProviders?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-10 text-center">
      <div
        aria-hidden="true"
        className="relative flex size-12 items-center justify-center rounded-full bg-primary-soft text-primary-text"
      >
        <Puzzle className="size-5" />
      </div>
      <div className="flex max-w-md flex-col gap-1">
        <p className="text-sm font-semibold">
          {t("agentBackends.empty.title")}
        </p>
        <p className="text-2xs leading-relaxed text-muted-foreground">
          {t("agentBackends.empty.description")}
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-2">
        <Button
          type="button"
          size="sm"
          className="h-[30px] gap-1.5 px-3 text-xs"
          onClick={onCreate}
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          {t("agentBackends.empty.addFirst")}
        </Button>
        {onOpenLlmProviders ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-[30px] gap-1.5 px-3 text-xs"
            onClick={onOpenLlmProviders}
          >
            {t("agentBackends.empty.addProvider")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

// 一行后端的绑定面包屑有四种形态：网关托管 / 走 CLI 自身登录 / 绑定失效 / 正常绑定。
type BindingVariant = "openclaw" | "cli-login" | "invalid" | "bound";

// 绑定长在元数据行上（供应商 › 模型 + 跟随默认/固定），不再独立成块 —— 一眼看清绑了什么。
function BackendRowBinding({
  backend,
  variant,
}: {
  backend: Backend;
  variant: BindingVariant;
}) {
  const { t } = useTranslation();
  const providerKey =
    (backend as unknown as { llmProviderKey?: string }).llmProviderKey ?? "";
  const modelKey =
    (backend as unknown as { llmModelKey?: string }).llmModelKey ?? "";
  const follow = modelKey === "";
  return (
    <span
      data-testid="backend-binding"
      className={cn(
        "inline-flex min-w-0 shrink items-center gap-1 rounded-md px-1.5 py-0.5",
        variant === "invalid"
          ? "bg-status-waiting-bg text-status-waiting"
          : "bg-secondary text-foreground",
      )}
    >
      {variant === "openclaw" ? (
        <>
          <LlmModelLogo
            providerType="openclaw"
            model={
              backend.openClawDefaultModel ||
              backend.openClawAgentId ||
              "openclaw"
            }
            className="size-3.5 shrink-0"
          />
          <span className="truncate">
            {backend.openClawDefaultModel ||
              backend.openClawAgentId ||
              t("agentBackends.openclaw.modelGatewayDefault")}
          </span>
        </>
      ) : variant === "cli-login" ? (
        <>
          <Lock className="size-3 shrink-0" aria-hidden="true" />
          <span className="truncate">
            {t("agentBackends.row.bindingCliLogin")}
          </span>
        </>
      ) : variant === "invalid" ? (
        <>
          <AlertCircle className="size-3 shrink-0" aria-hidden="true" />
          <span className="shrink-0 font-medium">
            {t("agentBackends.row.bindingInvalid")}
          </span>
          <span className="text-decorative-foreground" aria-hidden="true">
            ·
          </span>
          <span className="truncate">
            {t("agentBackends.row.bindingInvalidReason", {
              provider: backend.llmProviderName || providerKey,
              model: backend.llmProviderModel || modelKey,
            })}
          </span>
        </>
      ) : (
        <>
          <LlmProviderLogo
            providerType={backend.llmProviderType ?? ""}
            providerName={backend.llmProviderName ?? ""}
            className="size-3.5 shrink-0"
          />
          <span className="truncate">{backend.llmProviderName}</span>
          <span
            className="shrink-0 text-decorative-foreground"
            aria-hidden="true"
          >
            ›
          </span>
          <span className="truncate font-mono">
            {backend.llmProviderModel || t("agentBackends.provider.noModel")}
          </span>
          <Badge
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
      )}
    </span>
  );
}

// 运行位置只说这一行知道的事：解析得到名字就报名字，解析不到就是那台机器已从账号
// 撤销，没填设备则按宿主有没有本机分成「本机」与「未指定设备」。从前无论哪种都回落
// 成「本机」——那是把三个不同的事实压成同一句话。
function runtimeLocationLabel(
  backend: Backend,
  hasLocalDevice: boolean,
  t: Translate,
): string {
  const deviceName = (backend.deviceName ?? "").trim();
  switch (
    backendDeviceLocation(backend.deviceId ?? "", deviceName, hasLocalDevice)
  ) {
    case "named":
      return deviceName;
    case "local":
      return t("agentBackends.device.localShort");
    case "revoked":
      return t("agentBackends.device.revoked");
    default:
      return t("agentBackends.device.unspecified");
  }
}

export function BackendRow({
  backend,
  hasLocalDevice,
  testing,
  testDisabled,
  onTest,
  onCancelTest,
  onEdit,
  onChangeBinding,
  onDelete,
}: {
  backend: Backend;
  hasLocalDevice: boolean;
  testing: boolean;
  testDisabled: boolean;
  onTest: () => void;
  onCancelTest: () => void;
  onEdit: () => void;
  onChangeBinding: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const typ = (backend.type as BackendType) ?? "builtin";
  const cliBased = isCliBackend(typ);
  const openClaw = typ === "openclaw";
  // 未关联 provider 的 CLI 后端 = 走 CLI 自身 login，不算需处理。
  const providerKey =
    (backend as unknown as { llmProviderKey?: string }).llmProviderKey ?? "";
  const unlinkedCli = cliBased && providerKey === "";
  const warning = !openClaw && !unlinkedCli && !backend.llmProviderActive;
  const bindingVariant: BindingVariant = openClaw
    ? "openclaw"
    : unlinkedCli
      ? "cli-login"
      : warning
        ? "invalid"
        : "bound";

  // 类型回到名字旁的 chip;元数据行只留「绑定面包屑 · 运行位置 · 引用数」，一行说清
  // 「绑了谁、在哪跑、谁在用」。
  const metaTail: string[] = [
    openClaw && backend.openClawGatewayUrl
      ? backend.openClawGatewayUrl
      : runtimeLocationLabel(backend, hasLocalDevice, t),
    backend.agentCount > 0
      ? t("agentBackends.row.agentCount", { count: backend.agentCount })
      : t("agentBackends.row.unused"),
  ];

  return (
    <div
      role="listitem"
      className={cn(
        "flex min-w-0 flex-col gap-1.5 border-b border-border px-4 py-3 transition-colors last:border-b-0 hover:bg-accent/45",
        // 需处理的行用琥珀色左缘标记，一眼可扫；用 inset 阴影避免改变行宽。
        warning && "shadow-[inset_3px_0_0_0_var(--status-waiting)]",
      )}
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <AgentBackendLogo backendType={typ} className="size-7 rounded-md" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <span
              data-selectable-text="true"
              className="min-w-0 truncate text-sm font-semibold"
            >
              {backend.name}
            </span>
            <Badge
              data-testid="backend-type-chip"
              variant="secondary"
              className="shrink-0 rounded-sm px-1.5 py-0 text-2xs font-normal"
            >
              {t(`agentBackends.backendType.${typ}.label`)}
            </Badge>
            {warning ? (
              <Badge
                variant="secondary"
                className="shrink-0 rounded-sm bg-status-waiting-bg px-1.5 py-0 font-mono text-2xs text-status-waiting"
              >
                {t("agentBackends.row.needsAction")}
              </Badge>
            ) : null}
          </div>
          <div
            data-testid="backend-meta"
            className="flex min-w-0 items-center gap-1.5 text-2xs text-muted-foreground"
          >
            <BackendRowBinding backend={backend} variant={bindingVariant} />
            <span className="min-w-0 truncate">
              {metaTail.map((part, i) => (
                <React.Fragment key={i}>
                  <span
                    className="mx-1 text-decorative-foreground"
                    aria-hidden="true"
                  >
                    ·
                  </span>
                  {part}
                </React.Fragment>
              ))}
            </span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {!openClaw ? (
            <Button
              type="button"
              variant={warning ? "default" : "outline"}
              size="xs"
              // 绑定已失效时这不是「换一个」而是「必须重选」，动词跟着状态走。
              aria-label={
                warning
                  ? t("agentBackends.actions.rebindNamed", {
                      name: backend.name,
                    })
                  : t("agentBackends.actions.changeBindingNamed", {
                      name: backend.name,
                    })
              }
              onClick={onChangeBinding}
            >
              {warning
                ? t("agentBackends.actions.rebind")
                : t("agentBackends.actions.changeBinding")}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            // testing 时按钮变成"取消测试"，必须保持可点击；其它行 testDisabled 仍 disable。
            aria-label={
              testing
                ? t("agentBackends.actions.cancelTestNamed", {
                    name: backend.name,
                  })
                : t("agentBackends.actions.testNamed", { name: backend.name })
            }
            title={
              testing
                ? t("agentBackends.actions.cancelTest")
                : t("agentBackends.actions.test")
            }
            className={cn(
              "size-[26px]",
              testing ? "text-status-error" : "text-muted-foreground",
            )}
            disabled={testDisabled && !testing}
            onClick={testing ? onCancelTest : onTest}
          >
            {testing ? (
              <X data-icon="only" aria-hidden="true" />
            ) : (
              <SendHorizontal data-icon="only" aria-hidden="true" />
            )}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("agentBackends.actions.editNamed", {
              name: backend.name,
            })}
            title={t("common.edit")}
            className="size-[26px] text-muted-foreground"
            onClick={onEdit}
          >
            <Pencil data-icon="only" aria-hidden="true" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("agentBackends.actions.deleteNamed", {
              name: backend.name,
            })}
            title={t("common.delete")}
            className="size-[26px] text-status-error"
            onClick={onDelete}
          >
            <Trash2 data-icon="only" aria-hidden="true" />
          </Button>
        </div>
      </div>
    </div>
  );
}
