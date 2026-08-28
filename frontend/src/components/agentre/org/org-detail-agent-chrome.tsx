import { AlertTriangle, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";

import { AgentAvatarPicker } from "../icon-picker";
import type { AgentColor } from "../types";

import type { OrgAgent, OrgDepartment } from "./types";

/** Agent 详情的顶栏：头像 / 名字 / 主管徽标 + 删除、关闭两颗按钮。 */
export function OrgDetailAgentHeader({
  agent,
  name,
  avatarColor,
  avatarIcon,
  isLeadOf,
  isCEO,
  onChangeIcon,
  onDelete,
  onClose,
}: {
  agent: OrgAgent;
  name: string;
  avatarColor: AgentColor;
  avatarIcon: string;
  isLeadOf: OrgDepartment | null;
  isCEO: boolean;
  onChangeIcon: (icon: string) => void;
  onDelete: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <header className="flex shrink-0 items-start gap-3 border-b border-border bg-card px-5 py-4">
      <AgentAvatarPicker
        name={name || agent.name}
        avatarColor={avatarColor}
        avatarIcon={avatarIcon}
        avatarDataUrl={agent.avatarDataUrl}
        onChangeIcon={onChangeIcon}
        showImageMode={false}
        triggerSize="lg"
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-base font-semibold">{agent.name}</span>
        {isLeadOf && (
          <span className="truncate font-mono text-2xs text-primary-text">
            {t("org.agent.departmentLead", { name: isLeadOf.name })}
          </span>
        )}
      </div>
      <div className="flex shrink-0 gap-1">
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          disabled={isCEO}
          aria-label={t("org.agent.actions.deleteAgent")}
          onClick={onDelete}
        >
          <Trash2 className="size-4 text-destructive" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          className="size-8"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>
    </header>
  );
}

/** 删除确认：Agent 侧没有策略可选，问一句就够。 */
export function DeleteAgentDialog({
  open,
  agentName,
  onOpenChange,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  agentName: string;
  onOpenChange: (open: boolean) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open && (
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle
                className="size-[18px] text-destructive"
                aria-hidden="true"
              />
              <span>
                {t("org.agent.deleteDialog.title", { name: agentName })}
              </span>
            </DialogTitle>
            <DialogDescription>
              {t("org.agent.deleteDialog.description")}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <span className="mr-auto font-mono text-2xs text-muted-foreground">
              {t("org.department.deleteDialog.irreversible")}
            </span>
            <Button variant="outline" size="sm" onClick={onCancel}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" size="sm" onClick={onConfirm}>
              <Trash2 className="size-3.5" />
              {t("org.department.deleteDialog.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  );
}
