// 「新建部门」对话框:薄壳(控制开合)+ 对话框体(表单本体)。
// 它与「新建 agent」那个对话框原先都与组织架构页挤在一个 783 行的文件里。

import * as React from "react";
import { useTranslation } from "react-i18next";

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
import { IconPicker } from "../icon-picker";
import { type OrgAgent, type OrgDepartment } from "../org/types";

export type NewDeptProps = {
  open: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  defaultParentId?: number;
  onSubmit: (req: {
    name: string;
    description: string;
    icon: string;
    accentColor: string;
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
  const [icon, setIcon] = React.useState("hammer");
  const [accentColor, setAccentColor] = React.useState<AgentColor>("agent-2");
  const [parentId, setParentId] = React.useState<number>(
    props.defaultParentId ?? 0,
  );

  const canSubmit = name.trim().length > 0;

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    await props.onSubmit({
      name: name.trim(),
      description: "",
      icon,
      accentColor,
      parentId,
    });
  }

  return (
    <AgentreDialog
      open={props.open}
      onOpenChange={(o) => !o && props.onClose()}
      title={t("org.chart.actions.newDepartment")}
      description={t("org.chart.newDepartment.description")}
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
      <div className="flex flex-col gap-2">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.icon")}
        </span>
        <IconPicker
          value={icon}
          onChange={setIcon}
          accentColor={accentColor}
          ariaLabel={t("org.chart.newDepartment.iconAria")}
        />
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.themeColor")}
        </span>
        <div
          className="grid grid-cols-5 gap-2"
          role="radiogroup"
          aria-label={t("org.department.themeColor")}
        >
          {agentColorOrder.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={accentColor === c}
              aria-label={t("org.department.themeColorNamed", { color: c })}
              onClick={() => setAccentColor(c)}
              className={cn(
                "size-6 rounded-full ring-offset-2 transition-all",
                agentColorClassNames[c],
                accentColor === c && "size-7 ring-2 ring-primary",
              )}
            />
          ))}
        </div>
      </div>
    </AgentreDialog>
  );
}
