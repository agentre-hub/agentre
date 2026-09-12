import * as React from "react";
import { useSearchParams } from "react-router-dom";

import type { AgentSlim } from "@/hooks/use-chat-agents";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import type { DeleteProjectTarget } from "../delete-project-dialog";
import type { ProjectMergeSource } from "../project-merge-dialog";

import type { app } from "../../../../wailsjs/go/models";

type SettingsFocus = "members" | "paths" | undefined;

type UseIndexDialogsOptions = {
  /** useProjectTree().invalidate —— 任何项目写操作之后重新拉树。*/
  invalidate: () => void;
  projectByID: Map<number, app.ProjectItem>;
  /** useProjectTree().loaded —— 树没到位就消费 `?focus=` 会把深链吞掉。*/
  loaded: boolean;
};

type IndexDialogsState = {
  newDialogOpen: boolean;
  setNewDialogOpen: React.Dispatch<React.SetStateAction<boolean>>;
  newDialogParent: number;
  openCreateDialog: (parentID: number) => void;
  settingsProjectID: number;
  setSettingsProjectID: React.Dispatch<React.SetStateAction<number>>;
  settingsFocus: SettingsFocus;
  setSettingsFocus: React.Dispatch<React.SetStateAction<SettingsFocus>>;
  deleteTarget: DeleteProjectTarget | null;
  setDeleteTarget: React.Dispatch<
    React.SetStateAction<DeleteProjectTarget | null>
  >;
  mergeSource: ProjectMergeSource | null;
  setMergeSource: React.Dispatch<
    React.SetStateAction<ProjectMergeSource | null>
  >;
  notChattableAgent: AgentSlim | null;
  setNotChattableAgent: React.Dispatch<React.SetStateAction<AgentSlim | null>>;
  refreshProjectData: () => void;
};

// useIndexDialogs 持有左栏那一排对话框的开关态(新建项目 / 项目设置 / 删除 / 合并 /
// 不可对话引导),外加它们共用的「改完刷新」与 `?focus=` 深链。
function useIndexDialogs({
  invalidate,
  projectByID,
  loaded,
}: UseIndexDialogsOptions): IndexDialogsState {
  const [newDialogOpen, setNewDialogOpen] = React.useState(false);
  const [newDialogParent, setNewDialogParent] = React.useState(0);
  const [settingsProjectID, setSettingsProjectID] = React.useState(0);
  /** 设置弹窗打开时直落哪一节。组头的「成员…」「机器与路径…」与「未配置」角标都
      落在折叠线以下，打开在顶部等于没有入口。 */
  const [settingsFocus, setSettingsFocus] =
    React.useState<SettingsFocus>(undefined);
  const [deleteTarget, setDeleteTarget] =
    React.useState<DeleteProjectTarget | null>(null);
  const [mergeSource, setMergeSource] =
    React.useState<ProjectMergeSource | null>(null);
  const [notChattableAgent, setNotChattableAgent] =
    React.useState<AgentSlim | null>(null);

  const refreshProjectData = React.useCallback(() => {
    invalidate();
    reloadSidebarSources();
  }, [invalidate]);

  const openCreateDialog = React.useCallback((parentID: number) => {
    setNewDialogParent(parentID);
    setNewDialogOpen(true);
  }, []);

  // `/chat?focus=<id>`：会话设置页点「项目」进来，打开该项目的设置抽屉，然后清掉
  // query 防重复。命中失败静默丢弃 —— 项目可能已经被删了。
  const [searchParams, setSearchParams] = useSearchParams();
  React.useEffect(() => {
    const raw = searchParams.get("focus");
    // 树还没到位就消费这条 query = 把深链吞掉：清 query 是无条件的，而项目要等
    // ProjectListTree 回来才认得出，冷缓存下抽屉就永远不开了。
    if (!raw || !loaded) return;
    const id = Number(raw);
    if (id > 0 && projectByID.has(id)) setSettingsProjectID(id);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("focus");
        return next;
      },
      { replace: true },
    );
  }, [searchParams, setSearchParams, projectByID, loaded]);

  return {
    newDialogOpen,
    setNewDialogOpen,
    newDialogParent,
    openCreateDialog,
    settingsProjectID,
    setSettingsProjectID,
    settingsFocus,
    setSettingsFocus,
    deleteTarget,
    setDeleteTarget,
    mergeSource,
    setMergeSource,
    notChattableAgent,
    setNotChattableAgent,
    refreshProjectData,
  };
}

export { useIndexDialogs };
export type { IndexDialogsState, SettingsFocus };
