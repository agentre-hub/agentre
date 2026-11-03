import * as React from "react";
import { Pencil } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { DepartmentMark } from "../org/department-mark";
import { hasIcon, iconMeta } from "../org/icon-registry";
import {
  presetForKey,
  presetsByGroup,
  type PresetGroup,
} from "../presets/preset-registry";
import { initialOf } from "../session-index/glyph";
import { ProjectGlyph } from "../session-index/project-glyph";
import { AgentAvatar, getAgentInitials } from "../ui/agent-avatar";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import {
  SegmentedControl,
  SegmentedControlItem,
} from "../ui/segmented-control";
import { uploadedAvatarSrc } from "./avatar-image";
import { ColorPalette } from "./color-palette";
import { IconGrid } from "./icon-grid";
import {
  ImagePanel,
  useIdentityImageUpload,
  type IdentityImageUpload,
} from "./image-panel";
import { PresetGrid } from "./preset-grid";

export interface IdentityMarkPickerProps {
  kind: "agent" | "department" | "project";
  name: string;
  iconKey: string;
  color: string;
  avatarDataUrl?: string;
  upload?: IdentityImageUpload | "after-create";
  onPickIcon(key: string): void;
  onPickColor(color: string): void;
}

type Mode = "preset" | "icon" | "letter" | "image";
const GROUPS: Record<IdentityMarkPickerProps["kind"], readonly PresetGroup[]> =
  {
    agent: ["agent", "brand"],
    department: ["team"],
    project: ["project"],
  };

export function IdentityMarkPicker({
  kind,
  name,
  iconKey,
  color,
  avatarDataUrl: storedAvatar,
  upload,
  onPickIcon,
  onPickColor,
}: IdentityMarkPickerProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const imageOperation = useIdentityImageUpload(upload);
  const avatarDataUrl = uploadedAvatarSrc(storedAvatar);
  const preset = presetForKey(iconKey);
  const icon = iconMeta(iconKey, t);
  const effective: Mode =
    avatarDataUrl && upload
      ? "image"
      : preset
        ? "preset"
        : hasIcon(iconKey)
          ? "icon"
          : "letter";
  const [selectedMode, setMode] = React.useState<Mode>(effective);
  const mode = selectedMode === "image" && !upload ? effective : selectedMode;
  const id = React.useId();
  const initials =
    kind === "agent" ? getAgentInitials(name) : (initialOf(name) ?? "?");
  const labels: Record<Mode, string> = {
    preset: t("identityMark.preset"),
    icon: t("identityMark.icon"),
    letter: t("identityMark.letter"),
    image: t("identityMark.image"),
  };
  const hints: Record<Mode, string> = {
    preset: preset ? t(preset.labelKey) : t("identityMark.notSelected"),
    icon: icon?.label ?? t("identityMark.notSet"),
    letter: initials,
    image: avatarDataUrl
      ? t("identityMark.uploaded")
      : t("identityMark.notUploaded"),
  };
  const groupLabels: Record<PresetGroup, string> = {
    agent: t("identityMark.professions"),
    brand: t("identityMark.brands"),
    team: t("identityMark.departments"),
    project: t("identityMark.projects"),
  };
  const modes: Mode[] = upload
    ? ["preset", "icon", "letter", "image"]
    : ["preset", "icon", "letter"];
  function pick(key: string) {
    onPickIcon(key);
    setOpen(false);
  }

  // 触发器和字母预览复用真正的身份记号，不在选择器内另写一份优先级。
  function mark(letterOnly = false) {
    const key = letterOnly ? "" : iconKey;
    const className = "size-13 rounded-xl text-base";
    if (kind === "department")
      return (
        <DepartmentMark
          name={name}
          icon={key}
          accentColor={color}
          className={className}
        />
      );
    if (kind === "project")
      return (
        <ProjectGlyph
          project={{ name, icon: key, color }}
          className={className}
        />
      );
    return (
      <AgentAvatar
        name={name}
        iconKey={key}
        color={color}
        avatarDataUrl={letterOnly ? undefined : avatarDataUrl}
        className={className}
      />
    );
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next) setMode(effective);
        setOpen(next);
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={
            kind === "agent"
              ? t("identityMark.changeAvatar")
              : t("identityMark.changeIcon")
          }
          className="group/mark relative shrink-0 rounded-xl outline-none focus-visible:focus-outline"
        >
          {mark()}
          <span
            aria-hidden="true"
            className="absolute -bottom-1 -right-1 inline-flex size-5 items-center justify-center rounded-full bg-field-fill text-muted-foreground shadow-sm transition-colors group-hover/mark:text-foreground"
          >
            <Pencil className="size-2.5" />
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        side="bottom"
        sideOffset={8}
        className="w-[392px] max-w-[calc(100vw-24px)] p-0"
      >
        <SegmentedControl
          value={mode}
          onValueChange={(value) => setMode(value as Mode)}
          className="flex gap-1 p-2"
        >
          {modes.map((value) => (
            <SegmentedControlItem
              key={value}
              value={value}
              id={`${id}-${value}`}
              aria-controls={`${id}-panel`}
              aria-label={labels[value]}
              className="h-auto min-w-0 flex-1 flex-col items-start gap-0.5 px-2 py-1.5"
            >
              <span className="text-xs font-semibold">{labels[value]}</span>
              <span className="max-w-full truncate font-mono text-2xs font-normal text-muted-foreground">
                {hints[value]}
              </span>
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
        {(kind !== "department" || mode === "icon" || mode === "letter") && (
          <div
            role="group"
            aria-label={t("identityMark.color")}
            className="px-3 pb-3"
          >
            <p className="text-2xs text-muted-foreground">
              {t("identityMark.color")}
              {kind !== "department" && (
                <span className="ml-2">
                  {kind === "agent"
                    ? t("identityMark.agentColorNote")
                    : t("identityMark.projectColorNote")}
                </span>
              )}
            </p>
            <ColorPalette color={color} onPickColor={onPickColor} />
          </div>
        )}
        <div
          role="tabpanel"
          id={`${id}-panel`}
          aria-labelledby={`${id}-${mode}`}
        >
          {mode === "preset" && (
            <>
              <div className="max-h-[270px] space-y-3 overflow-y-auto p-3">
                {GROUPS[kind].map((group) => (
                  <div key={group}>
                    <p className="mb-2 text-2xs font-semibold text-muted-foreground">
                      {groupLabels[group]}
                    </p>
                    <PresetGrid
                      items={presetsByGroup(group)}
                      value={iconKey}
                      onPick={pick}
                      label={groupLabels[group]}
                    />
                  </div>
                ))}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="m-2"
                onClick={() => pick("")}
              >
                {t("identityMark.clearPreset")}
              </Button>
            </>
          )}
          {mode === "icon" && (
            <>
              <IconGrid
                value={iconKey}
                onPick={pick}
                presetGroups={GROUPS[kind]}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="m-2"
                onClick={() => pick("")}
              >
                {t("identityMark.clearIcon")}
              </Button>
            </>
          )}
          {mode === "letter" && (
            <div className="flex items-center gap-3 p-3">
              {mark(true)}
              <div>
                <p className="text-sm font-semibold">
                  {t("identityMark.autoGenerated", { initials })}
                </p>
                <p className="font-mono text-2xs text-muted-foreground">
                  {t("identityMark.letterHint")}
                </p>
              </div>
            </div>
          )}
          {mode === "image" && upload && (
            <ImagePanel
              name={name}
              avatarDataUrl={avatarDataUrl}
              upload={upload}
              operation={imageOperation}
            />
          )}
        </div>
        {avatarDataUrl && mode !== "image" && (
          <p className="rounded-b-lg bg-secondary/40 px-3 py-2 font-mono text-2xs text-muted-foreground">
            {t("identityMark.activeUpload")}
            {t("identityMark.uploadFallback")}
          </p>
        )}
      </PopoverContent>
    </Popover>
  );
}
