// 搜索行下面的上下文条:新会话建在哪个项目(chip 选择器)+ 右侧的提示。
// cycleProjectShortcut 是输入框里按 Tab / Shift+Tab 原地轮换项目用的。

import * as React from "react";
import { useTranslation } from "react-i18next";

import { type ProjectFlat } from "@/hooks/use-project-list";
import {
  clearLastContext,
  writeLastContext,
} from "@/stores/new-chat-context-persistence";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";

import { KbdHint } from "./footer";
import { ProjectChipPicker } from "./project-chip-picker";

export type ContextBarProps = {
  projects: ProjectFlat[];
  // 选择器的开合由面板持有:面板关掉时一并收起。
  pickerOpen: boolean;
  onPickerOpenChange: (open: boolean) => void;
  // 选择器收起后焦点交还的命令输入框。
  returnFocusRef: React.RefObject<HTMLInputElement | null>;
};

// 点选(选择器)与键盘(Tab 轮换)两个入口共用这一处写入:store + localStorage
// 一起动,两条路径留下的状态不会分叉。
function applyProjectPick(picked: ProjectFlat | null): void {
  const store = useNewChatContextStore.getState();
  if (picked === null) {
    store.setContext(null);
    clearLastContext();
    return;
  }
  const next = { projectID: picked.id, projectName: picked.name };
  store.setContext(next);
  writeLastContext(next);
}

// Tab / Shift+Tab 直接操作 new-chat-context-store（焦点不动）。
// projects 是 use-project-list 的先序平铺,与选择器里树的自上而下顺序一致。
export function cycleProjectShortcut(
  projects: ProjectFlat[],
  direction: 1 | -1,
): void {
  if (projects.length === 0) return; // 没有项目时 no-op
  const cur = useNewChatContextStore.getState().projectContext;
  const curIdx =
    cur == null ? -1 : projects.findIndex((p) => p.id === cur.projectID);

  // 环形顺序：无项目 → 0 → 1 → ... → n-1 → 无项目 → 0 → ...
  // 反向顺序：无项目 → n-1 → ... → 0 → 无项目 → n-1 → ...
  // 当前 projectID 已不在列表里（项目被删）也从头（正向）/ 从尾（反向）开始。
  let next: ProjectFlat | null;
  if (direction === 1) {
    if (curIdx === -1) next = projects[0];
    else if (curIdx === projects.length - 1) next = null;
    else next = projects[curIdx + 1];
  } else {
    if (curIdx === -1) next = projects[projects.length - 1];
    else if (curIdx === 0) next = null;
    else next = projects[curIdx - 1];
  }
  applyProjectPick(next);
}

export function ContextBar({
  projects,
  pickerOpen,
  onPickerOpenChange,
  returnFocusRef,
}: ContextBarProps) {
  const { t } = useTranslation();
  const projectContext = useNewChatContextStore((s) => s.projectContext);

  return (
    <div className="flex h-9 shrink-0 items-center gap-2 bg-muted/40 px-5 text-2xs">
      <span className="shrink-0 font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("commandPalette.context.title")}
      </span>

      <ProjectChipPicker
        projectContext={projectContext}
        projects={projects}
        open={pickerOpen}
        onOpenChange={onPickerOpenChange}
        returnFocusRef={returnFocusRef}
        onPick={applyProjectPick}
      />

      <div className="ml-auto flex shrink-0 items-center gap-3">
        <span className="text-2xs text-muted-foreground">
          {projectContext
            ? t("commandPalette.context.projectModeHint")
            : t("commandPalette.context.freeModeHint")}
        </span>
        <KbdHint kbd="Tab" label={t("commandPalette.context.switchProject")} />
      </div>
    </div>
  );
}
