// 搜索行下面的上下文条:新会话建在哪个项目(chip 选择器)+ 右侧的提示。

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
  // 选择器的开合由面板持有:点 chip 与在输入框按 Tab 是同一个入口。
  pickerOpen: boolean;
  onPickerOpenChange: (open: boolean) => void;
  // 选择器收起后焦点交还的命令输入框。
  returnFocusRef: React.RefObject<HTMLInputElement | null>;
};

export function ContextBar({
  projects,
  pickerOpen,
  onPickerOpenChange,
  returnFocusRef,
}: ContextBarProps) {
  const { t } = useTranslation();
  const projectContext = useNewChatContextStore((s) => s.projectContext);
  const setContext = useNewChatContextStore((s) => s.setContext);

  return (
    <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-muted/40 px-5 text-2xs">
      <span className="shrink-0 font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
        {t("commandPalette.context.title")}
      </span>

      <ProjectChipPicker
        projectContext={projectContext}
        projects={projects}
        open={pickerOpen}
        onOpenChange={onPickerOpenChange}
        returnFocusRef={returnFocusRef}
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
