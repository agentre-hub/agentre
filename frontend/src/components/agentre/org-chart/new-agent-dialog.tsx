// 「新建 agent」对话框:薄壳 + 对话框体(表单本体,含部门选择与权限位)。

import * as React from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import {
  AgentreDialog,
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  agentColorClassNames,
  agentColorOrder,
  type AgentColor,
} from "../types";
import { AgentAvatarPicker } from "../icon-picker";
import { type OrgAgent, type OrgDepartment } from "../org/types";
import { useOrgData } from "../org/use-org-data";

export type NewAgentProps = {
  open: boolean;
  fromIntent?: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: ReturnType<typeof useOrgData>["backends"];
  defaultDepartmentId?: number;
  onSubmit: (req: {
    name: string;
    description: string;
    avatarColor: string;
    avatarIcon: string;
    departmentId: number;
    parentAgentId: number;
    agentBackendId: number;
    prompt: string[];
    skills: { label: string; enabled: boolean }[];
  }) => Promise<void>;
  onClose: () => void;
};

export function NewAgentDialog(props: NewAgentProps) {
  if (!props.open) return null;
  return <NewAgentDialogBody {...props} />;
}

export function NewAgentDialogBody(props: NewAgentProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [avatarColor, setAvatarColor] = React.useState<AgentColor>("agent-1");
  const [avatarIcon, setAvatarIcon] = React.useState<string>("");
  const placementOptions = React.useMemo(
    () => [
      ...props.departments.map((d) => ({
        value: `department:${d.id}`,
        label: t("org.chart.newAgent.departmentOption", { name: d.name }),
      })),
      ...props.agents.map((a) => ({
        value: `agent:${a.id}`,
        label: t("org.chart.newAgent.agentOption", { name: a.name }),
      })),
    ],
    [props.departments, props.agents, t],
  );
  const initialPlacement = React.useMemo(() => {
    if (props.fromIntent) {
      const ceo = props.agents.find((a) => a.systemBadge === "DEFAULT");
      if (props.departments.length === 0 && ceo) {
        return `agent:${ceo.id}`;
      }
      return placementOptions[0]?.value ?? "";
    }
    const preset = props.defaultDepartmentId
      ? `department:${props.defaultDepartmentId}`
      : null;
    if (preset && placementOptions.some((opt) => opt.value === preset)) {
      return preset;
    }
    return placementOptions[0]?.value ?? "";
  }, [
    props.agents,
    props.defaultDepartmentId,
    props.departments.length,
    placementOptions,
    props.fromIntent,
  ]);
  const [placement, setPlacement] = React.useState<string>(initialPlacement);
  const [backendId, setBackendId] = React.useState<number>(
    props.fromIntent ? 0 : (props.backends[0]?.id ?? 0),
  );
  const parsedPlacement = parsePlacement(placement);

  const canSubmit =
    name.trim().length > 0 &&
    (parsedPlacement.departmentId > 0 || parsedPlacement.parentAgentId > 0);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    await props.onSubmit({
      name: name.trim(),
      description: description.trim(),
      avatarColor,
      avatarIcon,
      departmentId: parsedPlacement.departmentId,
      parentAgentId: parsedPlacement.parentAgentId,
      agentBackendId: backendId,
      prompt: [],
      skills: [],
    });
  }

  return (
    <AgentreDialog
      open={props.open}
      onOpenChange={(o) => !o && props.onClose()}
      title={t("org.chart.actions.newAgent")}
      description={t("org.chart.newAgent.description")}
      bodyClassName="flex flex-col gap-4"
      onSubmit={handleSubmit}
      footer={
        <>
          <Button type="button" variant="outline" onClick={props.onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={!canSubmit}>
            {t("common.create")}
          </Button>
        </>
      }
    >
      {props.fromIntent ? (
        <div className="flex items-center justify-between gap-3 rounded-md border border-primary/20 bg-primary-soft px-3 py-2 text-2xs text-muted-foreground">
          <span>{t("org.chart.newAgent.intentHint")}</span>
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-2xs"
            onClick={() =>
              navigate("/settings", {
                state: { settingsPage: "agent-backend" },
              })
            }
          >
            {t("org.chart.newAgent.intentSettings")}
          </Button>
        </div>
      ) : null}
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.name")}
        </span>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
          aria-label={t("org.department.name")}
        />
      </label>
      <label className="flex flex-col gap-1.5 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.description")}{" "}
          <span className="opacity-60">
            {t("org.chart.newAgent.optionalSuffix")}
          </span>
        </span>
        <Input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          aria-label={t("org.department.description")}
        />
      </label>
      <div className="flex flex-col gap-1.5">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatar")}
        </span>
        <div className="flex items-center gap-3">
          <AgentAvatarPicker
            name={name || t("org.agent.fallbackName")}
            avatarColor={avatarColor}
            avatarIcon={avatarIcon}
            avatarDataUrl=""
            onChangeIcon={setAvatarIcon}
            allowUpload={false}
            triggerSize="lg"
          />
          <span className="font-mono text-2xs text-muted-foreground">
            {t("org.chart.newAgent.avatarHint")}
          </span>
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatarColor")}
        </span>
        <div
          className="grid grid-cols-5 gap-2"
          role="radiogroup"
          aria-label={t("org.chart.newAgent.avatarColor")}
        >
          {agentColorOrder.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={avatarColor === c}
              aria-label={t("org.chart.newAgent.avatarColorNamed", {
                color: c,
              })}
              onClick={() => setAvatarColor(c)}
              className={cn(
                "size-6 rounded-full ring-offset-2 transition-all",
                agentColorClassNames[c],
                avatarColor === c && "size-7 ring-2 ring-primary",
              )}
            />
          ))}
        </div>
      </div>
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.placement")}
        </span>
        <Select value={placement} onValueChange={setPlacement}>
          <SelectTrigger aria-label={t("org.chart.newAgent.placement")}>
            <SelectValue placeholder={t("org.chart.newAgent.placement")} />
          </SelectTrigger>
          <SelectContent>
            {placementOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.backend")}
        </span>
        <Select
          value={backendId > 0 ? String(backendId) : ""}
          onValueChange={(v) => setBackendId(Number(v))}
        >
          <SelectTrigger aria-label={t("org.chart.newAgent.backend")}>
            <SelectValue placeholder={t("org.chart.newAgent.backend")} />
          </SelectTrigger>
          <SelectContent>
            {props.backends.map((b) => (
              <SelectItem key={b.id} value={String(b.id)}>
                {b.name} · {b.type}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
    </AgentreDialog>
  );
}

function parsePlacement(value: string): {
  departmentId: number;
  parentAgentId: number;
} {
  const [kind, rawId] = value.split(":");
  const id = Number(rawId);
  if (!Number.isFinite(id) || id <= 0) {
    return { departmentId: 0, parentAgentId: 0 };
  }
  if (kind === "department") {
    return { departmentId: id, parentAgentId: 0 };
  }
  if (kind === "agent") {
    return { departmentId: 0, parentAgentId: id };
  }
  return { departmentId: 0, parentAgentId: 0 };
}
