import * as React from "react";
import { Upload } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@agentre-hub/agentre-ui";

import {
  ApplyImportData,
  PreviewImportData,
} from "../../../../wailsjs/go/app/App";
import { ImportPreviewDialog, type PreviewItem } from "./import-preview-dialog";
import type { ItemAction } from "./types";

export function ImportSection({
  onResult,
}: {
  onResult: (counts: Record<string, number>) => void;
}) {
  const { t } = useTranslation();
  const [preview, setPreview] = React.useState<{
    secretsIncluded: boolean;
    items: PreviewItem[];
  } | null>(null);
  const [open, setOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  const pick = async () => {
    setBusy(true);
    try {
      const pv = await PreviewImportData();
      if (!pv) return; // canceled
      setPreview({
        secretsIncluded: pv.secretsIncluded,
        items: pv.items as PreviewItem[],
      });
      setOpen(true);
    } catch (e) {
      toast.error(t("dataBackup.import.parseFailed"), {
        description: String(e),
      });
    } finally {
      setBusy(false);
    }
  };

  const apply = async (
    actions: Record<string, ItemAction>,
    fallback: ItemAction,
  ) => {
    try {
      const res = await ApplyImportData({
        actions,
        fallbackStrategy: fallback,
      });
      setOpen(false);
      onResult(res.counts ?? {});
    } catch (e) {
      toast.error(t("dataBackup.import.failed"), { description: String(e) });
    }
  };

  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-3">
      <header>
        <h2 className="text-sm font-semibold">
          {t("dataBackup.import.title")}
        </h2>
        <p className="text-xs text-muted-foreground">
          {t("dataBackup.import.description")}
        </p>
      </header>
      <Button onClick={pick} disabled={busy}>
        <Upload className="size-4 mr-2" />
        {t("dataBackup.import.chooseFile")}
      </Button>
      <ImportPreviewDialog
        open={open}
        onOpenChange={setOpen}
        preview={preview}
        onApply={apply}
      />
    </section>
  );
}
