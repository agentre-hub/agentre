// 面板底部的上下文条:当前作用域(项目/会话)+ 快捷键提示。
// cycleProjectShortcut 是它按 ⇧⇥ 轮换项目时用的键位串。

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
};

// Tab / Shift+Tab 直接操作 new-chat-context-store（焦点不动）。
// 复用 ContextBar 的写库 / localStorage 写入逻辑，保证两套入口（点击 vs 键盘）
// 状态变化一致。
export function cycleProjectShortcut(
  projects: ProjectFlat[],
  direction: 1 | -1,
): void {
  if (projects.length === 0) return; // 没有项目时 no-op
  const store = useNewChatContextStore.getState();
  const cur = store.projectContext;
  const curIdx =
    cur == null ? -1 : projects.findIndex((p) => p.id === cur.projectID);

  // 环形顺序：无项目 → 0 → 1 → ... → n-1 → 无项目 → 0 → ...
  // 反向顺序：无项目 → n-1 → ... → 0 → 无项目 → n-1 → ...
  // 当前 projectID 已不在列表里（项目被删）也走"从头开始"分支。
  let next: ProjectFlat | null;
  if (direction === 1) {
    if (cur == null) {
      next = projects[0];
    } else if (curIdx === -1) {
      next = projects[0];
    } else if (curIdx === projects.length - 1) {
      next = null;
    } else {
      next = projects[curIdx + 1];
    }
  } else {
    if (cur == null) {
      next = projects[projects.length - 1];
    } else if (curIdx === -1) {
      next = projects[projects.length - 1];
    } else if (curIdx === 0) {
      next = null;
    } else {
      next = projects[curIdx - 1];
    }
  }

  if (next == null) {
    store.setContext(null);
    clearLastContext();
    return;
  }
  const ctx = {
    projectID: next.id,
    projectName: next.name,
  };
  store.setContext(ctx);
  writeLastContext(ctx);
}

export function ContextBar({ projects }: ContextBarProps) {
  const { t } = useTranslation();
  const projectContext = useNewChatContextStore((s) => s.projectContext);
  const setContext = useNewChatContextStore((s) => s.setContext);

  return (
    <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-muted/40 px-5 text-2xs">
      <span className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("commandPalette.context.title")}
      </span>

      <ProjectChipPicker
        projectContext={projectContext}
        projects={projects}
        onPick={(picked) => {
          if (picked === null) {
            setContext(null);
            clearLastContext();
            return;
          }
          const next = {
            projectID: picked.id,
            projectName: picked.name,
          };
          setContext(next);
          writeLastContext(next);
        }}
      />

      <div className="ml-auto flex items-center gap-3">
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
