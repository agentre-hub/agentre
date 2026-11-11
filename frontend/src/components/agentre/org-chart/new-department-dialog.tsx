// 「新建部门」对话框:薄壳(控制开合)+ 对话框体(表单本体)。

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
import { type OrgAgent, type OrgDepartment } from "../org/types";
import { WriteFailureLine } from "../org/write-failure-line";
import { useCreateSubmit } from "./create-submit";

export type NewDeptProps = {
  open: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  defaultParentId?: number;
  // 共享身份区只改草稿；没设过的字段（描述、被清空的图标）不进请求。
  onSubmit: (req: {
    name: string;
    description?: string;
    icon?: string;
    accentColor?: string;
    parentId: number;
  }) => Promise<void>;
  onClose: () => void;
};

export function NewDepartmentDialog(props: NewDeptProps) {
  if (!props.open) return null;
  return <NewDepartmentDialogBody {...props} />;
}

export function NewDepartmentDialogBody(props: NewDeptProps) {
  const { t } = useTranslation();
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  // 默认值不变：hammer 图标 + agent-2 主题色。
  const [icon, setIcon] = React.useState("hammer");
  const [accentColor, setAccentColor] = React.useState("agent-2");
  const initialParentId = props.defaultParentId ?? 0;
  const [parentId, setParentId] = React.useState<number>(initialParentId);
  const { busy, failure, run } = useCreateSubmit(
    t("org.chart.errors.createDepartmentFailed"),
  );

  const canSubmit = name.trim().length > 0;
  // 填过任何一格就算有草稿：点遮罩不再丢掉它（Esc 与 × 照常关）。
  const dirty =
    name !== "" ||
    description !== "" ||
    icon !== "hammer" ||
    accentColor !== "agent-2" ||
    parentId !== initialParentId;

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    const trimmedDescription = description.trim();
    await run(() =>
      props.onSubmit({
        name: name.trim(),
        ...(trimmedDescription ? { description: trimmedDescription } : {}),
        ...(icon ? { icon } : {}),
        accentColor,
        parentId,
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
        title={t("org.chart.actions.newDepartment")}
        subtitle={t("org.chart.newDepartment.description")}
        onClose={props.onClose}
        busy={busy}
      />
      <DialogShellBody className="flex flex-col gap-4">
        <IdentityFields
          kind="department"
          testIdPrefix="new-department"
          name={name}
          description={description}
          onNameChange={setName}
          onDescriptionChange={setDescription}
          autoFocusName
          mark={{
            iconKey: icon,
            color: accentColor,
            onPickIcon: setIcon,
            onPickColor: (color) => setAccentColor(safeAgentColor(color)),
          }}
        />
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-2xs text-muted-foreground">
            {t("org.department.parent")}
          </span>
          <Select
            value={String(parentId)}
            onValueChange={(v) => setParentId(Number(v))}
          >
            <SelectTrigger aria-label={t("org.department.parent")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="0">{t("org.department.topLevel")}</SelectItem>
              {props.departments.map((d) => (
                <SelectItem key={d.id} value={String(d.id)}>
                  {d.name}
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
