// 「新建 agent」对话框:薄壳 + 对话框体(表单本体,含部门选择与权限位)。

import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
  IdentityFields,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agentre-hub/agentre-ui";
import { safeAgentColor } from "../org/types";
import { type AgentColor } from "../types";
import { openSettings } from "@/stores/settings-dialog-store";
import { type OrgAgent, type OrgDepartment } from "../org/types";
import { useOrgData } from "../org/use-org-data";
import { WriteFailureLine } from "../org/write-failure-line";
import { useCreateSubmit } from "./create-submit";

export type NewAgentProps = {
  open: boolean;
  fromIntent?: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: ReturnType<typeof useOrgData>["backends"];
  defaultDepartmentId?: number;
  onSubmit: (req: {
    name: string;
    description?: string;
    avatarColor: string;
    avatarIcon?: string;
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
  // 草稿的基线是这次打开时落进表单的那两个初值，不随之后的列表重拉变：后台一次
  // 重拉把别的部门 / 后端排到第一位，不等于用户填过东西。
  const [opened] = React.useState({ placement, backendId });
  const parsedPlacement = parsePlacement(placement);
  const { busy, failure, run } = useCreateSubmit(
    t("org.chart.errors.createAgentFailed"),
  );

  const canSubmit =
    name.trim().length > 0 &&
    (parsedPlacement.departmentId > 0 || parsedPlacement.parentAgentId > 0);
  // 填过任何一格就算有草稿：点遮罩不再丢掉它（Esc 与 × 照常关）。
  const dirty =
    name !== "" ||
    description !== "" ||
    avatarIcon !== "" ||
    avatarColor !== "agent-1" ||
    placement !== opened.placement ||
    backendId !== opened.backendId;

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    await run(() =>
      props.onSubmit({
        name: name.trim(),
        ...(description.trim() ? { description: description.trim() } : {}),
        avatarColor,
        ...(avatarIcon ? { avatarIcon } : {}),
        departmentId: parsedPlacement.departmentId,
        parentAgentId: parsedPlacement.parentAgentId,
        agentBackendId: backendId,
        prompt: [],
        skills: [],
      }),
    );
  }

  return (
    <DialogShell
      open={props.open}
      onOpenChange={(o) => !o && props.onClose()}
      onSubmit={handleSubmit}
      busy={busy}
      dirty={dirty}
    >
      <DialogShellHeader
        title={t("org.chart.actions.newAgent")}
        subtitle={t("org.chart.newAgent.description")}
        onClose={props.onClose}
        busy={busy}
      />
      <DialogShellBody className="flex flex-col gap-4">
        {props.fromIntent ? (
          <div className="flex items-center justify-between gap-3 rounded-md bg-primary-soft px-3 py-2 text-2xs text-muted-foreground">
            <span>{t("org.chart.newAgent.intentHint")}</span>
            <Button
              type="button"
              variant="link"
              size="sm"
              className="h-auto px-0 text-2xs"
              disabled={busy}
              onClick={() => {
                // 先关掉本对话框再开设置，不叠两层。
                props.onClose();
                openSettings("agent-backend");
              }}
            >
              {t("org.chart.newAgent.intentSettings")}
            </Button>
          </div>
        ) : null}
        <IdentityFields
          kind="agent"
          testIdPrefix="new-agent"
          name={name}
          description={description}
          onNameChange={setName}
          onDescriptionChange={setDescription}
          autoFocusName
          mark={{
            iconKey: avatarIcon,
            color: avatarColor,
            onPickIcon: setAvatarIcon,
            onPickColor: (color) => setAvatarColor(safeAgentColor(color)),
            upload: "after-create",
          }}
        />
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
      </DialogShellBody>
      <DialogShellFooter
        // 外壳的 `error` 只收字符串、带不了 `title`，所以走 `left`。
        left={failure ? <WriteFailureLine failure={failure} /> : null}
      >
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={props.onClose}
        >
          {t("common.cancel")}
        </Button>
        <DialogShellSubmit type="submit" busy={busy} disabled={!canSubmit}>
          {t("common.create")}
        </DialogShellSubmit>
      </DialogShellFooter>
    </DialogShell>
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
