import { History } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

import type { AutoSaveStatus as Status } from "./use-auto-save";

export function AutoSaveStatus({
  status,
  pendingInvalid,
  onRetry,
}: {
  status: Status;
  pendingInvalid: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const label =
    status === "error"
      ? t("common.saveFailed")
      : status === "saving"
        ? t("common.saving")
        : pendingInvalid
          ? t("common.unsavedChanges")
          : t("common.saved");

  return (
    <footer className="flex items-center gap-2 border-t border-border bg-secondary/40 px-5 py-3">
      <span className="flex flex-1 items-center gap-1.5 font-mono text-2xs text-muted-foreground">
        <History className="size-3" aria-hidden="true" />
        {label}
      </span>
      {status === "error" && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      )}
    </footer>
  );
}
