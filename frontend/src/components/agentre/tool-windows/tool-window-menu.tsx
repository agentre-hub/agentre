import type * as React from "react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
} from "@agentre-hub/agentre-ui";
import {
  SLOT_IDS,
  TOOL_WINDOW_MODES,
  useToolWindowLayout,
  type SlotId,
  type ToolWindowId,
  type ToolWindowMode,
} from "@/stores/tool-window-layout-store";

import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { formatFocusedToolWindowShortcut } from "../shortcuts/tool-window-shortcuts";

const MODE_LABEL_KEYS: Record<ToolWindowMode, string> = {
  pinned: "toolWindows.modes.pinned",
  autoHide: "toolWindows.modes.autoHide",
  float: "toolWindows.modes.float",
};

const SLOT_LABEL_KEYS: Record<SlotId, string> = {
  rightTop: "toolWindows.slots.rightTop",
  rightBottom: "toolWindows.slots.rightBottom",
  bottomLeft: "toolWindows.slots.bottomLeft",
  bottomRight: "toolWindows.slots.bottomRight",
};

const isMode = (v: string): v is ToolWindowMode =>
  (TOOL_WINDOW_MODES as readonly string[]).includes(v);
const isSlot = (v: string): v is SlotId =>
  (SLOT_IDS as readonly string[]).includes(v);

type Props = { id: ToolWindowId } & Omit<
  React.ComponentProps<typeof DropdownMenuContent>,
  "children"
>;

/**
 * 工具窗的 ⋯ 菜单（spec「⋯ 菜单」），图标右键弹出的也是这一份：视图模式与移到
 * （当前项打勾）、最大化、隐藏、恢复默认布局。
 */
export function ToolWindowMenuContent({ id, ...contentProps }: Props) {
  const { t } = useTranslation();
  const platform = useOptionalShortcutsContext()?.platform;
  const mode = useToolWindowLayout((s) => s.mode[id]);
  const slot = useToolWindowLayout((s) => s.slotOf[id]);
  const shown = useToolWindowLayout((s) => s.shown[s.slotOf[id]] === id);
  const maximized = useToolWindowLayout((s) => s.maximized === id);
  const setMode = useToolWindowLayout((s) => s.setMode);
  const moveTo = useToolWindowLayout((s) => s.moveTo);
  const hide = useToolWindowLayout((s) => s.hide);
  const toggleMaximize = useToolWindowLayout((s) => s.toggleMaximize);
  const requestFocus = useToolWindowLayout((s) => s.requestFocus);
  const restoreDefaults = useToolWindowLayout((s) => s.restoreDefaults);

  return (
    <DropdownMenuContent className="min-w-52" {...contentProps}>
      <DropdownMenuLabel className="text-2xs font-medium text-muted-foreground">
        {t("toolWindows.menu.viewMode")}
      </DropdownMenuLabel>
      <DropdownMenuRadioGroup
        value={mode}
        onValueChange={(v) => {
          if (!isMode(v)) return;
          setMode(id, v);
          // 换模式可能把它挪到另一处渲染（停靠 ↔ 浮动），焦点跟过去。
          requestFocus(id);
        }}
      >
        {TOOL_WINDOW_MODES.map((m) => (
          <DropdownMenuRadioItem key={m} value={m}>
            {t(MODE_LABEL_KEYS[m])}
          </DropdownMenuRadioItem>
        ))}
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuLabel className="text-2xs font-medium text-muted-foreground">
        {t("toolWindows.menu.moveTo")}
      </DropdownMenuLabel>
      <DropdownMenuRadioGroup
        value={slot}
        onValueChange={(v) => {
          if (isSlot(v)) moveTo(id, v);
        }}
      >
        {SLOT_IDS.map((s) => (
          <DropdownMenuRadioItem key={s} value={s}>
            {t(SLOT_LABEL_KEYS[s])}
          </DropdownMenuRadioItem>
        ))}
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuItem disabled={!shown} onSelect={() => toggleMaximize(id)}>
        {maximized
          ? t("toolWindows.menu.restore")
          : t("toolWindows.menu.maximize")}
        {platform ? (
          <MenuShortcut>
            {formatFocusedToolWindowShortcut("maximize", platform)}
          </MenuShortcut>
        ) : null}
      </DropdownMenuItem>
      <DropdownMenuItem disabled={!shown} onSelect={() => hide(id)}>
        {t("toolWindows.hide")}
        {platform ? (
          <MenuShortcut>
            {formatFocusedToolWindowShortcut("hide", platform)}
          </MenuShortcut>
        ) : null}
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem onSelect={restoreDefaults}>
        {t("toolWindows.menu.restoreDefaults")}
      </DropdownMenuItem>
    </DropdownMenuContent>
  );
}

function MenuShortcut({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="ml-auto pl-4 font-sans text-xs text-muted-foreground">
      {children}
    </kbd>
  );
}
