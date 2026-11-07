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
  /** 接到现有草稿后面（空则直接填入，否则空一行再接），光标停在末尾。 */
  appendDraft: (text: string) => void;
  /**
   * 撤销最近一次 `appendDraft`：只有文档自那次追加后一个字都没被改过才生效，返回 true；
   * 没有可撤的追加、或已被改过（见 `onAppendEdited`）时不动作，返回 false。
   */
  removeLastAppend: () => boolean;
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
    fp?: string;
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
