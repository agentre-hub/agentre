import { ImagePlus, SendHorizontal, X } from "lucide-react";
import * as React from "react";

import { AIChatInput } from "../chat-input";
import type { AIChatInputProps } from "../chat-input";
import type { AIChatInputHandle } from "../chat-input/types";
import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

export type ChatImageAttachment = {
  dataUrl: string;
  mediaType: string;
  name: string;
};

export type ChatComposerSubmit = {
  images?: ChatImageAttachment[];
  text: string;
};

export type ChatComposerProps = Omit<
  AIChatInputProps,
  "onSubmit" | "onEmptyChange"
> & {
  onSubmit: (message: ChatComposerSubmit) => void;
  supportsImageInput?: boolean;
  inputHandleRef?: React.RefObject<AIChatInputHandle | null>;
  sendButtonTestId?: string;
  leadingControls?: React.ReactNode;
  trailingControls?: React.ReactNode;
};

const IMAGE_ACCEPT = "image/png,image/jpeg,image/webp";
const MAX_IMAGE_COUNT = 4;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;

function readImage(file: File): Promise<ChatImageAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("read failed"));
        return;
      }
      resolve({
        dataUrl: reader.result,
        mediaType: file.type,
        name: file.name,
      });
    };
    reader.readAsDataURL(file);
  });
}

export function ChatComposer({
  className,
  disabled = false,
  leadingControls,
  onSubmit,
  supportsImageInput = true,
  sendButtonTestId,
  trailingControls,
  inputHandleRef,
  ...inputProps
}: ChatComposerProps) {
  const { t } = useUiTranslation();
  const ownInputRef = React.useRef<AIChatInputHandle>(null);
  const inputRef = inputHandleRef ?? ownInputRef;
  const fileRef = React.useRef<HTMLInputElement>(null);
  const [empty, setEmpty] = React.useState(true);
  const [images, setImages] = React.useState<ChatImageAttachment[]>([]);
  const [imageError, setImageError] = React.useState("");

  async function addFiles(files: FileList | null) {
    if (!files?.length) return;
    const next = Array.from(files);
    if (images.length + next.length > MAX_IMAGE_COUNT) {
      setImageError(
        t("chatComposer.images.tooMany", { count: MAX_IMAGE_COUNT }),
      );
      return;
    }
    if (
      next.some(
        (file) =>
          !IMAGE_ACCEPT.split(",").includes(file.type) ||
          file.size > MAX_IMAGE_BYTES,
      )
    ) {
      setImageError(t("chatComposer.images.unsupported"));
      return;
    }
    try {
      const attachments = await Promise.all(next.map(readImage));
      setImages((current) => [...current, ...attachments]);
      setImageError("");
    } catch {
      setImageError(t("chatComposer.images.readFailed"));
    } finally {
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  function submit(text: string) {
    const body = text.trim();
    if (!body && images.length === 0) return;
    onSubmit(images.length ? { text: body, images } : { text: body });
    setImages([]);
    setImageError("");
  }

  return (
    <form
      className={cn(
        "flex w-full flex-col overflow-hidden rounded-md border border-border bg-card shadow-xs transition-colors focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50",
        className,
      )}
      onSubmit={(event) => {
        event.preventDefault();
        if (empty && images.length) submit("");
        else inputRef.current?.submit();
      }}
    >
      <div className="flex flex-col gap-1 px-3.5 pt-2.5 pb-1">
        {images.length ? (
          <div className="flex flex-wrap gap-2 pb-1">
            {images.map((image, index) => (
              <div
                key={`${image.name}-${index}`}
                className="group relative h-16 w-20 overflow-hidden rounded-md border border-border bg-muted"
              >
                <img
                  src={image.dataUrl}
                  alt={image.name || t("chatComposer.images.attachmentAlt")}
                  className="h-full w-full object-cover"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="absolute top-1 right-1 size-5 bg-background/90 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                  aria-label={t("chatComposer.images.remove", {
                    name: image.name || index + 1,
                  })}
                  onClick={() =>
                    setImages((current) =>
                      current.filter((_, i) => i !== index),
                    )
                  }
                >
                  <X aria-hidden="true" className="size-3" />
                </Button>
              </div>
            ))}
          </div>
        ) : null}
        <AIChatInput
          {...inputProps}
          ref={inputRef}
          disabled={disabled}
          onEmptyChange={setEmpty}
          onSubmit={submit}
        />
        <div className="flex items-center gap-2 py-1">
          {supportsImageInput ? (
            <>
              <input
                ref={fileRef}
                type="file"
                accept={IMAGE_ACCEPT}
                multiple
                className="hidden"
                aria-label={t("chatComposer.images.add")}
                onChange={(event) => void addFiles(event.target.files)}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t("chatComposer.images.add")}
                disabled={disabled || images.length >= MAX_IMAGE_COUNT}
                onClick={() => fileRef.current?.click()}
              >
                <ImagePlus aria-hidden="true" className="size-4" />
              </Button>
            </>
          ) : null}
          {leadingControls}
          <div className="min-w-0 flex-1" />
          {trailingControls}
          <Button
            type="submit"
            data-testid={sendButtonTestId}
            size="icon-sm"
            aria-label={t("chatComposer.send")}
            disabled={disabled || (empty && images.length === 0)}
          >
            <SendHorizontal aria-hidden="true" className="size-4" />
          </Button>
        </div>
        {imageError ? (
          <p role="alert" className="text-xs text-destructive">
            {imageError}
          </p>
        ) : null}
      </div>
    </form>
  );
}
