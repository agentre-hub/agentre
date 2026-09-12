// 「上传图片」那一档:选文件、校验大小与类型,再交给上传动作。
// ACCEPT / MAX_BYTES 是这里唯一的输入约束,跟着它走。

import { useTranslation } from "react-i18next";

import { AgentAvatarUploadActions } from "./avatar-upload-actions";

export const ACCEPT = "image/png,image/jpeg,image/webp";

export const MAX_BYTES = 2 * 1024 * 1024;

export function ImageModePanel({
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
