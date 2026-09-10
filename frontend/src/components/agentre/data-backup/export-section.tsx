import * as React from "react";
import { Download, ShieldAlert } from "lucide-react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  Alert,
  AlertDescription,
  Button,
  Checkbox,
  Switch,
} from "@agentre-hub/agentre-ui";

import { ExportData } from "../../../../wailsjs/go/app/App";
import type { Scope } from "./types";

const ALL_SCOPES: Scope[] = [
  "llm-providers",
  "agent-backends",
  "organization",
  "remote-devices",
];

function scopeLabel(scope: Scope, t: TFunction): string {
  return t(`dataBackup.scopes.${scope}`);
}

export function ExportSection() {
  const { t } = useTranslation();
  const [selected, setSelected] = React.useState<Set<Scope>>(
    new Set(ALL_SCOPES),
  );
  const [includeSecrets, setIncludeSecrets] = React.useState(false);
  const [running, setRunning] = React.useState(false);

  const toggle = (s: Scope) => {
    const next = new Set(selected);
    if (next.has(s)) {
      next.delete(s);
    } else {
      next.add(s);
    }
    setSelected(next);
  };

  const handleExport = async () => {
    if (selected.size === 0) {
      toast.error(t("dataBackup.export.selectOne"));
      return;
    }
    setRunning(true);
    try {
      const res = await ExportData({
        scopes: Array.from(selected),
        includeSecrets,
      });
      if (res.canceled) return;
      const total = Object.values(res.summary ?? {}).reduce((a, b) => a + b, 0);
      toast.success(t("dataBackup.export.success", { count: total }), {
        description: res.path,
      });
    } catch (e) {
      toast.error(t("dataBackup.export.failed"), { description: String(e) });
    } finally {
      setRunning(false);
    }
  };

  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-4">
      <header>
        <h2 className="text-sm font-semibold">
          {t("dataBackup.export.title")}
        </h2>
        <p className="text-xs text-muted-foreground">
          {t("dataBackup.export.description")}
        </p>
      </header>
      <div className="grid grid-cols-2 gap-2">
        {ALL_SCOPES.map((s) => (
          <label key={s} className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={selected.has(s)}
              onCheckedChange={() => toggle(s)}
              aria-label={scopeLabel(s, t)}
            />
            {scopeLabel(s, t)}
          </label>
        ))}
      </div>
      <div className="flex items-center justify-between rounded-md border border-border p-3">
        <div>
          <div className="text-sm font-medium">
            {t("dataBackup.export.includeSecrets")}
          </div>
          <div className="text-xs text-muted-foreground">
            {t("dataBackup.export.includeSecretsDescription")}
          </div>
        </div>
        <Switch checked={includeSecrets} onCheckedChange={setIncludeSecrets} />
      </div>
      {includeSecrets && (
        <Alert>
          <ShieldAlert className="size-4" />
          <AlertDescription>
            {t("dataBackup.export.secretsWarning")}
          </AlertDescription>
        </Alert>
      )}
      <Button onClick={handleExport} disabled={running}>
        <Download className="size-4 mr-2" />
        {t("dataBackup.export.action")}
      </Button>
    </section>
  );
}
