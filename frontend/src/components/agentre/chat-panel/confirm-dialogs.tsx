import type * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
} from "@agentre-hub/agentre-ui";

import type { PendingRename } from "./use-message-actions";

type ChatPanelConfirmDialogsProps = {
  pendingRegenId: number | null;
  setPendingRegenId: React.Dispatch<React.SetStateAction<number | null>>;
  onConfirmRegenerate: () => void;
  pendingDeleteId: number | null;
  setPendingDeleteId: React.Dispatch<React.SetStateAction<number | null>>;
  onConfirmDelete: () => void;
  pendingRename: PendingRename | null;
  setPendingRename: React.Dispatch<React.SetStateAction<PendingRename | null>>;
  onConfirmRename: () => void;
};

// ChatPanelConfirmDialogs:重生成 / 删除 / 改名三颗确认弹窗。三者都是受控的
// ——「开不开」与「确认之后做什么」都由 ChatPanel 那边的 useMessageActions 持有。
function ChatPanelConfirmDialogs({
  pendingRegenId,
  setPendingRegenId,
  onConfirmRegenerate,
  pendingDeleteId,
  setPendingDeleteId,
  onConfirmDelete,
  pendingRename,
  setPendingRename,
  onConfirmRename,
}: ChatPanelConfirmDialogsProps) {
  const { t } = useTranslation();
  return (
    <>
      <Dialog
        open={pendingRegenId !== null}
        onOpenChange={(open) => {
          if (!open) setPendingRegenId(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.regenerateDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-muted-foreground">
              {t("chatPanel.regenerateDialog.description")}
            </p>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingRegenId(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={() => onConfirmRegenerate()}>
              {t("chatPanel.regenerateDialog.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={pendingDeleteId !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteId(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.deleteDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-muted-foreground">
              {t("chatPanel.deleteDialog.description")}
            </p>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingDeleteId(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => onConfirmDelete()}
            >
              {t("common.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {/* Rename Dialog 取代旧 window.prompt：把 draft 提到 state 上，
          DialogClose 由 onOpenChange 统一管理（× / Esc / 取消 都走同一 setter）。 */}
      <Dialog
        open={pendingRename !== null}
        onOpenChange={(open) => {
          if (!open) setPendingRename(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.renameDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <form
              id="rename-session-form"
              onSubmit={(e) => {
                e.preventDefault();
                onConfirmRename();
              }}
            >
              <Input
                autoFocus
                value={pendingRename?.draft ?? ""}
                onChange={(e) =>
                  setPendingRename((prev) =>
                    prev ? { ...prev, draft: e.target.value } : prev,
                  )
                }
                placeholder={t("chatPanel.renameDialog.placeholder")}
                aria-label={t("chatPanel.renameDialog.nameAria")}
              />
            </form>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingRename(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form="rename-session-form"
              size="sm"
              disabled={
                !pendingRename || pendingRename.draft.trim().length === 0
              }
            >
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export { ChatPanelConfirmDialogs };
export type { ChatPanelConfirmDialogsProps };
