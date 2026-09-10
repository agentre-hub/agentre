// 面板底部的操作提示行:几组「按键 → 干什么」的成对提示。

import { Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";

import { formatChord } from "../shortcuts/format";
import { CMD_NEW_CHAT_ID } from "../shortcuts/registry";
import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { type PaletteMode } from "./mode";

export function Footer({ mode }: { mode: PaletteMode }) {
  const { t } = useTranslation();
  // 命令模式下 Tab 切上下文的提示放在 ContextBar 右侧（与 chip 同行）；
  // Footer 只承担命令面板通用提示 + 命令模式专属的 ⌫ 清上下文。
  const isCommand = mode === "command";
  // 默认搜索模式右侧展示“新建对话”教学：按键文案读取 cmd.new-chat 当前绑定并按
  // 平台格式化；脱离快捷键 Provider 的兜底场景显示默认 ⌘N（与 ⌘P 触发器“⌘P”
  // 兜底一致，保证提示不显示与用户当前绑定不一致的按键）。
  const shortcuts = useOptionalShortcutsContext();
  const chord = shortcuts?.bindings.get(CMD_NEW_CHAT_ID);
  const newChatShortcut = chord
    ? formatChord(chord, shortcuts!.platform)
    : "⌘N";
  return (
    <div className="flex h-9 shrink-0 items-center gap-3 overflow-hidden border-t border-border bg-muted px-4">
      <FooterHint kbd="↑↓" label={t("commandPalette.footer.navigate")} />
      <FooterHint
        kbd="↵"
        label={
          isCommand
            ? t("commandPalette.footer.create")
            : t("commandPalette.footer.open")
        }
      />
      {isCommand ? (
        <FooterHint kbd="⌫" label={t("commandPalette.footer.clearContext")} />
      ) : null}
      <FooterHint kbd="Esc" label={t("common.close")} />
      <div className="flex-1" />
      {isCommand ? (
        <span className="flex shrink-0 items-center gap-1 text-2xs font-medium text-muted-foreground">
          <Sparkles className="size-[11px] text-primary" aria-hidden="true" />
          {t("commandPalette.search.commandMode")}
        </span>
      ) : (
        <NewChatHint
          kbd={newChatShortcut}
          label={t("commandPalette.footer.newChat")}
        />
      )}
    </div>
  );
}

// 默认搜索模式右侧的“新建对话”教学提示（占用原“✦ 命令面板”状态标识位）。
// 按键胶囊用轻量品牌色强调，但仍是次级信息，不抢列表焦点；纯教学，无点击行为。

export function KbdHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd
        className="inline-flex h-[16px] min-w-[16px] items-center justify-center rounded-sm border border-border bg-secondary px-1 font-mono text-2xs font-medium text-muted-foreground"
        aria-hidden="true"
      >
        {kbd}
      </kbd>
      <span className="text-2xs text-muted-foreground">{label}</span>
    </span>
  );
}

// 默认搜索模式右侧的“新建对话”教学提示（占用原“✦ 命令面板”状态标识位）。
// 按键胶囊用轻量品牌色强调，但仍是次级信息，不抢列表焦点；纯教学，无点击行为。
export function NewChatHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <>
      <span className="h-4 w-px shrink-0 bg-border" aria-hidden="true" />
      <span className="flex shrink-0 items-center gap-1.5">
        <kbd
          className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-sm border border-primary/40 bg-primary-soft px-1.5 font-mono text-2xs font-semibold text-primary-text"
          aria-hidden="true"
        >
          {kbd}
        </kbd>
        <span className="text-2xs font-semibold text-primary-text">
          {label}
        </span>
      </span>
    </>
  );
}

export function FooterHint({ kbd, label }: { kbd: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd
        className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-sm border border-border bg-card px-1.5 font-mono text-2xs font-medium text-muted-foreground"
        aria-hidden="true"
      >
        {kbd}
      </kbd>
      <span className="text-2xs text-muted-foreground">{label}</span>
    </span>
  );
}
