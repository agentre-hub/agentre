import * as React from "react";
import { useTranslation } from "react-i18next";

import { CleanupSection } from "./cleanup-section";
import { ExportSection } from "./export-section";
import { ImportResultDialog } from "./import-result-dialog";
import { ImportSection } from "./import-section";

export function DataBackupPanel() {
  const { t } = useTranslation();
  const [result, setResult] = React.useState<Record<string, number> | null>(
    null,
  );
  return (
    <>
      <div className="flex max-w-3xl flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">
          {t("dataBackup.title")}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {t("dataBackup.description")}
        </p>
      </div>
      <ExportSection />
      <ImportSection onResult={setResult} />
      <CleanupSection />
      <ImportResultDialog counts={result} onClose={() => setResult(null)} />
    </>
  );
}
