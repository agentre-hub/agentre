// frontend/src/components/agentre/session-index/session-actions.tsx
//
// 会话行右键菜单里的两个破坏性动作：改名 / 删除。
// 从合并前的对话页原样搬出来（chat-page.tsx 的 pendingRename / pendingDeleteId），
// 拆成 hook + 对话框，让索引页不必自己扛这两段 state 与它们的确认弹层。
import * as React from "react";
import { Loader2 } from "lucide-react";
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
import { classifyAppError, type AppError } from "@/lib/app-error";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { FailureBody } from "../failure-body";

import {
  DeleteChatSession,
  RenameChatSession,
} from "../../../../wailsjs/go/app/App";

const RENAME_FORM_ID = "sidebar-rename-session-form";

type PendingRename = { id: number; draft: string };
/** title 是点「删除」那一刻行上的标题：确认框要说清删的是哪一条。 */
type PendingDelete = { id: number; title: string };

export type SessionActions = {
  requestRename: (sessionID: number, title: string) => void;
  requestDelete: (sessionID: number) => void;
  pendingRename: PendingRename | null;
  setRenameDraft: (draft: string) => void;
  pendingDelete: PendingDelete | null;
  /** 关窗。写入进行中不关 —— 成没成要在窗里交代。 */
  closeRename: () => void;
  closeDelete: () => void;
  confirmRename: () => Promise<void>;
  confirmDelete: () => Promise<void>;
  renameBusy: boolean;
  deleteBusy: boolean;
  renameError: AppError | null;
  deleteError: AppError | null;
};

export function useSessionActions(): SessionActions {
  const [pendingRename, setPendingRename] =
    React.useState<PendingRename | null>(null);
  const [pendingDelete, setPendingDelete] =
    React.useState<PendingDelete | null>(null);
  const [renameBusy, setRenameBusy] = React.useState(false);
  const [deleteBusy, setDeleteBusy] = React.useState(false);
  const [renameError, setRenameError] = React.useState<AppError | null>(null);
  const [deleteError, setDeleteError] = React.useState<AppError | null>(null);

  const requestRename = React.useCallback(
    (sessionID: number, title: string) => {
      setRenameError(null);
      setPendingRename({ id: sessionID, draft: title });
    },
    [],
  );
  const requestDelete = React.useCallback((sessionID: number) => {
    setDeleteError(null);
    setPendingDelete({
      id: sessionID,
      // 行上的标题就来自这份 meta（use-group-rows），确认框与行说的是同一个名字。
      title: useSessionMetaStore.getState().metas.get(sessionID)?.title ?? "",
    });
  }, []);

  const setRenameDraft = React.useCallback(
    (draft: string) =>
      setPendingRename((prev) => (prev ? { ...prev, draft } : prev)),
    [],
  );
  const closeRename = React.useCallback(() => {
    if (!renameBusy) setPendingRename(null);
  }, [renameBusy]);
  const closeDelete = React.useCallback(() => {
    if (!deleteBusy) setPendingDelete(null);
  }, [deleteBusy]);

  const confirmRename = React.useCallback(async () => {
    if (!pendingRename || renameBusy) return;
    const next = pendingRename.draft.trim();
    if (!next) {
      setPendingRename(null);
      return;
    }
    setRenameError(null);
    setRenameBusy(true);
    try {
      await RenameChatSession({
        sessionId: pendingRename.id,
        title: next,
      } as never);
    } catch (e) {
      // 留窗、留草稿：用户改一下或原样再点一次就是重试。
      setRenameError(classifyAppError(e));
      return;
    } finally {
      setRenameBusy(false);
    }
    setPendingRename(null);
    reloadSidebarSources();
  }, [pendingRename, renameBusy]);

  const confirmDelete = React.useCallback(async () => {
    if (!pendingDelete || deleteBusy) return;
    const { id } = pendingDelete;
    setDeleteError(null);
    setDeleteBusy(true);
    try {
      await DeleteChatSession({ sessionId: id } as never);
    } catch (e) {
      // 没删掉：标签页照留，窗里说清楚。
      setDeleteError(classifyAppError(e));
      return;
    } finally {
      setDeleteBusy(false);
    }
    setPendingDelete(null);
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
  }, [pendingDelete, deleteBusy]);

  return {
    requestRename,
    requestDelete,
    pendingRename,
    setRenameDraft,
    pendingDelete,
    closeRename,
    closeDelete,
    confirmRename,
    confirmDelete,
    renameBusy,
    deleteBusy,
    renameError,
    deleteError,
  };
}

/**
 * 写入失败（S5）：先说事实；后端带码的那句人话作正文，机器原话只进默认折叠的「详情」。
 */
function ActionFailure({ title, err }: { title: string; err: AppError }) {
  return (
    <div
      role="alert"
      className="flex flex-col gap-1 rounded-md bg-destructive-soft px-3 py-2 text-2xs text-destructive-text"
    >
      <p className="font-medium">{title}</p>
      <FailureBody message={err.message} detail={err.detail} />
    </div>
  );
}

export function SessionActionDialogs({ actions }: { actions: SessionActions }) {
  const { t } = useTranslation();
  const {
    pendingRename,
    setRenameDraft,
    pendingDelete,
    closeRename,
    closeDelete,
    confirmRename,
    confirmDelete,
    renameBusy,
    deleteBusy,
    renameError,
    deleteError,
  } = actions;

  return (
    <>
      <Dialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) closeDelete();
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.deleteDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody className="flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">
              {t("sessionIndex.deleteDialog.description", {
                title:
                  pendingDelete?.title || t("sessionIndex.untitledSession"),
              })}
            </p>
            {deleteError ? (
              <ActionFailure
                title={t("sessionIndex.deleteDialog.failed")}
                err={deleteError}
              />
            ) : null}
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              disabled={deleteBusy}
              onClick={closeDelete}
            >
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={deleteBusy}
              onClick={() => void confirmDelete()}
            >
              {deleteBusy ? (
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              ) : null}
              {t("common.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={pendingRename !== null}
        onOpenChange={(open) => {
          if (!open) closeRename();
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.renameDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody className="flex flex-col gap-3">
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
                readOnly={renameBusy}
                onChange={(e) => setRenameDraft(e.target.value)}
                placeholder={t("chatPanel.renameDialog.placeholder")}
                aria-label={t("chatPanel.renameDialog.nameAria")}
              />
            </form>
            {renameError ? (
              <ActionFailure
                title={t("sessionIndex.renameDialog.failed")}
                err={renameError}
              />
            ) : null}
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              disabled={renameBusy}
              onClick={closeRename}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form={RENAME_FORM_ID}
              size="sm"
              disabled={
                renameBusy ||
                !pendingRename ||
                pendingRename.draft.trim().length === 0
              }
            >
              {renameBusy ? (
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              ) : null}
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
