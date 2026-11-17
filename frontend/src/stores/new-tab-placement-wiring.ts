// 把「新对话靠拢同项目」接到标签页 store 上（spec 2026-11-17-group-new-tabs-by-project）。
//
// 每次新建时现读：设置开关、会话所属项目（session-meta-store）与项目树快照。
// 开关关闭时返回 tabs.length，与未装端口时完全一致。

import { getProjectTreeSnapshot } from "@/hooks/use-project-tree";
import {
  newTabInsertIndex,
  projectOfTab,
  rootProjectResolver,
} from "@/lib/new-tab-placement";

import { setNewTabPlacement } from "./chat-tabs-store";
import { useSessionMetaStore } from "./session-meta-store";
import { useTabSettingsStore } from "./tab-settings-store";

export function installNewTabPlacement(): void {
  setNewTabPlacement((tabs, projectId) => {
    if (!useTabSettingsStore.getState().settings.groupNewByProject) {
      return tabs.length;
    }
    const metas = useSessionMetaStore.getState().metas;
    return newTabInsertIndex(tabs, projectId, {
      projectOfTab: (tab) => projectOfTab(tab, (sid) => metas.get(sid)),
      rootOf: rootProjectResolver(getProjectTreeSnapshot()),
    });
  });
}
