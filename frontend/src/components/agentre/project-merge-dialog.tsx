import * as React from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { ProjectMerge } from "../../../wailsjs/go/app/App";
import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agentre-hub/agentre-ui";

export type ProjectMergeCandidate = { id: number; name: string };
export type ProjectMergeSource = { id: number; name: string };

/**
 * ProjectMergeDialog —— R11a「合并到已有项目」：把 source 合并进用户选定的另一个
 * 本地项目。哪一边最终"赢"（同步标识 / 本机路径）由后端 project_svc.Merge 决定，
 * 这里只负责收集"合并到哪一个"并展示后果，不做客户端猜测。
 */
export function ProjectMergeDialog({
  source,
  candidates,
  onClose,
  onMerged,
}: {
  source: ProjectMergeSource | null;
  candidates: ProjectMergeCandidate[];
  onClose: () => void;
  onMerged: () => void;
}) {
  const { t } = useTranslation();
  const [targetID, setTargetID] = React.useState<number | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    setTargetID(null);
    setBusy(false);
    setErr(null);
  }, [source?.id]);

  const handleMerge = async () => {
    if (source === null || targetID === null) return;
    setErr(null);
    setBusy(true);
    try {
      await ProjectMerge({ sourceID: source.id, targetID });
      onMerged();
    } catch (e) {
      setErr(t("projectMerge.errors.mergeFailed", { error: String(e) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={source !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("projectMerge.title")}</DialogTitle>
        </DialogHeader>
        <DialogBody className="flex flex-col gap-3">
          <p className="text-2xs text-muted-foreground">
            {t("projectMerge.description")}
          </p>
          <label className="flex flex-col gap-1.5 text-xs">
            <span className="font-medium text-foreground">
              {t("projectMerge.pickTargetLabel")}
            </span>
            {candidates.length === 0 ? (
              <p className="text-2xs text-muted-foreground">
                {t("projectMerge.noCandidates")}
              </p>
            ) : (
              <Select
                value={targetID ? String(targetID) : ""}
                onValueChange={(v) => setTargetID(Number(v))}
              >
                <SelectTrigger className="h-9 text-xs">
                  <SelectValue
                    placeholder={t("projectMerge.pickTargetPlaceholder")}
                  />
                </SelectTrigger>
                <SelectContent>
                  {candidates.map((c) => (
                    <SelectItem key={c.id} value={String(c.id)}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </label>
          <div className="flex flex-col gap-1.5 rounded-md bg-muted p-3">
            <div className="flex items-baseline gap-2">
              <span
                aria-hidden="true"
                className="mt-1.5 size-1.5 shrink-0 rounded-full bg-status-running"
              />
              <p className="text-2xs leading-relaxed">
                {t("projectMerge.keepPathNote")}
              </p>
            </div>
            <div className="flex items-baseline gap-2">
              <span
                aria-hidden="true"
                className="mt-1.5 size-1.5 shrink-0 rounded-full bg-status-running"
              />
              <p className="text-2xs leading-relaxed">
                {t("projectMerge.reassignNote")}
              </p>
            </div>
            <div className="flex items-baseline gap-2">
              <span
                aria-hidden="true"
                className="mt-1.5 size-1.5 shrink-0 rounded-full bg-status-idle"
              />
              <p className="text-2xs leading-relaxed">
                {t("projectMerge.identityNote")}
              </p>
            </div>
          </div>
          {err ? (
            <div className="rounded-md border border-destructive bg-destructive-soft px-3 py-2 text-2xs text-destructive">
              {err}
            </div>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={onClose}
          >
            {t("projectMerge.cancel")}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={!targetID || busy}
            onClick={() => void handleMerge()}
          >
            {busy ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {t("projectMerge.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
