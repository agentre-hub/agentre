// frontend/src/components/agentre/chat-tabs/tab-strip.tsx
import * as React from "react";
import { createPortal } from "react-dom";
import { Pin, PinOff, X, XCircle, ArrowRightFromLine } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  defaultDropAnimationSideEffects,
  useDndContext,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DropAnimation,
  type Modifier,
} from "@dnd-kit/core";
import {
  SortableContext,
  horizontalListSortingStrategy,
  sortableKeyboardCoordinates,
  useSortable,
} from "@dnd-kit/sortable";
type DndTransform = { x: number; y: number; scaleX: number; scaleY: number };

// 外壳只平移。dnd-kit 的 transform 可能带 scaleX（拖拽源不走浮层时是「落点 Tab 宽 /
// 自身宽」）——Tab 宽窄不一，拖过一格就被压扁或拉宽，绕中心缩放还让它一顿一顿地往回跳。
function translateOnly(transform: DndTransform | null): string | undefined {
  if (!transform) return undefined;
  return `translate3d(${transform.x}px, ${transform.y}px, 0)`;
}

// Tab 只沿 Tab 条横向排序。不锁纵向时被拖的 Tab 跟着指针上下飘、离开 Tab 条；拖拽源
// 一出界，Tab 条的内容高度跟着变大，dnd-kit 的自动滚动随即把整条 Tab 纵向滚走
// （overflow-y-hidden 挡不住程序化滚动）。
const alongTabStrip: Modifier = ({ transform }) => ({ ...transform, y: 0 });
const tabStripModifiers = [alongTabStrip];

// 松手时浮着的拷贝滑进落点（原位置的空位此时已经挪到那里）。滑完之前空位一直藏着，
// 拷贝落定才换回真身。
const tabDropAnimation: DropAnimation = {
  duration: 200,
  easing: "ease-out",
  sideEffects: defaultDropAnimationSideEffects({
    styles: { active: { opacity: "0" } },
  }),
};

function prefersReducedMotion(): boolean {
  return (
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false
  );
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

import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { Tab } from "./tab";
import { TabOverflowMenu } from "./tab-overflow-menu";
import { TabTooltip, createHoverCardGate } from "./tab-tooltip";
import { useHiddenTabIds } from "./use-hidden-tab-ids";
import { useTabsView, type TabView } from "./use-tabs-view";

export function TabStrip({
  hiddenTabIds: hiddenTabIdsOverride,
}: {
  hiddenTabIds?: ReadonlySet<string>;
} = {}) {
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

  // 溢出按钮要知道哪些 tab 被挤出可视区。happy-dom 没有 IntersectionObserver，
  // 测试经 hiddenTabIds 注入；生产不传，走观察器。
  const scrollerRef = React.useRef<HTMLDivElement>(null);
  const observedHidden = useHiddenTabIds(
    scrollerRef,
    tabs.map((t) => t.id),
  );
  const hiddenTabIds = hiddenTabIdsOverride ?? observedHidden;

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  // 拖拽期间压住悬停卡：指针一路扫过别的 Tab，卡片会一张张弹出来跟着跑。
  const [hoverCards] = React.useState(createHoverCardGate);

  function onDragEnd(e: DragEndEvent) {
    hoverCards.setClosed(false);
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
      <div
        ref={scrollerRef}
        className="scrollbar-none isolate flex h-full min-h-0 min-w-0 flex-1 items-center overflow-x-auto overflow-y-hidden pl-5 pr-2"
      >
        {/* 悬停卡的延迟由这一个 Provider 统一管：首次停 400ms，卡片开过之后 0.5s 内滑到
            相邻 Tab 立刻换卡，不再每格重新等。 */}
        <TooltipProvider delayDuration={400} skipDelayDuration={500}>
          <DndContext
            sensors={sensors}
            modifiers={tabStripModifiers}
            onDragStart={() => hoverCards.setClosed(true)}
            onDragEnd={onDragEnd}
            onDragCancel={() => hoverCards.setClosed(false)}
          >
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
                    gate={hoverCards}
                  >
                    <SortableTab
                      data-tab-id={t.id}
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
            <TabDragOverlay tabs={sortedTabs} activeTabId={activeTabId} />
          </DndContext>
        </TooltipProvider>
      </div>
      {sortedTabs.length > 0 ? (
        <div className="flex h-full items-center gap-1 px-1">
          <TabOverflowMenu hiddenTabIds={hiddenTabIds} />
        </div>
      ) : null}
    </div>
  );
}

type TabBodyProps = {
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
};

type SortableTabProps = TabBodyProps &
  Omit<React.HTMLAttributes<HTMLSpanElement>, "children">;

/**
 * 可排序的外壳：只管 dnd-kit 给的位移与拖拽监听。拖拽中每一帧 dnd-kit 都让所有外壳
 * 重渲染，所以外壳里面那一大块（Tab 本体 + 右键菜单）是 memo 的 TabBody——拖拽期间
 * 它的 props 不变，整块跳过。
 */
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
    const {
      attributes,
      listeners,
      setNodeRef,
      transform,
      transition,
      isDragging,
    } = useSortable({ id: tab.id });

    const style: React.CSSProperties = {
      transform: translateOnly(transform),
      transition,
      // 拖着的是浮层上那份拷贝（TabDragOverlay）；原位置只是一个随落点挪动的空位。
      opacity: isDragging ? 0 : undefined,
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
      <span
        ref={setRef}
        // 键盘焦点停在这一层（dnd-kit 的 attributes 给了 role="button" + tabIndex=0），
        // 不是里面的 role="tab"。这一层填满会裁剪的标签条，向外的焦点环三边都被切掉，
        // 所以向内画。向内的环落在自己的盒子里，而定位的子元素画在父元素的 outline
        // 之上——带底色的标签页会把环整块盖住。所以标签页沉到 Tab 条那个层叠上下文的
        // 负层（见 Tab 的 -z-10 / -z-[9]），顺序才是「标签页 → 色线 → 环」。
        // relative：项目色线按这一格定位。
        // pointer-events-none：标签页沉在负层后，这一层的盒子在命中测试里压在它上面，
        // 点击会全落在这一层、到不了标签页（切不了 tab、× 点不到）。这一层不接指针、
        // 标签页接（Tab 的 pointer-events-auto），事件从标签页冒泡上来，拖拽与悬停卡
        // 照样挂在这一层；右键菜单挂在标签页本身。
        className="pointer-events-none relative inline-flex h-full min-w-0 flex-shrink focus-visible:focus-outline-inset"
        style={style}
        {...attributes}
        {...rest}
        {...listeners}
      >
        <TabBody
          tab={tab}
          active={active}
          isLast={isLast}
          lineStart={lineStart}
          lineEnd={lineEnd}
          onActivate={onActivate}
          onClose={onClose}
          onDoublePromote={onDoublePromote}
          onTogglePin={onTogglePin}
          onCloseOthers={onCloseOthers}
          onCloseToRight={onCloseToRight}
        />
      </span>
    );
  },
);

const TabBody = React.memo(function TabBody({
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
}: TabBodyProps) {
  const { t } = useTranslation();
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
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
      </ContextMenuTrigger>
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
});

/**
 * 拖拽中浮着的那份拷贝：跟着指针横向走，压在所有 Tab 之上（浮层那一级，挂到 body 下，
 * 不被 Tab 条裁掉）；松手时滑进落点。只是看的，读屏不该读到第二个同名 Tab。
 */
function TabDragOverlay({
  tabs,
  activeTabId,
}: {
  tabs: TabView[];
  activeTabId: string | null;
}) {
  // 拖拽中每一帧都重渲染这里；拷贝本身是 memo 的，只在拿起时渲染一次。
  const { active } = useDndContext();
  const dragged = active
    ? tabs.find((t) => t.id === String(active.id))
    : undefined;
  return createPortal(
    <DragOverlay
      zIndex={50}
      dropAnimation={prefersReducedMotion() ? null : tabDropAnimation}
    >
      {dragged ? (
        <TabDragPreview tab={dragged} active={dragged.id === activeTabId} />
      ) : null}
    </DragOverlay>,
    document.body,
  );
}

const noop = () => {};

const TabDragPreview = React.memo(function TabDragPreview({
  tab,
  active,
}: {
  tab: TabView;
  active: boolean;
}) {
  return (
    <div
      data-testid="tab-drag-preview"
      aria-hidden="true"
      // isolate：Tab 沉在负层，要沉在这一份自己的层叠上下文里。非活动 Tab 本身没有
      // 底色，浮着压在别的 Tab 上会和它们的字叠成一团——铺上悬停底，像被拿在手里。
      className={cn(
        "pointer-events-none relative isolate flex h-full w-full",
        !active && "rounded-t-xl bg-tab-hover",
      )}
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
        onActivate={noop}
        onClose={noop}
        onDoublePromote={noop}
      />
      {tab.projectColor ? (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 bottom-0 -z-[8] h-[2px] rounded-full"
          style={{
            backgroundColor: tab.projectColor,
            opacity: tab.worktree ? 0.4 : 1,
          }}
        />
      ) : null}
    </div>
  );
});
