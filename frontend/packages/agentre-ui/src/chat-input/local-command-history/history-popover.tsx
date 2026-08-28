import * as React from "react";
import { Trash2 } from "lucide-react";

import { useUiTranslation } from "../../i18n";
import { cn } from "../../lib/utils";
import { Button } from "../../ui/button";

import { SuggestionPopover } from "../suggestion-popover";
import type {
  LocalCommandHistoryEntry,
  LocalCommandHistoryMenuState,
} from "./types";

export const LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR =
  "[data-local-command-history-clear]";

export function localCommandHistoryOptionId(
  listboxId: string,
  index: number,
): string {
  return `${listboxId}-option-${index}`;
}

export function LocalCommandHistoryPopover({
  state,
  listboxId,
  onPick,
  onHover,
  clearButtonRef,
  onClear,
  onClearFocus,
  onClearBlur,
  onClearKeyDown,
  onDismiss,
  editorElement,
}: {
  state: LocalCommandHistoryMenuState;
  listboxId: string;
  onPick: (entry: LocalCommandHistoryEntry) => void;
  onHover: (index: number) => void;
  clearButtonRef: React.Ref<HTMLButtonElement>;
  onClear: () => void;
  onClearFocus: () => void;
  onClearBlur: React.FocusEventHandler<HTMLButtonElement>;
  onClearKeyDown: React.KeyboardEventHandler<HTMLButtonElement>;
  onDismiss?: () => void;
  editorElement?: HTMLElement | null;
}): React.ReactElement | null {
  const { t } = useUiTranslation();

  return (
    <SuggestionPopover
      open={state.open}
      anchorRect={state.anchorRect}
      selectedIndex={state.selectedIndex}
      itemCount={state.items.length}
      ariaLabel={t("localCommandHistory.aria")}
      listboxId={listboxId}
      onDismiss={onDismiss}
      editorElement={editorElement}
      footer={
        <div role="presentation" className="mt-1 border-t border-border pt-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            ref={clearButtonRef}
            data-local-command-history-clear="true"
            aria-label={t("localCommandHistory.clearCurrentScope")}
            className="h-auto w-full justify-start rounded-sm px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            onFocus={onClearFocus}
            onBlur={onClearBlur}
            onKeyDown={onClearKeyDown}
            onClick={onClear}
          >
            <Trash2 className="size-3" aria-hidden="true" />
            {t("localCommandHistory.clearCurrentScope")}
          </Button>
        </div>
      }
    >
      {(activeRef) =>
        state.items.map((entry, index) => {
          const active = !state.clearFocused && index === state.selectedIndex;
          return (
            <button
              id={localCommandHistoryOptionId(listboxId, index)}
              key={entry.command}
              type="button"
              role="option"
              ref={active ? activeRef : undefined}
              tabIndex={-1}
              aria-label={entry.command}
              aria-selected={active}
              onMouseMove={() => onHover(index)}
              onMouseDown={(event) => {
                event.preventDefault();
                onPick(entry);
              }}
              className={cn(
                "flex w-full min-w-0 cursor-pointer items-center rounded-sm px-2 py-1.5 text-left text-xs",
                active ? "bg-accent text-accent-foreground" : "text-foreground",
              )}
            >
              <span className="min-w-0 flex-1 truncate font-mono">
                {entry.command}
              </span>
            </button>
          );
        })
      }
    </SuggestionPopover>
  );
}
