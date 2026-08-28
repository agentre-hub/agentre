import type { MentionKind } from "./mentions/xml";
import type { LocalCommandHistoryScope } from "./local-command-history/types";

export type { LocalCommandHistoryScope };

export type LocalCommandSubmitHandler = (
  command: string,
) => LocalCommandHistoryScope | void | Promise<LocalCommandHistoryScope | void>;

export interface AIChatInputDraft {
  content: string;
}

export interface AIChatInputHandle {
  focus: () => void;
  clear: () => void;
  isEmpty: () => boolean;
  submit: () => void;
  loadDraft: (draft: string | AIChatInputDraft) => void;
  insertText: (text: string) => void;
}

export interface ProseMirrorLikeNode {
  type: { name: string };
  text?: string;
  attrs: Record<string, unknown>;
  descendants: (fn: (node: ProseMirrorLikeNode) => boolean | void) => void;
}

export interface TipTapTextNode {
  type: "text";
  text: string;
}

export interface TipTapMentionNode {
  type: "mention";
  attrs: {
    kind: MentionKind;
    refId: number;
    label: string;
    path: string;
    color?: string;
  };
}

export interface TipTapParagraphNode {
  type: "paragraph";
  content?: (TipTapTextNode | TipTapMentionNode)[];
}

export interface TipTapDocNode {
  type: "doc";
  content: TipTapParagraphNode[];
}

export type InputHistoryDirection = "up" | "down";

export interface InputHistoryNavigationOptions {
  direction: InputHistoryDirection;
  currentText: string;
  historyIndex: number;
  userMessageHistory: string[];
  canStartHistory: boolean;
  canContinueHistory: boolean;
}
