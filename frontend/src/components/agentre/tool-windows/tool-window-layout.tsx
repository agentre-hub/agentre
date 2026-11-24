import * as React from "react";
import { useTranslation } from "react-i18next";

import { cn } from "@agentre-hub/agentre-ui";
import {
  SLOT_IDS,
  resolvePresentation,
  useToolWindowLayout,
  type SlotId,
  type ToolWindowId,
} from "@/stores/tool-window-layout-store";

import { ToolWindowLayoutContext } from "./layout-context";
import { ToolWindowDivider } from "./tool-window-divider";
import {
  ToolWindowFrame,
  type ToolWindowPresentation,
} from "./tool-window-frame";
import { ToolWindowStrip } from "./tool-window-strip";
import type { ToolWindowDefinition } from "./types";

/** 对话页可用宽度低于它时，右侧停靠的工具窗改为盖在对话上方（spec「窄窗口」）。 */
export const NARROW_LAYOUT_WIDTH = 1200;

type Props = {
  /** 已注册的工具窗；没注册的工具窗不出图标，它所在的槽位按空槽处理。 */
  windows: ToolWindowDefinition[];
  /**
   * 新建会话提示页（尚未发出首条消息）不显示图标条与工具窗（spec「新建会话
   * 提示页」）。隐藏时编辑区仍渲染在同一个位置，里面的输入框不会因此重新挂载。
   */
  hidden?: boolean;
  /** 编辑区：对话内容页（以及暂时还在它右边的文件预览列）。 */
  children: React.ReactNode;
};

type Placed = {
  window: ToolWindowDefinition;
  slot: SlotId;
  presentation: ToolWindowPresentation;
  autoHide: boolean;
};

/**
 * 对话页的工具窗骨架（spec「工具窗布局」）：编辑区在左，下方是底部左 / 底部右
 * 两个槽位；右侧是右上 / 右下两个槽位，最右边是常驻图标条。每个槽位同一时刻
 * 最多显示一个工具窗；同侧两个槽位都有工具窗时分屏（右侧上下分、底部左右分），
 * 四条分隔条都可拖。浮动窗、窄窗口下的右侧覆盖层、最大化的工具窗都盖在工具窗
 * 区域上方，不占槽位。
 *
 * 编辑区永远是这棵树里的同一个位置——槽位、覆盖层与图标条只作为它的兄弟出现或
 * 消失。
 */
export function ToolWindowLayout({ windows, hidden = false, children }: Props) {
  const { t } = useTranslation();
  const rootRef = React.useRef<HTMLDivElement>(null);
  const workRef = React.useRef<HTMLDivElement>(null);
  const editorColumnRef = React.useRef<HTMLDivElement>(null);
  const editorRef = React.useRef<HTMLDivElement>(null);
  const width = useElementWidth(rootRef);
  // 还没量到宽度（0）时按宽窗口处理。
  const narrow = width > 0 && width < NARROW_LAYOUT_WIDTH;
  const [dragTarget, setDragTarget] = React.useState<SlotId | null>(null);
  const placed = usePlacedWindows(windows, narrow);

  useAutoHide(rootRef, editorRef, narrow, !hidden);
  useNarrowOverlayFocus(
    placed,
    width === 0 ? "unmeasured" : narrow ? "narrow" : "wide",
    hidden,
  );

  const context = React.useMemo(
    () => ({
      focusEditor: () => {
        // 输入框是编辑区里可编辑的文本框；编辑消息时转录里还会多出一个，主输入框
        // 在最下面，取最后一个。
        const boxes = editorRef.current?.querySelectorAll<HTMLElement>(
          '[contenteditable="true"][role="textbox"]',
        );
        boxes?.[boxes.length - 1]?.focus();
      },
    }),
    [],
  );

  const at = (slot: SlotId, presentation: ToolWindowPresentation) =>
    placed.find((p) => p.slot === slot && p.presentation === presentation) ??
    null;
  const floats = placed.filter((p) => p.presentation === "float");
  const overlay = placed.filter((p) => p.presentation === "overlay");
  const maximized = placed.find((p) => p.presentation === "maximized") ?? null;

  // 一行里：编辑区列（下方是底部槽位）、右侧槽位、最右的图标条。浮动窗、窄窗口
  // 覆盖层、最大化与拖动高亮画在覆盖层里——它盖住图标条以外的整块工具窗区域，
  // 自己不接指针。
  return (
    <ToolWindowLayoutContext.Provider value={context}>
      <div ref={rootRef} className="relative flex min-h-0 min-w-0 flex-1">
        <div
          ref={editorColumnRef}
          className="flex min-h-0 min-w-0 flex-1 flex-col"
        >
          <div ref={editorRef} className="flex min-h-0 min-w-0 flex-1">
            {children}
          </div>
          {hidden ? null : (
            <SplitArea
              kind="bottom"
              label={t("toolWindows.bottomArea")}
              first={at("bottomLeft", "docked")}
              second={at("bottomRight", "docked")}
              outerRef={editorColumnRef}
            />
          )}
        </div>
        {hidden ? null : (
          <SplitArea
            kind="right"
            label={t("toolWindows.rightArea")}
            first={at("rightTop", "docked")}
            second={at("rightBottom", "docked")}
            outerRef={workRef}
          />
        )}
        {hidden ? null : (
          <div
            ref={workRef}
            data-testid="tool-window-overlay-layer"
            className="pointer-events-none absolute inset-y-0 left-0 right-[38px] z-30"
          >
            {overlay.length === 0 ? null : (
              <aside
                aria-label={t("toolWindows.rightArea")}
                className="absolute inset-y-0 right-0 flex w-[min(360px,50%)] flex-col gap-2 p-2"
              >
                {overlay.map((p) => (
                  <ToolWindowFrame
                    key={p.window.id}
                    window={p.window}
                    presentation="overlay"
                    autoHide={p.autoHide}
                  />
                ))}
              </aside>
            )}
            {floats.map((p) => (
              <ToolWindowFrame
                key={p.window.id}
                window={p.window}
                presentation="float"
              />
            ))}
            {maximized === null ? null : (
              <div className="absolute inset-0 flex pb-2">
                <ToolWindowFrame
                  key={maximized.window.id}
                  window={maximized.window}
                  presentation="maximized"
                  autoHide={maximized.autoHide}
                  className="shadow-overlay"
                />
              </div>
            )}
            {dragTarget === null ? null : <SlotHighlight slot={dragTarget} />}
          </div>
        )}
        {hidden ? null : (
          <ToolWindowStrip
            windows={windows}
            onDragTargetChange={setDragTarget}
          />
        )}
      </div>
    </ToolWindowLayoutContext.Provider>
  );
}

/**
 * 每个槽位此刻显示的、已注册的工具窗放在哪儿画。显示的是没注册的工具窗时按空槽
 * 处理。
 */
function usePlacedWindows(
  windows: ToolWindowDefinition[],
  narrow: boolean,
): Placed[] {
  const shown = useToolWindowLayout((s) => s.shown);
  const mode = useToolWindowLayout((s) => s.mode);
  const maximized = useToolWindowLayout((s) => s.maximized);
  const narrowCollapsed = useToolWindowLayout((s) => s.narrowCollapsed);
  return React.useMemo(() => {
    const out: Placed[] = [];
    for (const slot of SLOT_IDS) {
      const id = shown[slot];
      const def = id === null ? undefined : windows.find((w) => w.id === id);
      if (def === undefined) continue;
      const { floating, autoHide } = resolvePresentation(
        mode[def.id],
        slot,
        narrow,
      );
      const presentation: ToolWindowPresentation =
        maximized === def.id
          ? "maximized"
          : !floating
            ? "docked"
            : mode[def.id] === "float"
              ? "float"
              : "overlay";
      // 窄窗口下收起的工具窗在保存的布局里仍然显示，只是此刻不画。
      if (presentation === "overlay" && narrowCollapsed[def.id] === true) {
        continue;
      }
      out.push({ window: def, slot, presentation, autoHide });
    }
    return out;
  }, [windows, narrow, shown, mode, maximized, narrowCollapsed]);
}

/**
 * 窄窗口下盖在对话上方的工具窗只在焦点在它身上时出现（spec「窄窗口」：停靠的
 * 工具窗改为「浮动 + 失焦自动收起」）：
 * - 进入窄窗口、或工具窗区域刚出现（启动、新建会话发出首条消息）时，焦点不在
 *   它身上的收起；
 * - 之后新盖上来的（例如从转录里的链接打开）拿焦点；
 * - 窗口恢复宽度后收起的全部回到停靠并显示。
 * 收起只记在瞬时状态里，保存的显示状态与模式不变。各标签页各有一份布局、共用
 * 同一份 store，「焦点在它身上」按整个文档里同名的工具窗判断。
 */
function useNarrowOverlayFocus(
  placed: Placed[],
  width: "unmeasured" | "narrow" | "wide",
  hidden: boolean,
) {
  const overlayKey = placed
    .filter((p) => p.presentation === "overlay")
    .map((p) => p.window.id)
    .join(" ");
  const seenRef = React.useRef<Set<string> | null>(null);
  React.useEffect(() => {
    if (width === "wide") {
      seenRef.current = null;
      useToolWindowLayout.getState().expandNarrow();
      return;
    }
    if (width === "unmeasured" || hidden) {
      seenRef.current = null;
      return;
    }
    const ids =
      overlayKey === "" ? [] : (overlayKey.split(" ") as ToolWindowId[]);
    const seen = seenRef.current;
    seenRef.current = new Set(ids);
    const s = useToolWindowLayout.getState();
    for (const id of ids) {
      if (s.focusRequest === id || holdsFocus(id)) continue;
      if (seen === null) s.collapseNarrow(id);
      else if (!seen.has(id)) s.requestFocus(id);
    }
  }, [overlayKey, width, hidden]);
}

function holdsFocus(id: ToolWindowId): boolean {
  const active = document.activeElement;
  return (
    active instanceof Element &&
    active.closest(`[data-tool-window="${id}"]`) !== null
  );
}

/**
 * 同侧两个槽位：右侧（上下分）或底部（左右分），与编辑区之间、两个槽位之间各有
 * 一条可拖的分隔条。
 */
function SplitArea({
  kind,
  label,
  first,
  second,
  outerRef,
}: {
  kind: "right" | "bottom";
  label: string;
  first: Placed | null;
  second: Placed | null;
  /** 外层分隔条（编辑区 | 右侧、编辑区 / 底部）比例相对的容器。 */
  outerRef: React.RefObject<HTMLDivElement | null>;
}) {
  const ratio = useToolWindowLayout((s) => s.ratios[kind]);
  const split = useToolWindowLayout(
    (s) => s.ratios[kind === "right" ? "rightSplit" : "bottomSplit"],
  );
  const innerRef = React.useRef<HTMLElement>(null);
  if (first === null && second === null) return null;
  const right = kind === "right";
  const both = first !== null && second !== null;
  return (
    <aside
      ref={innerRef}
      aria-label={label}
      style={{ flexBasis: `${ratio * 100}%` }}
      className={cn(
        "relative flex min-h-0 min-w-0 shrink-0 grow-0 bg-chrome pb-2",
        right ? "min-w-48 flex-col" : "min-h-24 pr-2",
      )}
    >
      {/* 与编辑区之间的分隔条压在边缘上，不额外占位。 */}
      <ToolWindowDivider
        id={kind}
        containerRef={outerRef}
        className={
          right ? "absolute! inset-y-0 -left-1" : "absolute! inset-x-0 -top-1"
        }
      />
      {first !== null ? (
        <div
          style={both ? { flexBasis: `${split * 100}%` } : undefined}
          className={cn(
            "flex min-h-0 min-w-0",
            both ? "shrink-0 grow-0" : "flex-1",
          )}
        >
          <ToolWindowFrame
            key={first.window.id}
            window={first.window}
            autoHide={first.autoHide}
          />
        </div>
      ) : null}
      {both ? (
        <ToolWindowDivider
          id={right ? "rightSplit" : "bottomSplit"}
          containerRef={innerRef}
        />
      ) : null}
      {second !== null ? (
        <div className="flex min-h-0 min-w-0 flex-1">
          <ToolWindowFrame
            key={second.window.id}
            window={second.window}
            autoHide={second.autoHide}
          />
        </div>
      ) : null}
    </aside>
  );
}

const SLOT_LABEL_KEYS: Record<SlotId, string> = {
  rightTop: "toolWindows.slots.rightTop",
  rightBottom: "toolWindows.slots.rightBottom",
  bottomLeft: "toolWindows.slots.bottomLeft",
  bottomRight: "toolWindows.slots.bottomRight",
};

/** 拖动图标换槽时，目标槽位整块高亮并标出槽位名（spec「拖动换槽」）。 */
function SlotHighlight({ slot }: { slot: SlotId }) {
  const { t } = useTranslation();
  const ratios = useToolWindowLayout((s) => s.ratios);
  const right = ratios.right * 100;
  const editor = 100 - right;
  const geometry: Record<SlotId, React.CSSProperties> = {
    rightTop: {
      top: 0,
      right: 0,
      width: `${right}%`,
      height: `${ratios.rightSplit * 100}%`,
    },
    rightBottom: {
      bottom: 0,
      right: 0,
      width: `${right}%`,
      height: `${(1 - ratios.rightSplit) * 100}%`,
    },
    bottomLeft: {
      bottom: 0,
      left: 0,
      width: `${editor * ratios.bottomSplit}%`,
      height: `${ratios.bottom * 100}%`,
    },
    bottomRight: {
      bottom: 0,
      left: `${editor * ratios.bottomSplit}%`,
      width: `${editor * (1 - ratios.bottomSplit)}%`,
      height: `${ratios.bottom * 100}%`,
    },
  };
  return (
    <div
      data-testid="tool-window-slot-highlight"
      style={geometry[slot]}
      className="pointer-events-none absolute z-50 flex items-center justify-center rounded-[10px] bg-primary/10 p-2 ring-2 ring-primary/60 ring-inset"
    >
      <span className="rounded-md bg-popover px-2 py-1 text-xs font-semibold text-primary-text shadow-overlay">
        {t("toolWindows.dropTo", { slot: t(SLOT_LABEL_KEYS[slot]) })}
      </span>
    </div>
  );
}

/** 元素当前的宽度，随尺寸变化更新；还没布局（或在隐藏的标签页里）时为 0。 */
function useElementWidth(ref: React.RefObject<HTMLElement | null>): number {
  const [width, setWidth] = React.useState(0);
  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setWidth(el.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", measure);
      return () => window.removeEventListener("resize", measure);
    }
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref]);
  return width;
}

/**
 * 「失焦自动收起」（spec「视图模式」）：焦点或指针落到编辑区或另一个工具窗时，
 * 收起其余处于自动收起的工具窗。落在图标条、分隔条或弹出菜单（不在这棵树里）
 * 上不算离开。每个标签页各有一份布局，只认落在自己这棵树里的事件。
 */
function useAutoHide(
  rootRef: React.RefObject<HTMLElement | null>,
  editorRef: React.RefObject<HTMLElement | null>,
  narrow: boolean,
  enabled: boolean,
) {
  const narrowRef = React.useRef(narrow);
  React.useEffect(() => {
    narrowRef.current = narrow;
  }, [narrow]);

  React.useEffect(() => {
    if (!enabled) return;
    const onEnter = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (!rootRef.current?.contains(target)) return;
      const el = target instanceof Element ? target : target.parentElement;
      const frame = el?.closest<HTMLElement>("[data-tool-window]") ?? null;
      const inEditor = editorRef.current?.contains(target) ?? false;
      if (frame === null && !inEditor) return;
      const into = frame?.dataset.toolWindow ?? null;
      const s = useToolWindowLayout.getState();
      for (const slot of SLOT_IDS) {
        const id = s.shown[slot];
        if (id === null || id === into) continue;
        if (
          !resolvePresentation(s.mode[id], slot, narrowRef.current).autoHide
        ) {
          continue;
        }
        // 只因窗口窄才自动收起的停靠工具窗只是收起，保存的显示状态不变，窗口
        // 恢复宽度后它照用户原来的样子停靠显示。
        if (s.mode[id] === "autoHide") s.hide(id);
        else s.collapseNarrow(id);
      }
    };
    document.addEventListener("focusin", onEnter);
    document.addEventListener("pointerdown", onEnter, true);
    return () => {
      document.removeEventListener("focusin", onEnter);
      document.removeEventListener("pointerdown", onEnter, true);
    };
  }, [enabled, rootRef, editorRef]);
}
