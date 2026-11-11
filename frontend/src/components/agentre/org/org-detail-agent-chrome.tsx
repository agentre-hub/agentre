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
  IdentityFields,
  type IdentityFieldsProps,
} from "@agentre-hub/agentre-ui";

import { WriteFailureLine, type WriteFailure } from "./write-failure-line";
import type { OrgDepartment } from "./types";

/** Agent 详情的唯一身份区：可编辑身份 / 主管信息 + 删除、关闭。 */
export function OrgDetailAgentHeader({
  identity,
  isLeadOf,
  isCEO,
  onDelete,
  onClose,
}: {
  identity: Omit<IdentityFieldsProps, "kind" | "testIdPrefix" | "meta">;
  isLeadOf: OrgDepartment | null;
  isCEO: boolean;
  onDelete: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <header className="flex shrink-0 items-start gap-3 px-5 py-4">
      <div className="min-w-0 flex-1">
        <IdentityFields
          {...identity}
          kind="agent"
          testIdPrefix="agent-detail"
          meta={
            isLeadOf ? (
              <span className="block truncate font-mono text-2xs text-primary-text">
                {t("org.agent.departmentLead", { name: isLeadOf.name })}
              </span>
            ) : null
          }
        />
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
  failure = null,
  onOpenChange,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  agentName: string;
  /** 上一次确认没删掉：页脚说这一句，框不关。 */
  failure?: WriteFailure | null;
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
            {failure ? (
              <WriteFailureLine failure={failure} className="mr-auto min-w-0" />
            ) : (
              <span className="mr-auto font-mono text-2xs text-muted-foreground">
                {t("org.department.deleteDialog.irreversible")}
              </span>
            )}
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
