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
  TooltipProvider,
  cn,
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
      className="flex h-[38px] shrink-0 items-center overflow-hidden bg-chrome"
    >
      {/* pl-5 = 内容页圆角 12px + 反向圆角 8px：第一个 tab 的反向圆角落在内容页平直的
          上沿；pr-2 让最后一个 tab 的反向圆角在滚到底时不被裁掉。
          isolate：所有 tab 共用这一个层叠上下文（tab 在负层、活动 tab 高一层、项目色线
          再高一层、键盘焦点环在最上）。各个外壳自成层的话，后一格的悬停底会画在前一格
          活动 tab 的反向圆角之上，项目色线也会被反向圆角咬出缺口。 */}
      <div className="scrollbar-none isolate flex h-full min-h-0 min-w-0 flex-1 items-center overflow-x-auto overflow-y-hidden pl-5 pr-2">
        {/* 悬停卡的延迟由这一个 Provider 统一管：首次停 400ms，卡片开过之后 0.5s 内滑到
            相邻 Tab 立刻换卡，不再每格重新等。 */}
        <TooltipProvider delayDuration={400} skipDelayDuration={500}>
          <DndContext sensors={sensors} onDragEnd={onDragEnd}>
            <SortableContext
              items={sortedTabs.map((t) => t.id)}
              strategy={horizontalListSortingStrategy}
            >
              {sortedTabs.map((t, idx) => {
                const isLast = idx === sortedTabs.length - 1;
                // 项目色线连成整段：只在前 / 后一格没有线的那一头收成圆头。
                const lineStart = !sortedTabs[idx - 1]?.projectColor;
                const lineEnd = !sortedTabs[idx + 1]?.projectColor;
                return (
                  <TabTooltip
                    key={t.id}
                    title={t.title}
                    projectChain={t.projectChain}
                    projectColor={t.projectColor}
                    status={t.status}
                    worktreeBranch={t.worktreeBranch}
                    keyboardIndex={idx < 9 ? idx + 1 : null}
                    lastMessageAt={t.lastMessageAt}
                  >
                    <SortableTab
                      tab={t}
                      active={t.id === activeTabId}
                      isLast={isLast}
                      lineStart={lineStart}
                      lineEnd={lineEnd}
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
        </TooltipProvider>
      </div>
      {sortedTabs.length > 0 ? (
        <div className="flex h-full items-center gap-1 px-1">
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
  /** 项目色线这一段是不是整段的头 / 尾（前 / 后一格没有线）。 */
  lineStart: boolean;
  lineEnd: boolean;
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
      lineStart,
      lineEnd,
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
          // 键盘焦点停在这一层（dnd-kit 的 attributes 给了 role="button" + tabIndex=0），
          // 不是里面的 role="tab"。这一层填满会裁剪的标签条，向外的焦点环三边都被切掉，
          // 所以向内画。向内的环落在自己的盒子里，而定位的子元素画在父元素的 outline
          // 之上——带底色的标签页会把环整块盖住。所以标签页沉到 Tab 条那个层叠上下文的
          // 负层（见 Tab 的 -z-10 / -z-[9]），顺序才是「标签页 → 色线 → 环」。
          // relative：项目色线按这一格定位。
          className="relative inline-flex h-full min-w-0 flex-shrink focus-visible:focus-outline-inset"
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
            pillText={tab.pillText}
            onActivate={onActivate}
            onClose={onClose}
            onDoublePromote={onDoublePromote}
          />
          {/* 项目色线画在外壳上、比所有 tab 高一层：贴底、从活动 tab 下面穿过，不被它的
              反向圆角咬出缺口；相邻两格首尾相接成一整段。 */}
          {tab.projectColor ? (
            <span
              data-testid="tab-project-line"
              aria-hidden="true"
              className={cn(
                "pointer-events-none absolute inset-x-0 bottom-0 -z-[8] h-[2px]",
                lineStart && "rounded-l-full",
                lineEnd && "rounded-r-full",
              )}
              style={{
                backgroundColor: tab.projectColor,
                opacity: tab.worktree ? 0.4 : 1,
              }}
            />
          ) : null}
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
