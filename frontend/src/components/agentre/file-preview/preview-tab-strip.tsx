import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  ChevronDown,
  Copy,
  Pin,
  PinOff,
  SquareX,
  X,
  XCircle,
} from "lucide-react";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  copyTextWithToast,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";
import {
  useFilePreviewTabsStore,
  type FilePreviewTab,
} from "@/stores/file-preview-tabs-store";

import { FileTypeIcon } from "../file-type-icon";
import { basename } from "./file-meta";

type Props = {
  sessionId: number;
};

/**
 * PreviewTabStrip 是预览面板 header 之上的标签条（spec「多标签预览」）：只在标签数
 * ≥ 2 时渲染——单文件路径与改造前完全一致，不为多标签能力给单文件用户加一行 chrome。
 *
 * 语义与应用内既有的会话标签条（chat-tabs/tab-strip.tsx）保持一致：临时标签斜体、
 * 活动标签背景提亮加顶部主色条、双击转常驻。右键菜单按 spec「右键菜单 · 预览标签」
 * 分三组：固定 / 取消固定；关闭 / 关闭其他 / 全部关闭；复制相对路径（标签的 path
 * 就是会话级 relPath）。标签等比收缩到下限后横向滚动，右端是常驻的溢出菜单（列出
 * 全部标签、可直接切换），活动标签始终自动滚入可见区。
 */
export function PreviewTabStrip({ sessionId }: Props) {
  const { t } = useTranslation();
  const entry = useFilePreviewTabsStore(
    (s) => s.previewTabsBySession[sessionId],
  );
  const activatePreviewTab = useFilePreviewTabsStore(
    (s) => s.activatePreviewTab,
  );
  const promoteActivePreviewTab = useFilePreviewTabsStore(
    (s) => s.promoteActivePreviewTab,
  );
  const togglePreviewTabPin = useFilePreviewTabsStore(
    (s) => s.togglePreviewTabPin,
  );
  const closePreviewTab = useFilePreviewTabsStore((s) => s.closePreviewTab);
  const closeOtherPreviewTabs = useFilePreviewTabsStore(
    (s) => s.closeOtherPreviewTabs,
  );
  const closeAllPreviewTabs = useFilePreviewTabsStore(
    (s) => s.closeAllPreviewTabs,
  );

  const tabs = entry?.tabs ?? [];
  if (tabs.length < 2) return null;

  // ← / → 在标签间移动（served requirement「键盘与无障碍」）：焦点走到哪个标签
  // 就把它激活（自动激活），与鼠标点击同一个语义；两端不回绕，和文件列表的上下
  // 键一致。roving tabindex 与「哪个是活动标签」是同一件事，不另存一份焦点态。
  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.defaultPrevented) return;
    const step =
      event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (step === 0) return;
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]'),
    );
    const current = (event.target as HTMLElement).closest<HTMLElement>(
      '[role="tab"]',
    );
    const index = current ? items.indexOf(current) : -1;
    if (index < 0) return;
    event.preventDefault();
    const next = items[index + step];
    if (!next) return;
    const path = next.dataset.tabPath;
    if (path !== undefined) activatePreviewTab(sessionId, path);
    next.focus();
  };

  return (
    <div
      role="tablist"
      aria-label={t("chatContext.filePreview.tabsAria")}
      onKeyDown={handleKeyDown}
      className="flex h-[34px] shrink-0 items-stretch overflow-hidden border-b border-border bg-muted"
    >
      <div className="scrollbar-none flex h-full min-h-0 min-w-0 flex-1 items-stretch overflow-x-auto overflow-y-hidden">
        {tabs.map((tab) => (
          <PreviewTab
            key={tab.path}
            tab={tab}
            active={tab.path === entry?.activePath}
            onActivate={() => activatePreviewTab(sessionId, tab.path)}
            onDoublePromote={() => {
              activatePreviewTab(sessionId, tab.path);
              promoteActivePreviewTab(sessionId);
            }}
            onTogglePin={() => togglePreviewTabPin(sessionId, tab.path)}
            onClose={() => closePreviewTab(sessionId, tab.path)}
            onCloseOthers={() => closeOtherPreviewTabs(sessionId, tab.path)}
            onCloseAll={() => closeAllPreviewTabs(sessionId)}
          />
        ))}
      </div>
      <div className="flex h-full shrink-0 items-center border-l border-border px-1">
        <PreviewTabOverflowMenu
          tabs={tabs}
          activePath={entry?.activePath ?? null}
          onSelect={(path) => activatePreviewTab(sessionId, path)}
        />
      </div>
    </div>
  );
}

function PreviewTab({
  tab,
  active,
  onActivate,
  onDoublePromote,
  onTogglePin,
  onClose,
  onCloseOthers,
  onCloseAll,
}: {
  tab: FilePreviewTab;
  active: boolean;
  onActivate: () => void;
  onDoublePromote: () => void;
  onTogglePin: () => void;
  onClose: () => void;
  onCloseOthers: () => void;
  onCloseAll: () => void;
}) {
  const { t } = useTranslation();
  const ref = React.useRef<HTMLSpanElement>(null);

  // 活动标签始终滚入可见区：标签条超出宽度后靠横向滚动而不是继续压缩。
  React.useEffect(() => {
    if (!active) return;
    ref.current?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [active]);

  return (
    <ContextMenu>
      <ContextMenuTrigger
        ref={ref}
        className="inline-flex h-full min-w-0 flex-shrink"
      >
        <div
          role="tab"
          aria-selected={active}
          // 标签自身走 roving tabindex：Tab 只停在活动标签上，其余靠 ← / →
          // 走到（每个标签里的关闭按钮照旧可 Tab 到——那是非活动标签唯一的
          // 键盘关闭途径，Esc 只管当前活动的那个）。
          tabIndex={active ? 0 : -1}
          data-active={active}
          data-preview={tab.isPreview}
          data-tab-path={tab.path}
          title={tab.path}
          onClick={onActivate}
          onDoubleClick={onDoublePromote}
          className={cn(
            "relative flex h-full min-w-[96px] max-w-[150px] flex-shrink items-center gap-1.5 border-r border-border pl-2.5 pr-1.5 text-2xs",
            active
              ? "bg-background text-foreground"
              : "text-muted-foreground hover:bg-card",
          )}
        >
          {active ? (
            <span className="absolute left-0 top-0 h-[2px] w-full bg-primary" />
          ) : null}
          <FileTypeIcon path={tab.path} testId="preview-tab-file-icon" />
          {tab.isPinned ? (
            <Pin
              data-testid="preview-tab-pin-icon"
              className="size-2.5 shrink-0 rotate-[30deg] text-primary"
              aria-hidden="true"
            />
          ) : null}
          <span
            className={cn(
              "min-w-0 flex-1 truncate font-mono",
              active && "font-semibold",
              // 临时标签用斜体区分（颜色已被主色与状态色占满）。
              tab.isPreview && "italic",
            )}
          >
            {basename(tab.path)}
          </span>
          <button
            type="button"
            aria-label={t("chatTabs.actions.closeTab")}
            className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            onClick={(e) => {
              e.stopPropagation();
              onClose();
            }}
          >
            <X className="size-2.5" aria-hidden="true" />
          </button>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onTogglePin}>
          {tab.isPinned ? <PinOff /> : <Pin />}
          <span>
            {tab.isPinned
              ? t("chatTabs.actions.unpin")
              : t("chatTabs.actions.pin")}
          </span>
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onSelect={onClose}>
          <X />
          <span>{t("common.close")}</span>
        </ContextMenuItem>
        <ContextMenuItem onSelect={onCloseOthers}>
          <XCircle />
          <span>{t("chatTabs.actions.closeOthers")}</span>
        </ContextMenuItem>
        <ContextMenuItem onSelect={onCloseAll}>
          <SquareX />
          <span>{t("chatContext.filePreview.closeAll")}</span>
        </ContextMenuItem>
        <ContextMenuSeparator />
        {/* 标签的 path 就是会话级 relPath，与行菜单的「复制相对路径」同一个语义、
            复用同一个 key。 */}
        <ContextMenuItem
          onSelect={() =>
            void copyTextWithToast(tab.path, {
              successTitle: t("common.copied"),
            })
          }
        >
          <Copy />
          <span>{t("chatContext.row.copyRelPath")}</span>
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/** 右端常驻的溢出菜单：列出全部标签（含被滚出可见区的），点一条直接切过去。 */
function PreviewTabOverflowMenu({
  tabs,
  activePath,
  onSelect,
}: {
  tabs: FilePreviewTab[];
  activePath: string | null;
  onSelect: (path: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("chatTabs.overflow.openMenu")}
          className="inline-flex size-6 items-center justify-center rounded-md hover:bg-accent"
        >
          <ChevronDown className="size-3.5" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="w-72 p-0">
        <div className="flex flex-col py-1">
          {tabs.map((tab) => (
            <DropdownMenuItem
              key={tab.path}
              data-active={tab.path === activePath}
              onSelect={() => onSelect(tab.path)}
              className={cn(
                "h-7 gap-2 rounded-none px-3 text-xs",
                tab.path === activePath && "bg-sidebar-active-bg",
              )}
            >
              <FileTypeIcon
                path={tab.path}
                testId="preview-overflow-file-icon"
              />
              <span
                className={cn(
                  "shrink-0 truncate font-mono",
                  tab.isPreview && "italic",
                )}
              >
                {basename(tab.path)}
              </span>
              <span
                dir="rtl"
                className="min-w-0 flex-1 truncate text-left font-mono text-3xs text-muted-foreground"
              >
                {tab.path}
              </span>
            </DropdownMenuItem>
          ))}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
