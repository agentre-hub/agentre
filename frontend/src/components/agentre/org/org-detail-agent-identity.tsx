import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Input,
  OrgPlacementField,
  type OrgDepartmentModel,
  type OrgPlacement,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { AgentAvatarPicker, AgentAvatarUploadActions } from "../icon-picker";
import { AgentAvatar } from "../primitives";
import { agentColorClassNames, type AgentColor } from "../types";

import { OrgColorSwatches } from "./org-color-swatches";
import {
  iconForKey,
  safeAgentColor,
  type OrgAgent,
  type OrgDepartment,
} from "./types";

type IdentityPatch = {
  name?: string;
  description?: string;
  avatarColor?: AgentColor;
  avatarIcon?: string;
};

/** 三栏里的「身份」栏：名字 / 简介 / 头像 / 头像色 / 归属。 */
export function OrgDetailAgentIdentity({
  agent,
  agents,
  departments,
  name,
  description,
  avatarColor,
  avatarIcon,
  patch,
  flush,
  placement,
  reportTarget,
  onPlacementPick,
  onUploadFile,
  onDeleteAvatar,
}: {
  agent: OrgAgent;
  agents: OrgAgent[];
  departments: OrgDepartment[];
  name: string;
  description: string;
  avatarColor: AgentColor;
  avatarIcon: string;
  patch: (partial: IdentityPatch, opts?: { immediate?: boolean }) => void;
  flush: () => void;
  placement: OrgPlacement;
  reportTarget: OrgAgent | null;
  onPlacementPick: (next: OrgPlacement) => void;
  onUploadFile: (file: File) => Promise<void>;
  onDeleteAvatar: () => Promise<void>;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("org.detail.columns.identity")}
      data-slot="org-detail-col-identity"
      className="flex min-w-0 flex-col gap-4"
    >
      <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("org.detail.columns.identity")}
      </h3>
      <div className="space-y-1.5">
        <label className="block text-2xs text-muted-foreground">
          {t("org.department.name")}
        </label>
        <Input
          value={name}
          onChange={(e) => patch({ name: e.target.value })}
          onBlur={flush}
          aria-label={t("org.department.name")}
        />
      </div>
      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <label className="text-2xs text-muted-foreground">
            {t("org.department.description")}
          </label>
          <span className="font-mono text-2xs text-muted-foreground">
            {t("org.agent.descriptionHint")}
          </span>
        </div>
        <Input
          value={description}
          onChange={(e) => patch({ description: e.target.value })}
          onBlur={flush}
          aria-label={t("org.department.description")}
        />
      </div>
      <div className="space-y-2">
        <label className="block text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatar")}
        </label>
        <div className="flex min-w-0 items-center gap-3">
          <AgentAvatarPicker
            name={name || agent.name}
            avatarColor={avatarColor}
            avatarIcon={avatarIcon}
            avatarDataUrl={agent.avatarDataUrl}
            onChangeIcon={(v) => patch({ avatarIcon: v }, { immediate: true })}
            showImageMode={false}
            triggerSize="lg"
            triggerClassName="size-12 rounded-lg"
          />
          <AgentAvatarUploadActions
            avatarDataUrl={agent.avatarDataUrl}
            onUpload={onUploadFile}
            onDelete={onDeleteAvatar}
            uploadLabel={
              agent.avatarDataUrl
                ? t("org.agent.avatar.replaceImage")
                : t("org.agent.avatar.uploadImage")
            }
          />
        </div>
      </div>
      <div className="space-y-2">
        <label className="block text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatarColor")}
        </label>
        <OrgColorSwatches
          value={avatarColor}
          groupLabel={t("org.chart.newAgent.avatarColor")}
          optionLabel={(c) =>
            t("org.chart.newAgent.avatarColorNamed", { color: c })
          }
          onPick={(c) => patch({ avatarColor: c }, { immediate: true })}
          swatchClassName="size-7"
          selectedClassName="ring-2 ring-primary"
        />
      </div>
      <OrgPlacementField
        agent={agent}
        agents={agents}
        departments={departments}
        placement={placement}
        reportTarget={reportTarget}
        onPick={onPlacementPick}
        renderAgentAvatar={(a, className) => (
          <AgentAvatar
            name={a.name}
            color={safeAgentColor(a.avatarColor ?? "")}
            avatarDataUrl={a.avatarDataUrl}
            avatarIcon={a.avatarIcon}
            className={className}
          />
        )}
        renderDepartmentGlyph={(department) => (
          <DepartmentGlyph department={department} />
        )}
      />
    </section>
  );
}

/**
 * 归属那一格与部门字形都在共享包里 —— 身份怎么画（图标注册表 / 自定义头像图片）
 * 是宿主的事，所以经 slot 递进去。
 */
function DepartmentGlyph({ department }: { department?: OrgDepartmentModel }) {
  const Icon = iconForKey(department?.icon ?? "puzzle");
  return (
    <span
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-agent-foreground",
        agentColorClassNames[safeAgentColor(department?.accentColor ?? "")],
      )}
      aria-hidden="true"
    >
      {React.createElement(Icon, { className: "size-3" })}
    </span>
  );
}
