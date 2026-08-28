import { AlertTriangle, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  RadioGroup,
  RadioGroupItem,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

export type DeleteDepartmentStrategy = "reparent" | "cascade";

/** 删除确认：部门要先说清子节点怎么办，所以比 Agent 那一支多一组策略。 */
export function DeleteDepartmentDialog({
  open,
  departmentName,
  strategy,
  onStrategyChange,
  onOpenChange,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  departmentName: string;
  strategy: DeleteDepartmentStrategy;
  onStrategyChange: (next: DeleteDepartmentStrategy) => void;
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
                {t("org.department.deleteDialog.title", {
                  name: departmentName,
                })}
              </span>
            </DialogTitle>
            <DialogDescription>
              {t("org.department.deleteDialog.description")}
            </DialogDescription>
          </DialogHeader>
          <DialogBody className="space-y-2.5">
            <h4 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("org.department.deleteDialog.strategyTitle")}
            </h4>
            <RadioGroup
              value={strategy}
              onValueChange={(next) =>
                onStrategyChange(next as DeleteDepartmentStrategy)
              }
              aria-label={t("org.department.deleteDialog.strategyTitle")}
              className="gap-2.5"
            >
              {/* 两项各写一遍而不是 map 一张表：文案键要留成静态字面量，
                  i18n.test.ts 校验不了模板字符串拼出来的 key。 */}
              <DeleteStrategyChoice
                value="reparent"
                active={strategy === "reparent"}
                title={t("org.department.deleteDialog.reparentTitle")}
                description={t(
                  "org.department.deleteDialog.reparentDescription",
                )}
              />
              <DeleteStrategyChoice
                value="cascade"
                active={strategy === "cascade"}
                title={t("org.department.deleteDialog.cascadeTitle")}
                description={t(
                  "org.department.deleteDialog.cascadeDescription",
                )}
              />
            </RadioGroup>
          </DialogBody>
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

/**
 * 删除策略的一项：整张卡片可点，圆点是共享包的 RadioGroupItem（此前是原生
 * `<input type="radio">`，深色下走浏览器自己的配色）。文案由调用方取好再传进来，
 * 这样文案 key 留在调用点、仍是静态字面量。
 */
function DeleteStrategyChoice({
  value,
  active,
  title,
  description,
}: {
  value: DeleteDepartmentStrategy;
  active: boolean;
  title: string;
  description: string;
}) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-2.5 rounded-md border bg-card px-3 py-2.5 transition-colors",
        active ? "border-primary ring-1 ring-primary" : "border-border",
      )}
    >
      <RadioGroupItem value={value} aria-label={title} className="mt-0.5" />
      <div className="flex-1">
        <div className="text-sm font-semibold text-foreground">{title}</div>
        <div className="text-2xs text-muted-foreground">{description}</div>
      </div>
    </label>
  );
}
