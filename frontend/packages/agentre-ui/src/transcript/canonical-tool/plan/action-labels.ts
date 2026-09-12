import {
  CheckCircle2,
  FastForward,
  PencilLine,
  Send,
  ShieldCheck,
  ShieldOff,
  type LucideIcon,
} from "lucide-react";

import type { PlanActionDTO } from "../types";

// PlanActionMeta 把 backend 装配的 provider-neutral actionId 映射到前端展示属性。
export type PlanActionMeta = {
  label: string;
  variant: "default" | "outline" | "ghost";
  icon: LucideIcon;
};

type PlanActionMetaTemplate = Omit<PlanActionMeta, "label"> & {
  labelKey: string;
};

type Translate = (key: string) => string;

const META: Record<string, PlanActionMetaTemplate> = {
  "plan.approve.bypass_permissions": {
    labelKey: "canonical.plan.actions.approveBypass",
    variant: "default",
    icon: ShieldOff,
  },
  "plan.approve.accept_edits": {
    labelKey: "canonical.plan.actions.approveAcceptEdits",
    variant: "default",
    icon: FastForward,
  },
  "plan.approve.manual": {
    labelKey: "canonical.plan.actions.approveManual",
    variant: "outline",
    icon: ShieldCheck,
  },
  "plan.execute": {
    labelKey: "canonical.plan.actions.execute",
    variant: "default",
    icon: CheckCircle2,
  },
};

type MetaContext = {
  requestId?: string;
};

// metaFor 兜底未知 actionId → kind 级通用文案,避免 backend 加新 id 时前端崩溃。
export function metaFor(
  action: PlanActionDTO,
  context?: MetaContext,
  t: Translate = (key) => key,
): PlanActionMeta {
  if (action.id === "plan.refine" || action.kind === "refine") {
    return {
      label: context?.requestId
        ? t("canonical.plan.actions.refine")
        : t("canonical.plan.actions.refineStandalone"),
      variant: context?.requestId ? "ghost" : "outline",
      icon: PencilLine,
    };
  }
  const exact = META[action.id];
  if (exact) return { ...exact, label: t(exact.labelKey) };
  if (action.kind === "approve") {
    return {
      label: context?.requestId
        ? t("canonical.plan.actions.approve")
        : t("canonical.plan.actions.execute"),
      variant: "default",
      icon: CheckCircle2,
    };
  }
  return {
    label: t("canonical.plan.actions.continue"),
    variant: "outline",
    icon: Send,
  };
}

// splitActions 把"主要按钮"(非 refine)与"反馈按钮"(refine)拆开,前者放
// 工具栏主区,后者打开 feedback textarea。
export function splitActions(actions: PlanActionDTO[] | undefined): {
  primary: PlanActionDTO[];
  refine?: PlanActionDTO;
} {
  if (!actions || actions.length === 0) return { primary: [] };
  const refine = actions.find((a) => a.kind === "refine");
  const primary = actions.filter((a) => a.kind !== "refine");
  return { primary, refine };
}
