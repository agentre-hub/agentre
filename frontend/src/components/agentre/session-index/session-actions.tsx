// frontend/src/components/agentre/session-index/session-actions.tsx
//
// 会话行右键菜单里的两个破坏性动作：改名 / 删除。
// 从合并前的对话页原样搬出来（chat-page.tsx 的 pendingRename / pendingDeleteId），
// 拆成 hook + 对话框，让索引页不必自己扛这两段 state 与它们的确认弹层。
import * as React from "react";
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
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import {
  DeleteChatSession,
  RenameChatSession,
} from "../../../../wailsjs/go/app/App";

const RENAME_FORM_ID = "sidebar-rename-session-form";

export type SessionActions = {
  requestRename: (sessionID: number, title: string) => void;
  requestDelete: (sessionID: number) => void;
  pendingRename: { id: number; draft: string } | null;
  setPendingRename: React.Dispatch<
    React.SetStateAction<{ id: number; draft: string } | null>
  >;
  pendingDeleteID: number | null;
  setPendingDeleteID: React.Dispatch<React.SetStateAction<number | null>>;
  confirmRename: () => Promise<void>;
  confirmDelete: () => Promise<void>;
};

export function useSessionActions(): SessionActions {
  const [pendingRename, setPendingRename] = React.useState<{
    id: number;
    draft: string;
  } | null>(null);
  const [pendingDeleteID, setPendingDeleteID] = React.useState<number | null>(
    null,
  );

  const requestRename = React.useCallback(
    (sessionID: number, title: string) =>
      setPendingRename({ id: sessionID, draft: title }),
    [],
  );
  const requestDelete = React.useCallback(
    (sessionID: number) => setPendingDeleteID(sessionID),
    [],
  );

  const confirmRename = React.useCallback(async () => {
    if (!pendingRename) return;
    const next = pendingRename.draft.trim();
    const id = pendingRename.id;
    setPendingRename(null);
    if (!next) return;
    await RenameChatSession({ sessionId: id, title: next } as never);
    reloadSidebarSources();
  }, [pendingRename]);

  const confirmDelete = React.useCallback(async () => {
    const id = pendingDeleteID;
    if (id == null) return;
    setPendingDeleteID(null);
    await DeleteChatSession({ sessionId: id } as never);
    // 与 ChatPanel 删除后关闭对应 tab 的行为保持一致：留着一枚指向已删会话的标签页
    // 会在下一次点它时报「会话不存在」。
    const openTabIDs = useChatTabsStore
      .getState()
      .tabs.filter(
        (tab) => tab.meta.kind === "session" && tab.meta.sessionId === id,
      )
      .map((tab) => tab.id);
    for (const tabID of openTabIDs) {
      useChatTabsStore.getState().closeTab(tabID);
    }
    reloadSidebarSources();
  }, [pendingDeleteID]);

  return {
    requestRename,
    requestDelete,
    pendingRename,
    setPendingRename,
    pendingDeleteID,
    setPendingDeleteID,
    confirmRename,
    confirmDelete,
  };
}

export function SessionActionDialogs({ actions }: { actions: SessionActions }) {
  const { t } = useTranslation();
  const {
    pendingRename,
    setPendingRename,
    pendingDeleteID,
    setPendingDeleteID,
    confirmRename,
    confirmDelete,
  } = actions;

  return (
    <>
      <Dialog
        open={pendingDeleteID !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteID(null);
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
              onClick={() => setPendingDeleteID(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void confirmDelete()}
            >
              {t("common.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
              id={RENAME_FORM_ID}
              onSubmit={(e) => {
                e.preventDefault();
                void confirmRename();
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
              form={RENAME_FORM_ID}
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
