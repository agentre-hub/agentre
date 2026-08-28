import type * as React from "react";
import {
  ImportSessionDialog,
  type ImportDialogPrefill,
} from "@agentre-hub/agentre-ui";

import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { DeleteProjectDialog } from "../delete-project-dialog";
import { NotChattableDialog } from "../not-chattable/not-chattable-dialog";
import { ProjectMergeDialog } from "../project-merge-dialog";
import { ProjectNewDialog } from "../project-new-dialog";
import { ProjectSettingsDrawer } from "../project-settings-drawer";

import type { SessionActions } from "./session-actions";
import { SessionActionDialogs } from "./session-actions";
import type { useSessionImportPorts } from "./import-ports-desktop";
import type { IndexDialogsState } from "./use-index-dialogs";

import type { app } from "../../../../wailsjs/go/models";

type IndexDialogsProps = {
  dialogs: IndexDialogsState;
  sessionActions: SessionActions;
  tree: app.ProjectTreeNode[];
  projectByID: Map<number, app.ProjectItem>;
  /** null = 导入对话框没开。入口按自己那一维预填，所以状态本身就是那份预填。*/
  importPrefill: ImportDialogPrefill | null;
  setImportPrefill: React.Dispatch<
    React.SetStateAction<ImportDialogPrefill | null>
  >;
  importPorts: ReturnType<typeof useSessionImportPorts>;
  openSession: (sessionID: number) => void;
};

// IndexDialogs:左栏挂着的那一排对话框 / 抽屉。全部受控 —— 开关态住在
// useIndexDialogs，这里只负责摆出来。
function IndexDialogs({
  dialogs,
  sessionActions,
  tree,
  projectByID,
  importPrefill,
  setImportPrefill,
  importPorts,
  openSession,
}: IndexDialogsProps) {
  // mergeSource 解构出来:留在 dialogs.mergeSource 上时,三元收窄穿不过属性访问,
  // 只能补一个原文没有的 `!`。
  const { mergeSource } = dialogs;
  return (
    <>
      <SessionActionDialogs actions={sessionActions} />
      {dialogs.notChattableAgent ? (
        <NotChattableDialog
          agent={dialogs.notChattableAgent}
          open
          onOpenChange={(open) => {
            if (!open) dialogs.setNotChattableAgent(null);
          }}
        />
      ) : null}
      <ProjectNewDialog
        open={dialogs.newDialogOpen}
        onOpenChange={dialogs.setNewDialogOpen}
        tree={tree}
        initialParentID={dialogs.newDialogParent}
        onCreated={dialogs.refreshProjectData}
      />
      <ProjectSettingsDrawer
        projectID={dialogs.settingsProjectID}
        focus={dialogs.settingsFocus}
        onClose={() => {
          dialogs.setSettingsProjectID(0);
          dialogs.setSettingsFocus(undefined);
        }}
        onChanged={dialogs.refreshProjectData}
      />
      <DeleteProjectDialog
        target={dialogs.deleteTarget}
        onClose={() => dialogs.setDeleteTarget(null)}
        onDeleted={dialogs.refreshProjectData}
      />
      {importPrefill ? (
        <ImportSessionDialog
          open
          onOpenChange={(next) => {
            if (!next) setImportPrefill(null);
          }}
          ports={importPorts}
          prefill={importPrefill}
          onImported={(outcome) => {
            // 已经导过那一支同样跳过去 —— 用户要的是「看那条会话」，不是一句
            // 「早就导过了」。
            openSession(Number(outcome.sessionId));
            reloadSidebarSources();
          }}
        />
      ) : null}
      <ProjectMergeDialog
        source={mergeSource}
        candidates={
          mergeSource
            ? [...projectByID.values()]
                .filter((p) => p.id !== mergeSource.id)
                .sort((a, b) => a.name.localeCompare(b.name))
            : []
        }
        onClose={() => dialogs.setMergeSource(null)}
        onMerged={dialogs.refreshProjectData}
      />
    </>
  );
}

export { IndexDialogs };
export type { IndexDialogsProps };
