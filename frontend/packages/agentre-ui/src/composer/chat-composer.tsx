import {
  Check,
  EyeOff,
  ImagePlus,
  LoaderCircle,
  Pencil,
  SendHorizontal,
  SquareTerminal,
  X,
} from "lucide-react";
import * as React from "react";

import { AIChatInput } from "../chat-input";
import type { AIChatInputProps } from "../chat-input";
import { resolveDroppedPaths, type DroppedImageItem } from "../chat-input/drop";
import { LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR } from "../chat-input/local-command-history/history-popover";
import type { AIChatInputHandle } from "../chat-input/types";
import {
  useFileDropZone,
  type DropZoneRegistrar,
} from "../chat-input/use-file-drop";
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

/**
 * 命令式句柄。发送失败时宿主用 `restoreDraft` 把刚提交的正文与图片原样放回；
 * `clearDraft` 丢弃草稿。富文本内容住在编辑器里而不是 React state，宿主在外面
 * 拼字符串是够不着的。
 */
export type ChatComposerHandle = {
  restoreDraft: (text: string, images: ChatImageAttachment[]) => void;
  clearDraft: () => void;
};

/** 宿主注入的原生拖入通道。浏览器里拿不到落盘绝对路径，所以这是可选的。 */
export type ChatComposerDropZone = {
  /** 把元素登记成原生 drop 目标（桌面端是 Wails 的 OnFileDrop）。 */
  registerDropZone: DropZoneRegistrar;
  /** 把落盘路径读成图片附件。宿主的文件读取能力。 */
  readImages: (paths: string[]) => Promise<DroppedImageItem[]>;
};

export type ChatComposerProps = Omit<
  AIChatInputProps,
  "onSubmit" | "onEmptyChange" | "onCommandModeChange"
> & {
  onSubmit: (message: ChatComposerSubmit) => void;
  supportsImageInput?: boolean;
  inputHandleRef?: React.RefObject<AIChatInputHandle | null>;
  sendButtonTestId?: string;
  /** 底栏弹性空档**左侧**的宿主内容（例：permission mode / 模型 pill）。 */
  leadingControls?: React.ReactNode;
  /** 底栏弹性空档**右侧**、紧挨提交键的宿主内容（例：用量计量器）。 */
  trailingControls?: React.ReactNode;
  /** 输入框上方、卡片内的插槽（例：排队消息条）。 */
  topSlot?: React.ReactNode;
  /**
   * 快捷键提示。省略走内置那句（按是否编辑模式选词）；显式传 `null` 整条不摆；
   * 传节点则替换 —— agentre-server 在发不出去时用它说明原因。
   */
  shortcutsHint?: React.ReactNode;
  /** 编辑既有消息。true 时挂提示条、提交键变「保存」、附件与宿主槽位全部收起。 */
  editing?: boolean;
  /** 进入编辑模式时载入输入框的初始草稿。换编辑目标会重新载入。 */
  editDraft?: string;
  /** 用户在提示条上取消编辑，或在编辑态按下 Esc。 */
  onCancelEdit?: () => void;
  /** 发送 RPC 在途。true 时提交键转 spinner 并禁用。 */
  sending?: boolean;
  /** 焦点在 composer 内按下 Shift+Tab（桌面端用来循环切换 permission mode）。 */
  onShiftTab?: () => void;
  /** 挂载即聚焦输入框。新建会话场景下让用户一打开就能打字。 */
  autoFocusOnMount?: boolean;
  /** 进出 `!` 命令模式时通知宿主（用于重新解析执行作用域）。 */
  onCommandModeChange?: (active: boolean) => void;
  dropZone?: ChatComposerDropZone;
  onPasteCapture?: (event: React.ClipboardEvent<HTMLFormElement>) => void;
  className?: string;
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

function imageFilesFromClipboard(data: DataTransfer): File[] {
  const itemFiles = Array.from(data.items ?? [])
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((file): file is File => !!file);
  if (itemFiles.length > 0) return itemFiles;
  return Array.from(data.files ?? []).filter((file) =>
    file.type.startsWith("image/"),
  );
}

/**
 * 两端唯一那份输入框外壳：卡片 + 图片附件 + 编辑/命令两种模式的提示条 + 一条恒为
 * 单行的底栏。
 *
 * **两条硬约束**（改这个组件时先读这里）：
 *   1. 底栏恒为单行 —— 内部文字不许折行把行高顶高；
 *   2. 底栏不得横向溢出 —— 提交键不许被裁出可视区。
 * 溢出优先级由类名承载：提交键与宿主的 `leadingControls` 保持 `shrink-0` 不参与
 * 收缩，宿主注入的计量器（`trailingControls`）是唯一带 `min-w-0` 的让位者；
 * 快捷键提示这类一次性教学文案在窄档整条隐藏。
 *
 * 分档读的是 **composer 自身宽度**（`@container/composer`），不是视口：面板实际
 * 宽度取决于宿主的侧栏 / 右侧面板开合，视口宽度读不到它。
 */
export const ChatComposer = React.forwardRef<
  ChatComposerHandle,
  ChatComposerProps
>(function ChatComposer(
  {
    className,
    disabled = false,
    leadingControls,
    trailingControls,
    topSlot,
    shortcutsHint,
    onSubmit,
    supportsImageInput = true,
    sendButtonTestId,
    inputHandleRef,
    editing = false,
    editDraft,
    onCancelEdit,
    sending = false,
    onShiftTab,
    autoFocusOnMount = false,
    onCommandModeChange,
    dropZone,
    onPasteCapture,
    ...inputProps
  },
  ref,
) {
  const { t } = useUiTranslation();
  const ownInputRef = React.useRef<AIChatInputHandle>(null);
  const inputRef = inputHandleRef ?? ownInputRef;
  const fileRef = React.useRef<HTMLInputElement>(null);
  const formRef = React.useRef<HTMLFormElement>(null);
  const [empty, setEmpty] = React.useState(true);
  const [images, setImages] = React.useState<ChatImageAttachment[]>([]);
  const [imageError, setImageError] = React.useState("");
  const [commandMode, setCommandMode] = React.useState(false);

  React.useImperativeHandle(
    ref,
    () => ({
      clearDraft: () => {
        inputRef.current?.clear();
        setImages([]);
        setImageError("");
      },
      restoreDraft: (text: string, restoreImages: ChatImageAttachment[]) => {
        inputRef.current?.loadDraft(text);
        setImages(restoreImages);
        setImageError("");
      },
    }),
    [inputRef],
  );

  // 切进编辑模式（或换了编辑目标）时把目标文本载进编辑器并抓回焦点；退出时清空，
  // 免得上一次的编辑残留干扰下一条新消息。
  const wasEditingRef = React.useRef(false);
  React.useEffect(() => {
    if (editing) {
      if (editDraft !== undefined) inputRef.current?.loadDraft(editDraft);
      inputRef.current?.focus();
      setImages([]);
      setImageError("");
    } else if (wasEditingRef.current) {
      inputRef.current?.clear();
    }
    wasEditingRef.current = editing;
  }, [editing, editDraft, inputRef]);

  const wasAutoFocusRef = React.useRef(autoFocusOnMount);
  React.useEffect(() => {
    const was = wasAutoFocusRef.current;
    wasAutoFocusRef.current = autoFocusOnMount;
    if (!autoFocusOnMount || was) return;
    inputRef.current?.focus();
  }, [autoFocusOnMount, inputRef]);

  React.useEffect(() => {
    if (supportsImageInput) return;
    setImages([]);
    setImageError("");
    if (fileRef.current) fileRef.current.value = "";
  }, [supportsImageInput]);

  const addFiles = React.useCallback(
    async (files: FileList | readonly File[] | null) => {
      try {
        if (disabled || !files || files.length === 0) return;
        const next = Array.from(files);
        if (images.length + next.length > MAX_IMAGE_COUNT) {
          setImageError(
            t("chatComposer.images.tooMany", { count: MAX_IMAGE_COUNT }),
          );
          return;
        }
        const bad = next.find(
          (file) =>
            !IMAGE_ACCEPT.split(",").includes(file.type) ||
            file.size > MAX_IMAGE_BYTES,
        );
        if (bad) {
          setImageError(t("chatComposer.images.unsupported"));
          return;
        }
        const attachments = await Promise.all(next.map(readImage));
        setImages((current) => [...current, ...attachments]);
        setImageError("");
      } catch {
        setImageError(t("chatComposer.images.readFailed"));
      } finally {
        if (fileRef.current) fileRef.current.value = "";
      }
    },
    [disabled, images.length, t],
  );

  // 拖入永不死路：所有没能收编成附件的项都降级为路径文本插进编辑器。
  const handleDroppedPaths = React.useCallback(
    (paths: string[]) => {
      if (disabled || !dropZone) return;
      void (async () => {
        const { attachments, text } = await resolveDroppedPaths(paths, {
          allowImages: !editing && supportsImageInput,
          readImages: dropZone.readImages,
          remainingImageSlots: MAX_IMAGE_COUNT - images.length,
        });
        if (attachments.length > 0) {
          setImages((current) => [...current, ...attachments]);
          setImageError("");
        }
        if (text) inputRef.current?.insertText(text);
      })();
    },
    [disabled, dropZone, editing, images.length, inputRef, supportsImageInput],
  );

  const { isDragOver } = useFileDropZone({
    enabled: !disabled && !!dropZone,
    onPaths: handleDroppedPaths,
    ref: formRef,
    // 没有宿主通道时给一个空注册器：包里不知道任何一端的原生 drop 怎么接。
    registerDropZone: dropZone?.registerDropZone ?? noopRegistrar,
  });

  const submit = React.useCallback(
    (text: string) => {
      if (disabled) return;
      const body = text.trim();
      if (!body && images.length === 0) return;
      onSubmit(images.length ? { images, text: body } : { text: body });
      setImages([]);
      setImageError("");
    },
    [disabled, images, onSubmit],
  );

  function handleFormSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (disabled) return;
    if (empty && images.length > 0) {
      submit("");
      return;
    }
    inputRef.current?.submit();
  }

  function handlePasteCapture(event: React.ClipboardEvent<HTMLFormElement>) {
    onPasteCapture?.(event);
    if (event.defaultPrevented || disabled || editing || !supportsImageInput) {
      return;
    }
    const pasted = imageFilesFromClipboard(event.clipboardData);
    if (pasted.length === 0) return;
    event.preventDefault();
    void addFiles(pasted);
  }

  // Esc 取消编辑：TipTap 的 handleKeyDown 不处理 Esc，所以在 form 层捕获；非编辑
  // 态不消费，让默认行为走。
  //
  // Shift+Tab 交给宿主（桌面端拿它循环切换 permission mode）。本地命令历史的清除
  // 控件保留原生反向焦点；编辑模式也不消费。
  function handleFormKeyDown(event: React.KeyboardEvent<HTMLFormElement>) {
    if (
      !editing &&
      empty &&
      images.length > 0 &&
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      submit("");
      return;
    }
    if (editing && event.key === "Escape") {
      event.preventDefault();
      onCancelEdit?.();
      return;
    }
    if (
      !event.defaultPrevented &&
      !editing &&
      onShiftTab &&
      event.key === "Tab" &&
      event.shiftKey &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey
    ) {
      if (
        event.target instanceof Element &&
        event.target.closest(LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR)
      ) {
        return;
      }
      event.preventDefault();
      onShiftTab();
    }
  }

  const showImageEntry = supportsImageInput && !editing;
  const showHostControls = !editing && !commandMode;
  const hint =
    shortcutsHint === undefined ? (
      <span
        data-slot="composer-shortcuts"
        className="shrink-0 font-mono text-meta leading-none whitespace-nowrap text-muted-foreground @max-[1000px]/composer:hidden"
      >
        {editing
          ? t("chatComposer.shortcuts.edit")
          : t("chatComposer.shortcuts.send")}
      </span>
    ) : (
      shortcutsHint
    );

  return (
    <form
      ref={formRef}
      className={cn(
        // @container/composer：底栏按 composer 自身宽度分档降级 —— 面板实际宽度
        // 取决于宿主的侧栏 / 右侧面板开合，视口宽度读不到它。
        "@container/composer relative flex w-full flex-col overflow-hidden rounded-md border bg-card shadow-xs transition-colors",
        "focus-within:ring-[3px] focus-within:ring-ring/50",
        editing
          ? "border-primary-text/45 focus-within:border-primary-text/70"
          : "border-border focus-within:border-ring",
        className,
      )}
      onSubmit={handleFormSubmit}
      onKeyDown={handleFormKeyDown}
      onPasteCapture={handlePasteCapture}
    >
      {isDragOver ? (
        <div
          className="pointer-events-none absolute inset-2 z-10 flex items-center justify-center rounded-md border-2 border-dashed border-ring bg-background/85 text-sm font-medium text-foreground"
          aria-hidden="true"
        >
          {t("chatComposer.dropHint")}
        </div>
      ) : null}
      {topSlot}
      {editing ? (
        <div
          role="status"
          aria-label={t("chatComposer.editing.aria")}
          className="flex items-center gap-2 border-b border-primary-text/20 bg-primary-soft px-3 py-1.5 text-meta"
        >
          <Pencil
            className="size-3 shrink-0 text-primary-text"
            aria-hidden="true"
          />
          <span className="font-semibold text-primary-text">
            {t("chatComposer.editing.title")}
          </span>
          <span className="text-muted-foreground">·</span>
          <span className="min-w-0 flex-1 truncate text-muted-foreground">
            {t("chatComposer.editing.description")}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-5 shrink-0"
            aria-label={t("chatComposer.editing.cancel")}
            title={t("chatComposer.editing.cancelTitle")}
            onClick={() => onCancelEdit?.()}
          >
            <X className="size-3" aria-hidden="true" />
          </Button>
        </div>
      ) : null}
      {!editing && commandMode ? (
        <div
          role="status"
          className="flex items-center gap-2 border-b border-primary-text/20 bg-primary-soft px-3 py-1.5 text-meta"
        >
          <SquareTerminal
            className="size-3 shrink-0 text-primary-text"
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1 truncate font-medium text-primary-text">
            {t("chatComposer.command.banner")}
          </span>
          <span className="inline-flex items-center gap-1 text-muted-foreground">
            <EyeOff className="size-3" aria-hidden="true" />
            {t("localCommand.notSharedWithAI")}
          </span>
        </div>
      ) : null}
      <div className="flex flex-col gap-1 px-3.5 pt-2.5 pb-1">
        {!editing && images.length ? (
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
          // 挂载即聚焦走 TipTap 自己的 autofocus:它会等 view 附加到 DOM 之后再
          // focus,绕开 mount 时 view 还没挂上的时序问题。下面那个 effect 只管
          // 「挂载之后才被打开」这一档(宿主从 false 翻成 true),两条路都要有。
          autoFocus={inputProps.autoFocus || autoFocusOnMount}
          disabled={disabled}
          onEmptyChange={setEmpty}
          onCommandModeChange={(active) => {
            setCommandMode(active);
            onCommandModeChange?.(active);
          }}
          onSubmit={submit}
        />
        {/* 底栏恒为单行（Hard invariant 1），且不得横向溢出（Hard invariant 2）。 */}
        <div
          data-slot="composer-bar"
          className="flex flex-nowrap items-center gap-2 py-1"
        >
          {showImageEntry ? (
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
                className="shrink-0"
                aria-label={t("chatComposer.images.add")}
                disabled={disabled || images.length >= MAX_IMAGE_COUNT}
                onClick={() => fileRef.current?.click()}
              >
                <ImagePlus aria-hidden="true" className="size-4" />
              </Button>
            </>
          ) : null}
          {hint}
          {showHostControls ? leadingControls : null}
          <div data-slot="composer-gap" className="min-w-0 flex-1" />
          {showHostControls ? trailingControls : null}
          <SubmitControl
            commandMode={commandMode}
            disabled={disabled}
            editing={editing}
            empty={empty}
            hasImages={images.length > 0}
            sending={sending}
            testId={sendButtonTestId}
          />
        </div>
        {imageError ? (
          <p role="alert" className="text-xs text-destructive">
            {imageError}
          </p>
        ) : null}
      </div>
    </form>
  );
});

/**
 * 提交键三态。命令模式与编辑模式各换一副样子 —— 同一个按钮在这三种情形下做的
 * 是三件不同的事，长得一样会让人按错。
 */
function SubmitControl({
  commandMode,
  disabled,
  editing,
  empty,
  hasImages,
  sending,
  testId,
}: {
  commandMode: boolean;
  disabled: boolean;
  editing: boolean;
  empty: boolean;
  hasImages: boolean;
  sending: boolean;
  testId?: string;
}) {
  const { t } = useUiTranslation();

  if (editing) {
    return (
      <Button
        type="submit"
        data-testid={testId}
        size="xs"
        className="shrink-0"
        disabled={empty}
        aria-label={t("common.save")}
      >
        <Check className="size-3.5" aria-hidden="true" />
        {t("common.save")}
      </Button>
    );
  }

  if (commandMode) {
    return (
      <Button
        type="submit"
        data-testid={testId}
        size="icon-sm"
        className="shrink-0"
        aria-label={t("chatComposer.command.run")}
      >
        <SquareTerminal className="size-4" aria-hidden="true" />
      </Button>
    );
  }

  return (
    <Button
      type="submit"
      data-testid={testId}
      size="icon-sm"
      className="shrink-0"
      disabled={disabled || sending || (empty && !hasImages)}
      aria-label={sending ? t("chatComposer.sending") : t("chatComposer.send")}
      title={t("chatComposer.sendTitle")}
    >
      {sending ? (
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
      ) : (
        <SendHorizontal aria-hidden="true" className="size-4" />
      )}
    </Button>
  );
}

const noopRegistrar: DropZoneRegistrar = () => () => {};
