import * as React from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import {
  Alert,
  AlertDescription,
  Badge,
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@agentre-hub/agentre-ui";
import { ShieldAlert } from "lucide-react";

import type { ItemAction, Scope } from "./types";

export type PreviewItem = {
  scope: Scope;
  sourceKey: string;
  name: string;
  conflict?: boolean;
  localID?: number;
  localName?: string;
  dangling?: boolean;
  danglingHint?: string;
  defaultAction: ItemAction;
};

export type ImportPreviewDialogProps = {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  preview: { secretsIncluded: boolean; items: PreviewItem[] } | null;
  onApply: (
    actions: Record<string, ItemAction>,
    fallback: ItemAction,
  ) => Promise<void>;
};

function actionLabel(action: ItemAction, t: TFunction): string {
  return t(`dataBackup.actions.${action}`);
}

function scopeLabel(scope: Scope, t: TFunction): string {
  return t(`dataBackup.scopes.${scope}`);
}

export function ImportPreviewDialog({
  open,
  onOpenChange,
  preview,
  onApply,
}: ImportPreviewDialogProps) {
  const { t } = useTranslation();
  const [global, setGlobal] = React.useState<ItemAction>("skip");
  const [actions, setActions] = React.useState<Record<string, ItemAction>>({});

  React.useEffect(() => {
    if (!preview) return;
    const next: Record<string, ItemAction> = {};
    for (const it of preview.items) {
      next[`${it.scope}:${it.sourceKey}`] = it.defaultAction;
    }
    setActions(next);
  }, [preview]);

  if (!preview) return null;

  const setOne = (key: string, v: ItemAction) =>
    setActions((p) => ({ ...p, [key]: v }));

  const setAllConflicts = (v: ItemAction) => {
    setGlobal(v);
    setActions((prev) => {
      const next = { ...prev };
      for (const it of preview.items) {
        if (it.conflict && !it.dangling) {
          next[`${it.scope}:${it.sourceKey}`] = v;
        }
      }
      return next;
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("dataBackup.importPreview.title")}</DialogTitle>
          <DialogDescription>
            {t("dataBackup.importPreview.description")}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="flex max-h-[70vh] flex-col gap-3 overflow-hidden">
          {!preview.secretsIncluded && (
            <Alert>
              <ShieldAlert className="size-4" />
              <AlertDescription>
                {t("dataBackup.importPreview.noSecrets")}
              </AlertDescription>
            </Alert>
          )}
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">
              {t("dataBackup.importPreview.defaultStrategy")}
            </span>
            <Select
              value={global}
              onValueChange={(v) => setAllConflicts(v as ItemAction)}
            >
              <SelectTrigger className="w-[160px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="skip">{actionLabel("skip", t)}</SelectItem>
                <SelectItem value="overwrite">
                  {actionLabel("overwrite", t)}
                </SelectItem>
                <SelectItem value="duplicate">
                  {actionLabel("duplicate", t)}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("dataBackup.importPreview.scope")}</TableHead>
                  <TableHead>{t("org.department.name")}</TableHead>
                  <TableHead>{t("org.list.sort.status")}</TableHead>
                  <TableHead>{t("dataBackup.importPreview.action")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {preview.items.map((it) => {
                  const key = `${it.scope}:${it.sourceKey}`;
                  const cur = actions[key] ?? it.defaultAction;
                  return (
                    <TableRow key={key}>
                      <TableCell className="text-xs">
                        {scopeLabel(it.scope, t)}
                      </TableCell>
                      <TableCell>{it.name}</TableCell>
                      <TableCell>
                        {it.dangling ? (
                          <Badge variant="destructive">
                            {t("dataBackup.importPreview.dangling")}
                          </Badge>
                        ) : it.conflict ? (
                          <Badge variant="secondary">
                            {t("dataBackup.importPreview.conflict")}
                          </Badge>
                        ) : (
                          <Badge>{t("dataBackup.importPreview.new")}</Badge>
                        )}
                        {it.danglingHint && (
                          <div className="text-xs text-destructive mt-1">
                            {it.danglingHint}
                          </div>
                        )}
                      </TableCell>
                      <TableCell>
                        <Select
                          value={cur}
                          disabled={it.dangling}
                          onValueChange={(v) => setOne(key, v as ItemAction)}
                        >
                          <SelectTrigger className="w-[120px]">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {!it.conflict && (
                              <SelectItem value="create">
                                {actionLabel("create", t)}
                              </SelectItem>
                            )}
                            {it.conflict && (
                              <SelectItem value="overwrite">
                                {actionLabel("overwrite", t)}
                              </SelectItem>
                            )}
                            <SelectItem value="skip">
                              {actionLabel("skip", t)}
                            </SelectItem>
                            {!it.dangling && (
                              <SelectItem value="duplicate">
                                {actionLabel("duplicate", t)}
                              </SelectItem>
                            )}
                          </SelectContent>
                        </Select>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => onApply(actions, global)}>
            {t("dataBackup.importPreview.apply")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
