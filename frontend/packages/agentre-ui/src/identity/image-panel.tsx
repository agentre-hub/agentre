import * as React from "react";
import { Upload, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

export interface IdentityImageUpload {
  onUpload(file: File): Promise<void>;
  onDelete(): Promise<void>;
}

const ACCEPT = "image/png,image/jpeg,image/webp";
const MAX_BYTES = 2 * 1024 * 1024;

// 文件约束在共享入口校验，宿主端口只负责上传与删除。
export function ImagePanel({
  name,
  avatarDataUrl,
  upload,
}: {
  name: string;
  avatarDataUrl?: string;
  upload: IdentityImageUpload | "after-create";
}) {
  const { t } = useUiTranslation();
  const input = React.useRef<HTMLInputElement>(null);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState("");

  async function run(file?: File) {
    if (upload === "after-create" || pending) return;
    setError("");
    if (file && file.size > MAX_BYTES) {
      setError(t("identityMark.tooLarge"));
      return;
    }
    if (file && !ACCEPT.split(",").includes(file.type)) {
      setError(t("identityMark.invalidType"));
      return;
    }
    setPending(true);
    try {
      if (file) await upload.onUpload(file);
      else await upload.onDelete();
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : file
            ? t("identityMark.uploadFailed")
            : t("identityMark.deleteFailed"),
      );
    } finally {
      setPending(false);
    }
  }

  if (upload === "after-create")
    return (
      <p className="px-3 py-4 text-xs text-muted-foreground">
        {t("identityMark.uploadDisabled")}
      </p>
    );

  return (
    <div className="space-y-3 p-3" aria-busy={pending}>
      <div className="flex items-center gap-3">
        <div className="inline-flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-field-fill">
          {avatarDataUrl ? (
            <img
              src={avatarDataUrl}
              alt={name}
              className="size-full object-cover"
              draggable={false}
            />
          ) : (
            <span className="font-mono text-2xs text-muted-foreground">
              {t("identityMark.notUploaded")}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={pending}
            onClick={() => input.current?.click()}
          >
            <Upload className="size-3" aria-hidden="true" />
            {avatarDataUrl
              ? t("identityMark.replace")
              : t("identityMark.upload")}
          </Button>
          {avatarDataUrl && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={pending}
              aria-label={t("identityMark.deleteUpload")}
              onClick={() => void run()}
            >
              <X className="size-3" aria-hidden="true" />
              {t("common.delete")}
            </Button>
          )}
        </div>
      </div>
      <p className="font-mono text-2xs text-muted-foreground">
        {t("identityMark.uploadHint")}
      </p>
      {error && (
        <p role="alert" className="text-2xs text-destructive-text">
          {error}
        </p>
      )}
      <Input
        ref={input}
        type="file"
        accept={ACCEPT}
        aria-label={t("identityMark.uploadImage")}
        className="hidden"
        disabled={pending}
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.target.value = "";
          if (file) void run(file);
        }}
      />
    </div>
  );
}
