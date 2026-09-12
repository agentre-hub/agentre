// 头像的「上传 / 移除」动作区(含上传中的禁用态)。

import * as React from "react";
import { Upload, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import { ACCEPT, MAX_BYTES } from "./image-mode-panel";

export type AgentAvatarUploadActionsProps = {
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
