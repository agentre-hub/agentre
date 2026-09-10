import { CheckCircle2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";

export function ImportResultDialog({
  counts,
  onClose,
}: {
  counts: Record<string, number> | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  if (!counts) return null;
  const open = counts !== null;
  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CheckCircle2 className="size-5 text-primary" />
            {t("dataBackup.importResult.title")}
          </DialogTitle>
        </DialogHeader>
        <ul className="space-y-1 text-sm">
          {Object.entries(counts).map(([k, v]) => (
            <li key={k}>
              <span className="text-muted-foreground">
                {t(`dataBackup.importResult.${k}`, { defaultValue: k })}:
              </span>{" "}
              {v}
            </li>
          ))}
        </ul>
        <DialogFooter>
          <Button onClick={onClose}>{t("dataBackup.importResult.ok")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
