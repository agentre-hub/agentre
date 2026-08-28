/**
 * 编辑器上方那三个候选弹层：`!` 本地命令历史 / `/` 斜杠命令 / `@` 提及。
 *
 * 三者都锚在同一个编辑器 DOM 上，且各有各的「不启用就整块不渲染」的判据——
 * 可选口是能力探测，不是弹一个空列表。渲染与否的判断留在调用方（它才知道
 * 宿主接没接），这里只负责把已经决定要出现的那几个摆出来。
 */
import { LocalCommandHistoryPopover } from "./local-command-history/history-popover";
import type { useLocalCommandHistoryMenu } from "./local-command-history/use-local-command-history-menu";
import { SlashPopover } from "./slash/slash-popover";
import type { useSlashMenu } from "./slash/use-slash-menu";
import { MentionPopover, type useMentionMenu } from "./mentions";

export interface ComposerPopoversProps {
  editorElement: HTMLElement | null;
  /** 宿主没有历史能力时传 false：整块不渲染。 */
  commandHistoryEnabled: boolean;
  commandHistoryListboxId: string;
  commandHistoryMenu: ReturnType<typeof useLocalCommandHistoryMenu>;
  slashEnabled: boolean;
  slashMenu: ReturnType<typeof useSlashMenu>;
  mentionEnabled: boolean;
  mentionMenu: ReturnType<typeof useMentionMenu>;
}

export function ComposerPopovers({
  editorElement,
  commandHistoryEnabled,
  commandHistoryListboxId,
  commandHistoryMenu,
  slashEnabled,
  slashMenu,
  mentionEnabled,
  mentionMenu,
}: ComposerPopoversProps) {
  return (
    <>
      {commandHistoryEnabled ? (
        <LocalCommandHistoryPopover
          state={commandHistoryMenu.state}
          listboxId={commandHistoryListboxId}
          onPick={commandHistoryMenu.pick}
          onHover={commandHistoryMenu.setSelectedIndex}
          clearButtonRef={commandHistoryMenu.clearButtonRef}
          onClear={commandHistoryMenu.clear}
          onClearFocus={commandHistoryMenu.onClearFocus}
          onClearBlur={commandHistoryMenu.onClearBlur}
          onClearKeyDown={commandHistoryMenu.onClearKeyDown}
          onDismiss={commandHistoryMenu.dismissCurrent}
          editorElement={editorElement}
        />
      ) : null}
      {slashEnabled ? (
        <SlashPopover
          state={slashMenu.state}
          onPick={slashMenu.pick}
          onHover={slashMenu.setSelectedIndex}
          onDismiss={slashMenu.close}
          editorElement={editorElement}
        />
      ) : null}
      {mentionEnabled ? (
        <MentionPopover
          state={mentionMenu.state}
          onPick={mentionMenu.pick}
          onHover={mentionMenu.setSelectedIndex}
          onDismiss={mentionMenu.close}
          editorElement={editorElement}
        />
      ) : null}
    </>
  );
}
