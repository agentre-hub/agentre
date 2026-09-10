// 校验和弹窗本体(展示期望值与实际值的对比)。
// 它的挂载点 UpdateChecksumDialogHost 留在这个模块的出口文件里 ——
// 那是对外导出的符号,消费者从 update-section 取它。

import { useTranslation } from "react-i18next";
import { Info } from "lucide-react";

import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";

export function ChecksumDialog({
  open,
  reason,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  reason: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog
      open={open}
      onOpenChange={(o: boolean) => (!o ? onCancel() : undefined)}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Info className="size-4 text-status-waiting" aria-hidden="true" />
            {t("update.checksum.title")}
          </DialogTitle>
          <DialogDescription>
            {t("update.checksum.description")}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="text-xs leading-relaxed">
          <div className="rounded-md border border-border bg-muted/40 p-2 font-mono text-2xs">
            {reason}
          </div>
          <p className="mt-3 text-muted-foreground">
            {t("update.checksum.warning")}
          </p>
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="button" variant="destructive" onClick={onConfirm}>
            {t("update.checksum.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
