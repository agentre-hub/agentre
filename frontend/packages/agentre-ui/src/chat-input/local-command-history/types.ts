import type {
  LocalCommandHistoryEntry,
  LocalCommandHistoryScope,
} from "./access";

export type { LocalCommandHistoryEntry, LocalCommandHistoryScope };

export type LocalCommandHistoryMenuState = {
  readonly open: boolean;
  readonly anchorRect: {
    readonly left: number;
    readonly top: number;
    readonly bottom: number;
  } | null;
  readonly items: LocalCommandHistoryEntry[];
  readonly selectedIndex: number;
  readonly clearFocused: boolean;
  readonly query: string;
};
