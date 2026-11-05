import * as React from "react";
import { Upload } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@agentre-hub/agentre-ui";

import { classifyAppError } from "@/lib/app-error";

import {
  ApplyImportData,
  PreviewImportData,
} from "../../../../wailsjs/go/app/App";
import { FailureBody } from "./failure-body";
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

  // 失败说中文事实：不是备份 → 「这个文件不是 Agentre 导出的备份」；其余统一
  // 「导入失败」。后端那句人话作正文，机器原话只进折叠的「详情」。
  const reportFailure = (e: unknown, offerRepick: boolean) => {
    const failure = classifyAppError(e);
    const notBackup = failure.kind === "invalidFile";
    toast.error(
      notBackup
        ? t("dataBackup.import.notBackup")
        : t("dataBackup.import.failed"),
      {
        description: (
          <FailureBody message={failure.message} detail={failure.detail} />
        ),
        action:
          offerRepick || notBackup
            ? {
                label: t("dataBackup.import.pickAgain"),
                onClick: () => void pick(),
              }
            : undefined,
      },
    );
  };

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
      // 选中的文件读不了 / 不是备份：不管哪一类，出口都是换一个文件。
      reportFailure(e, true);
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
      reportFailure(e, false);
    }
  };

  return (
    <section className="rounded-lg bg-field-fill p-4 space-y-3">
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
