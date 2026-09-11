// 图标 / 头像选择器:三个对外组件(IconPicker / AgentAvatarPicker /
// AgentAvatarUploadActions)与它们共用的模式切换。
//
// 那些零件在 icon-picker/ 下:图标网格、上传图片档、首字母档、模式芯片、上传动作区。

import * as React from "react";
import { ChevronDown, Image as ImageIcon, Pencil, Type } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
  getAgentInitials,
} from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import { iconForKey, iconMeta } from "./icon-registry";
import { agentColorClassNames, type AgentColor } from "./types";

import { IconGridPanel } from "./icon-picker/icon-grid-panel";
import { ImageModePanel } from "./icon-picker/image-mode-panel";
import { LetterModePanel } from "./icon-picker/letter-mode-panel";
import { ModeChip, getModeIconForChip } from "./icon-picker/mode-chip";

// AgentAvatarUploadActions 的实现搬到了 icon-picker/ 下，但它仍是本模块的对外入口之一，
// 消费方继续从 "./icon-picker" 取它。
export { AgentAvatarUploadActions } from "./icon-picker/avatar-upload-actions";

type IconPickerProps = {
  value: string;
  onChange: (key: string) => void;
  accentColor: AgentColor;
  ariaLabel?: string;
  className?: string;
};

export function IconPicker({
  value,
  onChange,
  accentColor,
  ariaLabel,
  className,
}: IconPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const Icon = iconForKey(value);
  const meta = iconMeta(value);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={ariaLabel ?? t("org.department.icon")}
          className={cn(
            "flex w-full items-center gap-2.5 rounded-md border border-border bg-card px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent",
            className,
          )}
        >
          <span
            className={cn(
              "inline-flex size-6 shrink-0 items-center justify-center rounded text-agent-foreground",
              agentColorClassNames[accentColor],
            )}
            aria-hidden="true"
          >
            {React.createElement(Icon, {
              className: "size-3.5",
              "aria-hidden": true,
            })}
          </span>
          <span className="flex-1 truncate font-mono text-2xs">
            {meta?.label ?? value ?? t("iconPicker.selectIcon")}
          </span>
          <ChevronDown
            className="size-3 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[360px] p-0" align="start">
        <IconGridPanel
          value={value}
          onSelect={(key) => {
            onChange(key);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

// ----------------------------------------------------------------------------
// AgentAvatarPicker —— Agent 用，三态切换（图片 / 图标 / 字母）
// ----------------------------------------------------------------------------

type AvatarMode = "image" | "icon" | "letter";

type AgentAvatarPickerProps = {
  name: string;
  avatarColor: AgentColor;
  avatarIcon: string;
  avatarDataUrl: string;
  onChangeIcon: (key: string) => void;
  onUploadFile?: (file: File) => Promise<void> | void;
  onDeleteUpload?: () => Promise<void> | void;
  allowUpload?: boolean;
  showImageMode?: boolean;
  triggerClassName?: string;
  triggerSize?: "sm" | "md" | "lg";
  triggerAriaLabel?: string;
  children?: React.ReactNode; // 自定义触发器（默认渲染头像）
};

const triggerSizeClassNames: Record<
  NonNullable<AgentAvatarPickerProps["triggerSize"]>,
  string
> = {
  sm: "size-6 rounded-md text-2xs",
  md: "size-8 rounded-lg text-sm",
  lg: "size-10 rounded-lg text-sm",
};

export function AgentAvatarPicker({
  name,
  avatarColor,
  avatarIcon,
  avatarDataUrl,
  onChangeIcon,
  onUploadFile,
  onDeleteUpload,
  allowUpload = true,
  showImageMode = true,
  triggerClassName,
  triggerSize = "md",
  triggerAriaLabel,
  children,
}: AgentAvatarPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const effectiveMode: AvatarMode =
    showImageMode && avatarDataUrl ? "image" : avatarIcon ? "icon" : "letter";
  const [mode, setMode] = React.useState<AvatarMode>(effectiveMode);
  const handleOpenChange = React.useCallback(
    (nextOpen: boolean) => {
      if (nextOpen) setMode(effectiveMode);
      setOpen(nextOpen);
    },
    [effectiveMode],
  );

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        {children ?? (
          <button
            type="button"
            aria-label={triggerAriaLabel ?? t("iconPicker.avatar.change")}
            className={cn(
              "group relative inline-flex shrink-0 items-center justify-center overflow-hidden font-semibold text-agent-foreground outline-offset-2 focus-visible:outline-2 focus-visible:outline-primary",
              triggerSizeClassNames[triggerSize],
              !avatarDataUrl && agentColorClassNames[avatarColor],
              !avatarDataUrl && avatarIcon && "rounded-lg",
              avatarDataUrl && "bg-muted",
              triggerClassName,
            )}
          >
            {avatarDataUrl ? (
              <img
                src={avatarDataUrl}
                alt={name}
                className="size-full object-cover"
                draggable={false}
              />
            ) : avatarIcon ? (
              React.createElement(iconForKey(avatarIcon), {
                className: "size-[60%]",
                "aria-hidden": true,
              })
            ) : (
              <span aria-hidden="true">{getAgentInitials(name)}</span>
            )}
            <span
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 hidden items-center justify-center bg-scrim text-agent-foreground group-hover:flex"
            >
              <Pencil className="size-3.5" />
            </span>
          </button>
        )}
      </PopoverTrigger>
      <PopoverContent className="w-[380px] p-0" align="start" sideOffset={8}>
        <div className="flex flex-col">
          <div className="flex items-center gap-1 border-b border-border px-3 py-2">
            {showImageMode && (
              <ModeChip
                active={mode === "image"}
                disabled={!allowUpload}
                icon={ImageIcon}
                label={t("iconPicker.avatar.image")}
                hint={
                  avatarDataUrl
                    ? t("iconPicker.avatar.uploaded")
                    : t("iconPicker.avatar.notUploaded")
                }
                onClick={() => allowUpload && setMode("image")}
              />
            )}
            <ModeChip
              active={mode === "icon"}
              icon={getModeIconForChip(avatarIcon)}
              label={t("org.department.icon")}
              hint={
                avatarIcon
                  ? (iconMeta(avatarIcon)?.label ??
                    t("iconPicker.avatar.selected"))
                  : t("iconPicker.avatar.notSet")
              }
              onClick={() => setMode("icon")}
            />
            <ModeChip
              active={mode === "letter"}
              icon={Type}
              label={t("iconPicker.avatar.letter")}
              hint={getAgentInitials(name)}
              onClick={() => setMode("letter")}
            />
          </div>

          {showImageMode && mode === "image" && (
            <ImageModePanel
              name={name}
              avatarDataUrl={avatarDataUrl}
              onUpload={async (file) => {
                if (onUploadFile) await onUploadFile(file);
              }}
              onDelete={() => {
                if (onDeleteUpload) void onDeleteUpload();
              }}
              allowUpload={allowUpload}
            />
          )}

          {mode === "icon" && (
            <IconGridPanel
              value={avatarIcon}
              allowClear
              onSelect={(key) => {
                onChangeIcon(key);
                setOpen(false);
              }}
              onClear={() => {
                onChangeIcon("");
                setOpen(false);
              }}
            />
          )}

          {mode === "letter" && (
            <LetterModePanel name={name} color={avatarColor} />
          )}

          {avatarDataUrl && mode !== "image" && (
            <div className="border-t border-border bg-secondary/40 px-3 py-2 font-mono text-2xs text-muted-foreground">
              {t("iconPicker.avatar.activeUpload")}
              {mode === "icon" ? t("iconPicker.avatar.iconFallback") : ""}
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

// ----------------------------------------------------------------------------
// 内部：图标网格面板（搜索 + 分类）—— IconPicker / AgentAvatarPicker 共用
// ----------------------------------------------------------------------------
