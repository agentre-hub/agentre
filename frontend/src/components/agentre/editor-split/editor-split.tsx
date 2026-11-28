import * as React from "react";
import { Maximize2, Minimize2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button, cn } from "@agentre-hub/agentre-ui";
import { detectBrowserPlatform } from "@/lib/platform";
import { FILE_RATIO_LIMITS } from "@/stores/editor-split-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";

import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import {
  formatFocusedToolWindowShortcut,
  matchFocusedToolWindowShortcut,
} from "../shortcuts/tool-window-shortcuts";

import { useEditorSplit } from "./use-editor-split";

/** 键盘每按一下改变的比例（与工具窗分隔条同一步长）。 */
const KEY_STEP = 0.02;

type Props = {
  sessionId: number;
  /** 文件一侧：这个会话的预览面板（标签条、Diff / 文件、HTML 渲染、Monaco）。 */
  file: React.ReactNode;
  /** 对话一侧：对话内容页。它在树里的位置不随分屏 / 占满变化，不会被重新挂载。 */
  children: React.ReactNode;
};

/**
 * 编辑区（spec「编辑区与文件」）：平时只有对话；会话开着文件时分成 对话 | 文件 两栏，
 * 中间的分隔条可拖、比例全局记住；文件占满时对话缩成左侧窄栏（窄栏里画什么由对话
 * 一侧自己按 useEditorSplit 决定）。
 *
 * ⇧⌘' 与工具窗的「最大化获得焦点的那一块」是同一个按键定义（同一个匹配函数）：
 * 焦点在某个工具窗里时由工具窗自己处理（toggleMaximize），焦点在文件一侧——或占满
 * 时在对话窄栏里——时由这里处理成占满 / 还原。两处各管自己那块焦点区域，不会同时
 * 收到同一次按键。
 */
export function EditorSplit({ sessionId, file, children }: Props) {
  const platform =
    useOptionalShortcutsContext()?.platform ?? detectBrowserPlatform();
  const { layout, fileRatio, setFileRatio, toggleFileFull } =
    useEditorSplit(sessionId);
  const rootRef = React.useRef<HTMLDivElement>(null);
  const fileRef = React.useRef<HTMLDivElement>(null);

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (layout === "chat" || e.nativeEvent.defaultPrevented) return;
    if (matchFocusedToolWindowShortcut(e.nativeEvent, platform) !== "maximize")
      return;
    const inFile = fileRef.current?.contains(e.target as Node) ?? false;
    if (!inFile && layout !== "file-full") return;
    e.preventDefault();
    toggleFileFull();
  };

  return (
    <div
      ref={rootRef}
      data-testid="editor-split"
      data-layout={layout}
      onKeyDown={onKeyDown}
      className="flex min-h-0 min-w-0 flex-1"
    >
      <div
        className={cn(
          "flex min-h-0 min-w-0",
          layout === "file-full" ? "w-72 shrink-0" : "flex-1",
        )}
      >
        {children}
      </div>
      {layout === "split" ? (
        <EditorSplitDivider
          containerRef={rootRef}
          ratio={fileRatio}
          onRatioChange={setFileRatio}
        />
      ) : null}
      {layout === "chat" ? null : (
        <div
          ref={fileRef}
          style={
            layout === "split"
              ? { flexBasis: `${fileRatio * 100}%` }
              : undefined
          }
          className={cn(
            "flex min-h-0 min-w-0",
            layout === "split" ? "shrink-0 grow-0" : "flex-1",
          )}
        >
          {file}
        </div>
      )}
    </div>
  );
}

/**
 * 对话 | 文件 之间的分隔条：指针拖，或获得焦点后用方向键调整（←让文件变宽）。
 * 它压在对话内容页右侧那道缝里，不额外占宽度。
 */
function EditorSplitDivider({
  containerRef,
  ratio,
  onRatioChange,
}: {
  /** 比例相对的容器（编辑区整宽）。 */
  containerRef: React.RefObject<HTMLElement | null>;
  /** 文件一侧占编辑区宽度的比例。 */
  ratio: number;
  onRatioChange: (ratio: number) => void;
}) {
  const { t } = useTranslation();
  const [dragging, setDragging] = React.useState(false);

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0) return;
    e.preventDefault();
    e.currentTarget.focus({ preventScroll: true });
    const onMove = (ev: PointerEvent) => {
      onRatioChange((rect.right - ev.clientX) / rect.width);
    };
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.removeEventListener("pointercancel", onUp);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      setDragging(false);
    };
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onUp);
    document.addEventListener("pointercancel", onUp);
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    setDragging(true);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey || e.metaKey || e.ctrlKey !== e.shiftKey) return;
    const delta = e.key === "ArrowLeft" ? 1 : e.key === "ArrowRight" ? -1 : 0;
    if (delta === 0) return;
    e.preventDefault();
    onRatioChange(ratio + delta * KEY_STEP);
  };

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation="vertical"
      aria-label={t("editorSplit.divider")}
      aria-valuemin={Math.round(FILE_RATIO_LIMITS.min * 100)}
      aria-valuemax={Math.round(FILE_RATIO_LIMITS.max * 100)}
      aria-valuenow={Math.round(ratio * 100)}
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      className={cn(
        "relative z-20 -ml-2 w-2 shrink-0 cursor-col-resize touch-none outline-none select-none",
        "after:absolute after:inset-y-3 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-transparent after:transition-colors after:content-['']",
        "hover:after:bg-primary/40 focus-visible:after:bg-primary data-[dragging]:after:bg-primary/60",
      )}
    />
  );
}

/**
 * 文件一侧标签条右端的两个动作：占满 / 还原（⇧⌘'）、关闭全部文件。
 */
export function EditorSplitFileActions({ sessionId }: { sessionId: number }) {
  const { t } = useTranslation();
  const platform =
    useOptionalShortcutsContext()?.platform ?? detectBrowserPlatform();
  const { layout, toggleFileFull } = useEditorSplit(sessionId);
  const closeAll = useFilePreviewTabsStore((s) => s.closeAllPreviewTabs);
  const full = layout === "file-full";
  const fillLabel = full ? t("editorSplit.restore") : t("editorSplit.fill");
  const chord = formatFocusedToolWindowShortcut("maximize", platform);
  const FillIcon = full ? Minimize2 : Maximize2;

  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={fillLabel}
        title={chord ? `${fillLabel} ${chord}` : fillLabel}
        onClick={toggleFileFull}
      >
        <FillIcon aria-hidden="true" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={t("editorSplit.closeAll")}
        title={t("editorSplit.closeAll")}
        onClick={() => closeAll(sessionId)}
      >
        <X aria-hidden="true" />
      </Button>
    </>
  );
}
