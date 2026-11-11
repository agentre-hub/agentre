import * as React from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import {
  ChevronLeft,
  ChevronRight,
  Copy,
  Maximize2,
  Minimize2,
  X,
} from "lucide-react";

import { useUiTranslation } from "../i18n";
import { copyImageWithToast } from "../lib/clipboard-image";
import { cn } from "../lib/utils";

/** 查看器里的一张图：转录图片块与输入框附件都是 data URL。 */
export type ViewerImage = {
  dataUrl: string;
  mediaType?: string;
  name?: string;
};

export type ImageViewerProps = {
  /** 同一组图（同一条消息的图片块 / 输入框当前全部附件）。 */
  images: ViewerImage[];
  /** 打开时显示第几张。每次打开都从这里开始。 */
  startIndex: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /**
   * 关闭后焦点回到哪里（打开它的缩略图）。
   *
   * 不能指望 Radix 记住「打开前的焦点」：WebKit 里鼠标点按钮**不会**让按钮获得焦点，
   * 打开前的焦点是 body，关掉后焦点就丢在 body 上。宿主把缩略图交进来才稳。
   */
  returnFocusTo?: React.RefObject<HTMLElement | null>;
};

/** ⌘ 是 Apple 平台的主修饰键，其余平台是 Ctrl。 */
function isApplePlatform(): boolean {
  return /Mac|iPhone|iPad|iPod/.test(navigator.userAgent);
}

/** `image/png` → `PNG`；没给 mediaType 时从 data URL 头里取。 */
function formatLabel(image: ViewerImage): string {
  const type =
    image.mediaType || /^data:([^;,]+)/.exec(image.dataUrl)?.[1] || "";
  return type.replace(/^image\//, "").toUpperCase();
}

/** 当前选区是否落在查看器里、且选中了文字。 */
function hasSelectionInside(container: Element): boolean {
  const selection = window.getSelection();
  if (!selection || !selection.toString()) return false;
  return (
    container.contains(selection.anchorNode) ||
    container.contains(selection.focusNode)
  );
}

const barButton =
  "inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-2.5 text-xs text-media-foreground/80 transition-colors hover:bg-media-foreground/10 hover:text-media-foreground focus-visible:focus-outline";

const navButton =
  "absolute top-1/2 inline-flex size-10 -translate-y-1/2 cursor-pointer items-center justify-center rounded-full bg-media-foreground/10 text-media-foreground transition-colors hover:bg-media-foreground/20 focus-visible:focus-outline";

/**
 * 全窗口图片查看器（spec 2026-11-06-image-viewer-and-copy「查看器」一节）。
 *
 * 遮罩两个主题都是深色（`bg-media-scrim`，决策 2）。默认适应窗口、不放大超过原始
 * 尺寸；点图片或顶栏按钮切到原始大小（1:1，超出部分滚动），换张时回到适应窗口。
 */
export function ImageViewer({
  images,
  startIndex,
  open,
  onOpenChange,
  returnFocusTo,
}: ImageViewerProps) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay
          data-slot="image-viewer-overlay"
          className="fixed inset-0 z-50 bg-media-scrim motion-safe:data-[state=open]:animate-in motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=closed]:fade-out-0"
        />
        {/* Content 只在打开时挂载：每次打开，张数与显示方式都从头来。 */}
        <ViewerContent
          images={images}
          startIndex={startIndex}
          onClose={() => onOpenChange(false)}
          returnFocusTo={returnFocusTo}
        />
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

type Loaded = { width: number; height: number };

function ViewerContent({
  images,
  startIndex,
  onClose,
  returnFocusTo,
}: {
  images: ViewerImage[];
  startIndex: number;
  onClose: () => void;
  returnFocusTo?: React.RefObject<HTMLElement | null>;
}) {
  const { t } = useUiTranslation();
  const [index, setIndex] = React.useState(() =>
    Math.min(Math.max(startIndex, 0), Math.max(images.length - 1, 0)),
  );
  const [actual, setActual] = React.useState(false);
  const [loaded, setLoaded] = React.useState<Loaded | null>(null);
  const [failed, setFailed] = React.useState(false);

  const image = images[index];
  const many = images.length > 1;
  const hasPrevious = index > 0;
  const hasNext = index < images.length - 1;
  const apple = isApplePlatform();

  const go = (next: number) => {
    if (next < 0 || next >= images.length || next === index) return;
    setIndex(next);
    setActual(false);
    setLoaded(null);
    setFailed(false);
  };

  const copy = () => {
    if (image && !failed) void copyImageWithToast(image);
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowLeft") {
      go(index - 1);
      return;
    }
    if (event.key === "ArrowRight") {
      go(index + 1);
      return;
    }
    const primary = apple ? event.metaKey : event.ctrlKey;
    if (
      primary &&
      !event.altKey &&
      !event.shiftKey &&
      event.key.toLowerCase() === "c"
    ) {
      // 查看器里有选中的文字（比如文件名）时，⌘C 复制的是文字；打开前留在
      // 查看器外面的选区（被遮罩挡住、看不见）不算，照样复制图片。
      if (hasSelectionInside(event.currentTarget)) return;
      event.preventDefault();
      copy();
    }
  };

  const closeOnBackdrop = (event: React.MouseEvent) => {
    if (event.target === event.currentTarget) onClose();
  };

  if (!image) return null;

  const title = image.name
    ? t("imageViewer.titleWithName", { name: image.name })
    : t("imageViewer.title");

  const hint = [
    t("imageViewer.hintClose"),
    many ? t("imageViewer.hintSwitch") : null,
    t("imageViewer.hintCopy", { mod: apple ? "⌘" : "Ctrl+" }),
    t("imageViewer.hintToggle"),
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <DialogPrimitive.Content
      data-slot="image-viewer-content"
      aria-describedby={undefined}
      onKeyDown={onKeyDown}
      onCloseAutoFocus={(event) => {
        const target = returnFocusTo?.current;
        if (target) {
          event.preventDefault();
          target.focus();
        }
      }}
      onClick={closeOnBackdrop}
      className="fixed inset-0 z-50 flex flex-col text-media-foreground outline-none motion-safe:data-[state=open]:animate-in motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=closed]:fade-out-0"
    >
      <DialogPrimitive.Title className="sr-only">{title}</DialogPrimitive.Title>
      <div className="flex h-12 shrink-0 items-center gap-3 px-4">
        {image.name ? (
          <span
            data-selectable-text="true"
            className="min-w-0 truncate text-sm text-media-foreground/90"
          >
            {image.name}
          </span>
        ) : null}
        {loaded && !failed ? (
          <span className="shrink-0 font-mono text-2xs text-media-foreground/50">
            {`${loaded.width} × ${loaded.height} · ${formatLabel(image)}`}
          </span>
        ) : null}
        {many ? (
          <span className="shrink-0 font-mono text-2xs text-media-foreground/50">
            {`${index + 1} / ${images.length}`}
          </span>
        ) : null}
        <div className="min-w-0 flex-1" />
        {failed ? null : (
          <button type="button" className={barButton} onClick={copy}>
            <Copy aria-hidden="true" className="size-3.5" />
            {t("imageViewer.copy")}
          </button>
        )}
        <button
          type="button"
          className={barButton}
          onClick={() => setActual((value) => !value)}
        >
          {actual ? (
            <Minimize2 aria-hidden="true" className="size-3.5" />
          ) : (
            <Maximize2 aria-hidden="true" className="size-3.5" />
          )}
          {actual ? t("imageViewer.fitToWindow") : t("imageViewer.actualSize")}
        </button>
        <DialogPrimitive.Close
          aria-label={t("imageViewer.close")}
          className={cn(barButton, "w-8 justify-center px-0")}
        >
          <X aria-hidden="true" className="size-4" />
        </DialogPrimitive.Close>
      </div>
      <div
        data-testid="image-viewer-stage"
        data-display={actual ? "actual" : "fit"}
        onClick={closeOnBackdrop}
        className={cn(
          "relative flex min-h-0 flex-1",
          actual
            ? "overflow-auto"
            : "items-center justify-center overflow-hidden px-16 pb-4",
        )}
      >
        {failed ? (
          <span className="m-auto text-sm text-media-foreground/70">
            {t("imageViewer.decodeFailed")}
          </span>
        ) : (
          <img
            key={index}
            data-testid="image-viewer-image"
            src={image.dataUrl}
            alt={image.name || formatLabel(image)}
            draggable={false}
            onLoad={(event) =>
              setLoaded({
                width: event.currentTarget.naturalWidth,
                height: event.currentTarget.naturalHeight,
              })
            }
            onError={() => setFailed(true)}
            onClick={() => setActual((value) => !value)}
            className={cn(
              "select-none rounded-sm",
              actual
                ? "m-auto max-w-none shrink-0 cursor-zoom-out"
                : "max-h-full max-w-full cursor-zoom-in object-contain",
            )}
          />
        )}
        {hasPrevious ? (
          <button
            type="button"
            aria-label={t("imageViewer.previous")}
            className={cn(navButton, "left-3")}
            onClick={() => go(index - 1)}
          >
            <ChevronLeft aria-hidden="true" className="size-5" />
          </button>
        ) : null}
        {hasNext ? (
          <button
            type="button"
            aria-label={t("imageViewer.next")}
            className={cn(navButton, "right-3")}
            onClick={() => go(index + 1)}
          >
            <ChevronRight aria-hidden="true" className="size-5" />
          </button>
        ) : null}
      </div>
      <div
        data-testid="image-viewer-hint"
        className="shrink-0 pb-3 text-center text-2xs text-media-foreground/45"
      >
        {hint}
      </div>
    </DialogPrimitive.Content>
  );
}
