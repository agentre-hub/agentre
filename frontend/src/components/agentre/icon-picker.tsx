import * as React from "react";
import {
  ChevronDown,
  Image as ImageIcon,
  Pencil,
  Search,
  Type,
  Upload,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  getAgentInitials,
} from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import {
  hasIcon,
  iconForKey,
  iconMeta,
  iconsByCategory,
  searchIcons,
  type IconMeta,
} from "./icon-registry";
import { agentColorClassNames, type AgentColor } from "./types";

// ----------------------------------------------------------------------------
// IconPicker —— 纯图标选择（部门用，无清空）
// ----------------------------------------------------------------------------

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

type IconGridPanelProps = {
  value: string;
  onSelect: (key: string) => void;
  allowClear?: boolean;
  onClear?: () => void;
};

function IconGridPanel({
  value,
  onSelect,
  allowClear,
  onClear,
}: IconGridPanelProps) {
  const { t } = useTranslation();
  const [query, setQuery] = React.useState("");
  const trimmed = query.trim();
  const groups = React.useMemo(() => {
    if (trimmed) {
      const flat = searchIcons(trimmed);
      return [
        {
          category: "search",
          label: t("iconPicker.search.results"),
          items: flat,
        },
      ];
    }
    return iconsByCategory();
  }, [t, trimmed]);

  return (
    <div className="flex flex-col">
      <div className="border-b border-border px-3 py-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={t("iconPicker.search.aria")}
            placeholder={t("iconPicker.search.placeholder")}
            className="h-8 pl-7 text-xs"
          />
        </div>
      </div>
      <div className="max-h-[280px] overflow-y-auto px-2 py-2">
        {groups.map((g, i) => (
          <div
            key={g.category}
            className={cn("space-y-1.5", i > 0 && "mt-3")}
            data-icon-group={g.category}
          >
            {!trimmed && (
              <div className="px-1 font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                {g.label}
              </div>
            )}
            {g.items.length === 0 ? (
              <div className="px-1 py-2 text-2xs text-muted-foreground">
                {t("iconPicker.search.noMatches")}
              </div>
            ) : (
              <div className="grid grid-cols-6 gap-1.5">
                {g.items.map((m) => (
                  <IconCell
                    key={m.key}
                    meta={m}
                    active={value === m.key}
                    onSelect={() => onSelect(m.key)}
                  />
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
      {allowClear && (
        <div className="border-t border-border px-3 py-2">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-full justify-start gap-1.5 text-xs text-muted-foreground"
            onClick={() => onClear?.()}
          >
            <X className="size-3.5" aria-hidden="true" />
            {t("iconPicker.search.clearIcon")}
          </Button>
        </div>
      )}
    </div>
  );
}

function IconCell({
  meta,
  active,
  onSelect,
}: {
  meta: IconMeta;
  active: boolean;
  onSelect: () => void;
}) {
  const Icon = meta.icon;
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      aria-label={`${meta.label} (${meta.key})`}
      title={meta.label}
      onClick={onSelect}
      className={cn(
        "inline-flex aspect-square items-center justify-center rounded-md border text-foreground transition-colors",
        active
          ? "border-primary bg-primary-soft text-primary-text"
          : "border-border bg-card hover:bg-accent",
      )}
    >
      <Icon className="size-4" aria-hidden="true" />
    </button>
  );
}

// ----------------------------------------------------------------------------
// 内部：模式切换 chip
// ----------------------------------------------------------------------------

function ModeChip({
  active,
  disabled,
  icon: Icon,
  label,
  hint,
  onClick,
}: {
  active: boolean;
  disabled?: boolean;
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  label: string;
  hint: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex flex-1 flex-col items-start gap-0 rounded-md border px-2 py-1.5 text-left transition-colors",
        active
          ? "border-primary bg-primary-soft text-primary-text"
          : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground",
        disabled && "cursor-not-allowed opacity-50 hover:bg-transparent",
      )}
    >
      <span className="flex items-center gap-1 text-xs font-semibold">
        <Icon className="size-3.5" aria-hidden={true} />
        {label}
      </span>
      <span
        className={cn(
          "font-mono text-2xs",
          active ? "text-primary-text/70" : "text-muted-foreground",
        )}
      >
        {hint}
      </span>
    </button>
  );
}

function getModeIconForChip(iconKey: string) {
  if (hasIcon(iconKey)) {
    return iconForKey(iconKey);
  }
  return ImageIcon; // 占位（未设置时显示个图）
}

// ----------------------------------------------------------------------------
// 内部：图片模式面板
// ----------------------------------------------------------------------------

const ACCEPT = "image/png,image/jpeg,image/webp";
const MAX_BYTES = 2 * 1024 * 1024;

function ImageModePanel({
  name,
  avatarDataUrl,
  onUpload,
  onDelete,
  allowUpload,
}: {
  name: string;
  avatarDataUrl: string;
  onUpload: (file: File) => Promise<void>;
  onDelete: () => void;
  allowUpload: boolean;
}) {
  const { t } = useTranslation();
  if (!allowUpload) {
    return (
      <div className="px-3 py-4 text-xs text-muted-foreground">
        {t("iconPicker.avatar.uploadDisabled")}
      </div>
    );
  }

  return (
    <div className="space-y-3 px-3 py-3">
      <div className="flex items-center gap-3">
        <div className="inline-flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-border bg-muted">
          {avatarDataUrl ? (
            <img
              src={avatarDataUrl}
              alt={name}
              className="size-full object-cover"
              draggable={false}
            />
          ) : (
            <span className="font-mono text-2xs text-muted-foreground">
              {t("iconPicker.avatar.notUploaded")}
            </span>
          )}
        </div>
        <AgentAvatarUploadActions
          avatarDataUrl={avatarDataUrl}
          onUpload={onUpload}
          onDelete={onDelete}
          uploadLabel={
            avatarDataUrl
              ? t("iconPicker.avatar.replace")
              : t("iconPicker.avatar.upload")
          }
        />
      </div>
    </div>
  );
}

type AgentAvatarUploadActionsProps = {
  avatarDataUrl: string;
  onUpload: (file: File) => Promise<void> | void;
  onDelete?: () => Promise<void> | void;
  uploadLabel?: string;
  className?: string;
};

export function AgentAvatarUploadActions({
  avatarDataUrl,
  onUpload,
  onDelete,
  uploadLabel,
  className,
}: AgentAvatarUploadActionsProps) {
  const { t } = useTranslation();
  const fileInputRef = React.useRef<HTMLInputElement>(null);
  const [error, setError] = React.useState<string | null>(null);

  const handleSelect = async (file: File) => {
    setError(null);
    if (file.size > MAX_BYTES) {
      setError(t("iconPicker.avatar.errors.tooLarge"));
      return;
    }
    try {
      await onUpload(file);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : t("iconPicker.avatar.errors.uploadFailed"),
      );
    }
  };

  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1.5"
          onClick={() => fileInputRef.current?.click()}
        >
          <Upload className="size-3" />
          {uploadLabel ??
            (avatarDataUrl
              ? t("iconPicker.avatar.replace")
              : t("iconPicker.avatar.upload"))}
        </Button>
        {avatarDataUrl && onDelete && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 text-destructive"
            aria-label={t("iconPicker.avatar.deleteUpload")}
            onClick={() => void onDelete()}
          >
            <X className="size-3" />
            {t("common.delete")}
          </Button>
        )}
      </div>
      <p className="font-mono text-2xs text-muted-foreground">
        {t("iconPicker.avatar.uploadHint")}
      </p>
      {error && <p className="text-2xs text-destructive">{error}</p>}
      <input
        ref={fileInputRef}
        type="file"
        accept={ACCEPT}
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) void handleSelect(file);
        }}
      />
    </div>
  );
}

// ----------------------------------------------------------------------------
// 内部：字母模式面板（仅展示）
// ----------------------------------------------------------------------------

function LetterModePanel({ name, color }: { name: string; color: AgentColor }) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 px-3 py-3">
      <div
        aria-hidden="true"
        className={cn(
          "inline-flex size-16 shrink-0 items-center justify-center rounded-lg text-2xl font-semibold text-agent-foreground",
          agentColorClassNames[color],
        )}
      >
        {getAgentInitials(name)}
      </div>
      <div className="flex flex-col gap-0.5">
        <span className="text-sm font-semibold">
          {t("iconPicker.avatar.autoGenerated", {
            initials: getAgentInitials(name),
          })}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("iconPicker.avatar.letterHint")}
        </span>
      </div>
    </div>
  );
}
