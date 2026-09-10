// frontend/src/components/agentre/chat-tabs/tab-strip.tsx
import * as React from "react";
import { Pin, PinOff, X, XCircle, ArrowRightFromLine } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  horizontalListSortingStrategy,
  sortableKeyboardCoordinates,
  useSortable,
} from "@dnd-kit/sortable";
type DndTransform = { x: number; y: number; scaleX: number; scaleY: number };

function transformToString(transform: DndTransform | null): string | undefined {
  if (!transform) return undefined;
  const { x, y, scaleX, scaleY } = transform;
  return `translate3d(${x}px, ${y}px, 0) scaleX(${scaleX}) scaleY(${scaleY})`;
}

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@agentre-hub/agentre-ui";

import { useSessionAttentionList } from "@/stores/attention-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { Tab } from "./tab";
import { TabOverflowMenu } from "./tab-overflow-menu";
import { TabTooltip } from "./tab-tooltip";
import { useAttentionBump } from "./use-attention-bump";
import { useTabsView, type TabView } from "./use-tabs-view";

export function TabStrip() {
  const sortedTabs = useTabsView();
  const tabs = useChatTabsStore((s) => s.tabs);
  const activeTabId = useChatTabsStore((s) => s.activeTabId);
  const setActive = useChatTabsStore((s) => s.setActive);
  const closeTab = useChatTabsStore((s) => s.closeTab);
  const closeOthers = useChatTabsStore((s) => s.closeOthers);
  const closeTabsToRight = useChatTabsStore((s) => s.closeTabsToRight);
  const promoteCurrent = useChatTabsStore((s) => s.promoteCurrent);
  const togglePin = useChatTabsStore((s) => s.togglePin);
  const moveTab = useChatTabsStore((s) => s.moveTab);

  // attention bump: 只对 session tab 计算
  const sessionTabIds = React.useMemo(
    () =>
      tabs
        .map((t) => (t.meta.kind === "session" ? t.meta.sessionId : 0))
        .filter((sid) => sid > 0),
    [tabs],
  );
  const attentionItems = useSessionAttentionList(sessionTabIds);
  const attentionTabIds = React.useMemo(() => {
    const ids = new Set<string>();
    const bySid = new Map<number, true>();
    for (const x of attentionItems) bySid.set(x.sessionId, true);
    for (const t of tabs) {
      if (t.meta.kind !== "session") {
        continue;
      }
      if (bySid.has(t.meta.sessionId)) ids.add(t.id);
    }
    return ids;
  }, [attentionItems, tabs]);
  useAttentionBump(attentionTabIds);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  function onDragEnd(e: DragEndEvent) {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const from = tabs.findIndex((t) => t.id === String(active.id));
    const to = tabs.findIndex((t) => t.id === String(over.id));
    if (from < 0 || to < 0) return;
    moveTab(from, to);
  }

  return (
    <div
      role="tablist"
      className="flex h-[38px] shrink-0 items-center overflow-hidden border-b border-border bg-secondary"
    >
      <div className="scrollbar-none flex h-full min-h-0 min-w-0 flex-1 items-center overflow-x-auto overflow-y-hidden">
        <DndContext sensors={sensors} onDragEnd={onDragEnd}>
          <SortableContext
            items={sortedTabs.map((t) => t.id)}
            strategy={horizontalListSortingStrategy}
          >
            {sortedTabs.map((t, idx) => {
              const isLast = idx === sortedTabs.length - 1;
              return (
                <TabTooltip
                  key={t.id}
                  title={t.title}
                  projectChain={t.projectChain}
                  projectColor={t.projectColor}
                  status={t.status}
                  sessionId={t.sessionId}
                  worktreeBranch={t.worktreeBranch}
                  keyboardIndex={idx < 9 ? idx + 1 : null}
                  lastMessageAt={t.lastMessageAt}
                >
                  <SortableTab
                    tab={t}
                    active={t.id === activeTabId}
                    isLast={isLast}
                    onActivate={() => setActive(t.id)}
                    onClose={() => closeTab(t.id)}
                    onDoublePromote={() => {
                      setActive(t.id);
                      promoteCurrent();
                    }}
                    onTogglePin={() => togglePin(t.id)}
                    onCloseOthers={() => closeOthers(t.id)}
                    onCloseToRight={() => closeTabsToRight(t.id)}
                  />
                </TabTooltip>
              );
            })}
          </SortableContext>
        </DndContext>
      </div>
      {sortedTabs.length > 0 ? (
        <div className="flex h-full items-center gap-1 border-l border-border px-1">
          <TabOverflowMenu />
        </div>
      ) : null}
    </div>
  );
}

type SortableTabProps = {
  tab: TabView;
  active: boolean;
  isLast: boolean;
  onActivate: () => void;
  onClose: () => void;
  onDoublePromote: () => void;
  onTogglePin: () => void;
  onCloseOthers: () => void;
  onCloseToRight: () => void;
} & Omit<React.HTMLAttributes<HTMLSpanElement>, "children">;

const SortableTab = React.forwardRef<HTMLSpanElement, SortableTabProps>(
  function SortableTab(
    {
      tab,
      active,
      isLast,
      onActivate,
      onClose,
      onDoublePromote,
      onTogglePin,
      onCloseOthers,
      onCloseToRight,
      ...rest
    },
    forwardedRef,
  ) {
    const { t } = useTranslation();
    const {
      attributes,
      listeners,
      setNodeRef,
      transform,
      transition,
      isDragging,
    } = useSortable({ id: tab.id });

    const style: React.CSSProperties = {
      transform: transformToString(transform),
      transition,
      opacity: isDragging ? 0.5 : undefined,
    };

    // 合并外面 forwardedRef(Tooltip 给的) 和 dnd-kit 的 setNodeRef
    const setRef = React.useCallback(
      (node: HTMLSpanElement | null) => {
        setNodeRef(node);
        if (typeof forwardedRef === "function") forwardedRef(node);
        else if (forwardedRef) forwardedRef.current = node;
      },
      [setNodeRef, forwardedRef],
    );

    return (
      <ContextMenu>
        <ContextMenuTrigger
          ref={setRef}
          className="inline-flex h-full min-w-0 flex-shrink"
          style={style}
          {...attributes}
          {...rest}
          {...listeners}
        >
          <Tab
            title={tab.title}
            kind={tab.kind}
            avatar={tab.avatar}
            active={active}
            isPreview={tab.isPreview}
            isPinned={tab.isPinned}
            status={tab.status}
            projectColor={tab.projectColor}
            worktree={tab.worktree}
            pillText={tab.pillText}
            onActivate={onActivate}
            onClose={onClose}
            onDoublePromote={onDoublePromote}
          />
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
          <ContextMenuItem onSelect={onCloseToRight} disabled={isLast}>
            <ArrowRightFromLine />
            <span>{t("chatTabs.actions.closeRight")}</span>
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    );
  },
);
