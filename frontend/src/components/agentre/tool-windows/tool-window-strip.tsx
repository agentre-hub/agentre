import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  DropdownMenu,
  DropdownMenuTrigger,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
  cn,
} from "@agentre-hub/agentre-ui";
import {
  TOOL_WINDOW_IDS,
  useToolWindowLayout,
  type SlotId,
  type ToolWindowId,
} from "@/stores/tool-window-layout-store";

import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { formatToolWindowToggle } from "../shortcuts/tool-window-shortcuts";
import { ToolWindowMenuContent } from "./tool-window-menu";
import type { ToolWindowDefinition } from "./types";

type Props = {
  windows: ToolWindowDefinition[];
  /** 拖动图标时指向的目标槽位（另一组）；null = 没在拖或指着原组 / 图标条外。 */
  onDragTargetChange?: (slot: SlotId | null) => void;
};

/** 指针移动超过它才算开始拖，免得点一下手抖就成了拖动。 */
const DRAG_THRESHOLD = 4;

type DragState = { id: ToolWindowId; from: SlotId; over: SlotId | null };

type DragApi = {
  drag: DragState | null;
  /** 在图标上按下主键：可能是点击，也可能是拖动的开始。 */
  begin: (id: ToolWindowId, event: React.PointerEvent) => void;
  /** 刚拖完松手：紧跟着的那次 click 不当作点击。 */
  consumeClick: () => boolean;
};

const DragContext = React.createContext<DragApi | null>(null);

/**
 * 对话页最右边常驻的竖向图标条（spec「图标条」）：按槽位分组——右上一组、右下
 * 一组放在上端，底部两组放在下端。点击图标显示 / 隐藏对应的工具窗；右键弹出与
 * ⋯ 相同的菜单；把图标拖到另一组就把工具窗移到那个槽位并显示（拖回原组或拖出
 * 图标条不变）。同一组里的顺序固定为工具窗的注册顺序（TOOL_WINDOW_IDS），不随
 * 点击跳动。
 */
function ToolWindowStripImpl({ windows, onDragTargetChange }: Props) {
  const { t } = useTranslation();
  const slotOf = useToolWindowLayout((s) => s.slotOf);
  const moveTo = useToolWindowLayout((s) => s.moveTo);
  const stripRef = React.useRef<HTMLDivElement>(null);
  const [drag, setDrag] = React.useState<DragState | null>(null);
  const suppressClickRef = React.useRef(false);
  const onTargetRef = React.useRef(onDragTargetChange);
  React.useEffect(() => {
    onTargetRef.current = onDragTargetChange;
  }, [onDragTargetChange]);

  const begin = React.useCallback(
    (id: ToolWindowId, down: React.PointerEvent) => {
      if (down.button !== 0) return;
      const from = useToolWindowLayout.getState().slotOf[id];
      const startX = down.clientX;
      const startY = down.clientY;
      let active = false;
      let over: SlotId | null = null;

      // 松手 / 移动时指针下面是哪一组：只认这条图标条里的组，原组不算落点。
      const groupAt = (ev: PointerEvent): SlotId | null => {
        const target = ev.target instanceof Element ? ev.target : null;
        const group = target?.closest<HTMLElement>("[data-tool-window-group]");
        if (!group || !stripRef.current?.contains(group)) return null;
        const slot = group.dataset.toolWindowGroup as SlotId;
        return slot === from ? null : slot;
      };
      const setOver = (next: SlotId | null) => {
        if (next === over) return;
        over = next;
        setDrag({ id, from, over });
        onTargetRef.current?.(over);
      };
      const onMove = (ev: PointerEvent) => {
        if (!active) {
          if (
            Math.hypot(ev.clientX - startX, ev.clientY - startY) <
            DRAG_THRESHOLD
          ) {
            return;
          }
          active = true;
          document.body.style.userSelect = "none";
          setDrag({ id, from, over: null });
        }
        setOver(groupAt(ev));
      };
      const finish = (ev: PointerEvent | null) => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onCancel);
        if (!active) return;
        document.body.style.userSelect = "";
        const target = ev === null ? null : groupAt(ev);
        setDrag(null);
        onTargetRef.current?.(null);
        // 松手后浏览器可能紧跟一次 click：它不是点击。
        suppressClickRef.current = true;
        window.setTimeout(() => {
          suppressClickRef.current = false;
        }, 0);
        if (target !== null) moveTo(id, target);
      };
      const onUp = (ev: PointerEvent) => finish(ev);
      const onCancel = () => finish(null);
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
      document.addEventListener("pointercancel", onCancel);
    },
    [moveTo],
  );

  const consumeClick = React.useCallback(() => {
    if (!suppressClickRef.current) return false;
    suppressClickRef.current = false;
    return true;
  }, []);

  const dragApi = React.useMemo<DragApi>(
    () => ({ drag, begin, consumeClick }),
    [drag, begin, consumeClick],
  );

  const groupOf = (slot: SlotId) =>
    TOOL_WINDOW_IDS.filter((id) => slotOf[id] === slot)
      .map((id) => windows.find((w) => w.id === id))
      .filter((w): w is ToolWindowDefinition => w !== undefined);

  // 拖动中四组都出现（空组也是落点）；平时只显示有图标的组。
  const groups = (slots: SlotId[]) =>
    slots
      .map((slot) => ({
        slot,
        label: t(SLOT_LABEL_KEYS[slot]),
        items: groupOf(slot),
      }))
      .filter((g) => drag !== null || g.items.length > 0);
  const top = groups(["rightTop", "rightBottom"]);
  const bottom = groups(["bottomLeft", "bottomRight"]);

  // 自带 Provider：图标条不依赖宿主在外面包一层才能出悬停提示。
  return (
    <DragContext.Provider value={dragApi}>
      <TooltipProvider delayDuration={300}>
        <div
          ref={stripRef}
          role="toolbar"
          aria-orientation="vertical"
          aria-label={t("toolWindows.strip")}
          data-testid="tool-window-strip"
          onKeyDown={handleStripKeyDown}
          className="flex w-[38px] shrink-0 flex-col items-center gap-1 pt-1 pb-2"
        >
          {top.map((g, i) => (
            <React.Fragment key={g.slot}>
              {i > 0 ? <GroupGap /> : null}
              <StripGroup slot={g.slot} label={g.label} items={g.items} />
            </React.Fragment>
          ))}
          <div className="flex-1" />
          {bottom.map((g, i) => (
            <React.Fragment key={g.slot}>
              {i > 0 ? <GroupGap /> : null}
              <StripGroup slot={g.slot} label={g.label} items={g.items} />
            </React.Fragment>
          ))}
        </div>
      </TooltipProvider>
    </DragContext.Provider>
  );
}

export const ToolWindowStrip = React.memo(ToolWindowStripImpl);

const SLOT_LABEL_KEYS: Record<SlotId, string> = {
  rightTop: "toolWindows.slots.rightTop",
  rightBottom: "toolWindows.slots.rightBottom",
  bottomLeft: "toolWindows.slots.bottomLeft",
  bottomRight: "toolWindows.slots.bottomRight",
};

/** 组与组之间的分隔：一小段空白加一枚短点，不画贯穿的结构线（design.md §3.4）。 */
function GroupGap() {
  return (
    <span
      aria-hidden="true"
      className="my-1 h-px w-4 shrink-0 rounded-full bg-muted-foreground/30"
    />
  );
}

function StripGroup({
  slot,
  label,
  items,
}: {
  slot: SlotId;
  label: string;
  items: ToolWindowDefinition[];
}) {
  const drag = React.useContext(DragContext)?.drag ?? null;
  const isTarget = drag?.over === slot;
  return (
    <div
      role="group"
      aria-label={label}
      data-tool-window-group={slot}
      className={cn(
        "flex flex-col items-center gap-1",
        // 拖动中空组也要能接住指针。
        drag !== null && "min-h-[30px] min-w-[30px]",
      )}
    >
      {items.map((w) => (
        <StripButton key={w.id} window={w} />
      ))}
      {isTarget ? (
        <span
          aria-hidden="true"
          data-testid="tool-window-drop-marker"
          className="size-[30px] shrink-0 rounded-[7px] border-2 border-dashed border-primary/70 bg-primary/10"
        />
      ) : null}
    </div>
  );
}

function StripButton({ window: w }: { window: ToolWindowDefinition }) {
  const { t } = useTranslation();
  const shown = useToolWindowLayout((s) => s.shown[s.slotOf[w.id]] === w.id);
  const toggle = useToolWindowLayout((s) => s.toggle);
  const dragApi = React.useContext(DragContext);
  const platform = useOptionalShortcutsContext()?.platform;
  const [menuOpen, setMenuOpen] = React.useState(false);
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const buttonId = React.useId();
  const badgeId = React.useId();
  const Icon = w.icon;
  const badge =
    w.countBadge !== false && w.count != null && w.count > 0 ? w.count : null;
  const runningId = React.useId();
  const running = w.running === true;
  const alertId = React.useId();
  const alerting = w.alert === true;
  const describedBy =
    [
      badge !== null ? badgeId : null,
      running ? runningId : null,
      alerting ? alertId : null,
    ]
      .filter(Boolean)
      .join(" ") || undefined;
  const dragging = dragApi?.drag?.id === w.id;

  return (
    <div className="relative">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            ref={buttonRef}
            id={buttonId}
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={w.label}
            aria-pressed={shown}
            aria-describedby={describedBy}
            data-tool-window-button={w.id}
            onPointerDown={(e) => dragApi?.begin(w.id, e)}
            onClick={() => {
              if (dragApi?.consumeClick()) return;
              toggle(w.id);
            }}
            onContextMenu={(e) => {
              e.preventDefault();
              setMenuOpen(true);
            }}
            className={cn(
              "relative size-[30px] rounded-[7px] text-sidebar-icon",
              shown
                ? "bg-button-fill text-sidebar-icon-active hover:bg-button-fill-hover hover:text-sidebar-icon-active"
                : "hover:bg-rail-accent",
              dragging && "opacity-50",
            )}
          >
            <Icon className="size-4" aria-hidden="true" />
            {badge !== null ? (
              <span
                id={badgeId}
                data-testid="tool-window-badge"
                className="absolute -top-px -right-[3px] h-3.5 min-w-3.5 rounded-full bg-primary px-[3px] text-center text-3xs/3.5 font-semibold text-primary-foreground tabular-nums"
              >
                {badge}
              </span>
            ) : null}
            {running ? (
              <>
                <span
                  aria-hidden="true"
                  data-testid="tool-window-running-dot"
                  className="absolute top-0.5 right-0.5 size-2 rounded-full bg-status-running"
                />
                <span id={runningId} className="sr-only">
                  {t("toolWindows.running")}
                </span>
              </>
            ) : null}
            {alerting ? (
              <>
                <span
                  aria-hidden="true"
                  data-testid="tool-window-alert-dot"
                  className="absolute top-0.5 right-0.5 size-2 rounded-full bg-status-waiting"
                />
                <span id={alertId} className="sr-only">
                  {t("toolWindows.alert")}
                </span>
              </>
            ) : null}
          </Button>
        </TooltipTrigger>
        <TooltipContent side="left">
          {w.label}
          {platform ? (
            <kbd className="ml-2 font-sans opacity-70">
              {formatToolWindowToggle(w.id, platform)}
            </kbd>
          ) : null}
        </TooltipContent>
      </Tooltip>
      {/* 右键菜单：与工具窗 ⋯ 同一份，锚在图标上。锚点不接指针、不进 Tab 顺序，
          左键点击仍归图标自己（显示 / 隐藏）。 */}
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-0"
          />
        </DropdownMenuTrigger>
        <ToolWindowMenuContent
          id={w.id}
          side="left"
          align="start"
          aria-labelledby={buttonId}
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            // 菜单项把焦点交给了工具窗时不抢回来；否则回到图标。
            const active = document.activeElement;
            if (active === null || active === document.body) {
              buttonRef.current?.focus();
            }
          }}
        />
      </DropdownMenu>
    </div>
  );
}

/** 工具栏内 ↑/↓ 在图标之间移动焦点（Home / End 到两端）。 */
function handleStripKeyDown(event: React.KeyboardEvent<HTMLElement>) {
  const buttons = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      "[data-tool-window-button]",
    ),
  );
  if (buttons.length === 0) return;
  const current = buttons.indexOf(document.activeElement as HTMLElement);
  let next: number;
  switch (event.key) {
    case "ArrowDown":
      next = current < 0 ? 0 : (current + 1) % buttons.length;
      break;
    case "ArrowUp":
      next =
        current < 0
          ? buttons.length - 1
          : (current - 1 + buttons.length) % buttons.length;
      break;
    case "Home":
      next = 0;
      break;
    case "End":
      next = buttons.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  buttons[next].focus();
}
