// 项目筛选:一个 chip 打开的下拉,里面是可搜索的项目列表。

import * as React from "react";
import { Check, ChevronDown, Folder, FolderMinus } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";
import { useProjectList } from "@/hooks/use-project-list";
import { cn } from "@/lib/utils";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";

export type ProjectChipPickerProps = {
  projectContext: ReturnType<
    typeof useNewChatContextStore.getState
  >["projectContext"];
  projects: ReturnType<typeof useProjectList>["projects"];
  onPick: (
    picked: ReturnType<typeof useProjectList>["projects"][number] | null,
  ) => void;
};

export function ProjectChipPicker({
  projectContext,
  projects,
  onPick,
}: ProjectChipPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const label = projectContext
    ? projectContext.projectName ||
      t("commandPalette.project.fallbackName", {
        id: projectContext.projectID,
      })
    : t("commandPalette.project.none");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("commandPalette.project.switchAria")}
          title={t("commandPalette.project.switchTitle")}
          className={cn(
            "inline-flex h-[22px] items-center gap-1.5 rounded-sm border bg-card px-2 text-xs font-medium text-foreground outline-none transition-colors hover:bg-accent",
            projectContext
              ? "border-border"
              : "border-border/60 text-muted-foreground",
          )}
        >
          {projectContext ? (
            <Folder
              className="size-[12px] text-primary-text"
              aria-hidden="true"
            />
          ) : (
            <FolderMinus
              className="size-[12px] text-muted-foreground"
              aria-hidden="true"
            />
          )}
          {label}
          <ChevronDown
            className="size-[11px] text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" sideOffset={4} className="w-[260px] p-1">
        <ProjectPickerItem
          icon={
            <FolderMinus
              className="size-[12px] text-muted-foreground"
              aria-hidden="true"
            />
          }
          label={t("commandPalette.project.none")}
          subtitle={t("commandPalette.context.freeModeHint")}
          selected={!projectContext}
          onSelect={() => {
            onPick(null);
            setOpen(false);
          }}
        />
        {projects.length > 0 ? (
          <div className="my-1 h-px bg-border" aria-hidden="true" />
        ) : null}
        {projects.length === 0 ? (
          <div className="px-2 py-3 text-center text-2xs text-muted-foreground">
            {t("commandPalette.project.empty")}
          </div>
        ) : (
          projects.map((p) => (
            <ProjectPickerItem
              key={p.id}
              icon={
                <Folder
                  className="size-[12px] text-primary-text"
                  aria-hidden="true"
                />
              }
              label={p.name}
              selected={projectContext?.projectID === p.id}
              onSelect={() => {
                onPick(p);
                setOpen(false);
              }}
            />
          ))
        )}
      </PopoverContent>
    </Popover>
  );
}

export type ProjectPickerItemProps = {
  icon: React.ReactNode;
  label: string;
  subtitle?: string;
  selected: boolean;
  onSelect: () => void;
};

export function ProjectPickerItem({
  icon,
  label,
  subtitle,
  selected,
  onSelect,
}: ProjectPickerItemProps) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left transition-colors hover:bg-accent",
        selected && "bg-accent/60",
      )}
    >
      <span className="flex size-5 shrink-0 items-center justify-center">
        {icon}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-xs font-medium text-foreground">
          {label}
        </span>
        {subtitle ? (
          <span className="truncate text-2xs text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
      </span>
      {selected ? (
        <Check
          className="size-[12px] shrink-0 text-primary-text"
          aria-hidden="true"
        />
      ) : null}
    </button>
  );
}
