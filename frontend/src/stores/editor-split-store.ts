import { create } from "zustand";
import { persist } from "zustand/middleware";

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "./file-preview-tabs-store";

/**
 * 编辑区分屏（spec「编辑区与文件」）：会话开着文件时编辑区分成 对话 | 文件 两栏。
 * 分不分屏由这个会话有没有打开的预览标签决定（file-preview-tabs-store，按会话），
 * 这里只记两件事——两栏比例（全局、跨重启）与「文件占满」（按会话、瞬时）。
 */
export const EDITOR_SPLIT_KEY = "editor-split-state";

/** 文件一侧占编辑区宽度的比例。 */
export const DEFAULT_FILE_RATIO = 0.5;
export const FILE_RATIO_LIMITS = { min: 0.2, max: 0.8 } as const;

type EditorSplitState = {
  /** 文件一侧占编辑区宽度的比例；拖分隔条改它，所有会话共用一份。 */
  fileRatio: number;
  /**
   * 哪些会话处在「文件占满」。瞬时、不持久化；会话的最后一个文件标签关掉时随之
   * 清除——下次再开文件是分屏，而不是直接占满。
   */
  fileFullBySession: Record<number, true>;
  /** 设比例；超出上下限时收在边界上。 */
  setFileRatio: (ratio: number) => void;
  /** 占满 ⇄ 还原；会话没有打开的文件时什么都不做。 */
  toggleFileFull: (sessionId: number) => void;
};

function isValidRatio(v: unknown): v is number {
  return (
    typeof v === "number" &&
    v >= FILE_RATIO_LIMITS.min &&
    v <= FILE_RATIO_LIMITS.max
  );
}

function withoutSession(
  table: Record<number, true>,
  sessionId: number,
): Record<number, true> {
  const next = { ...table };
  delete next[sessionId];
  return next;
}

export const useEditorSplitStore = create<EditorSplitState>()(
  persist(
    (set) => ({
      fileRatio: DEFAULT_FILE_RATIO,
      fileFullBySession: {},
      setFileRatio: (ratio) =>
        set((s) => {
          const next = Math.min(
            FILE_RATIO_LIMITS.max,
            Math.max(FILE_RATIO_LIMITS.min, ratio),
          );
          return Number.isNaN(next) || next === s.fileRatio
            ? s
            : { fileRatio: next };
        }),
      toggleFileFull: (sessionId) =>
        set((s) => {
          if (s.fileFullBySession[sessionId]) {
            return {
              fileFullBySession: withoutSession(s.fileFullBySession, sessionId),
            };
          }
          const open =
            selectActivePreviewTab(
              useFilePreviewTabsStore.getState(),
              sessionId,
            ) !== null;
          return open
            ? {
                fileFullBySession: {
                  ...s.fileFullBySession,
                  [sessionId]: true,
                },
              }
            : s;
        }),
    }),
    {
      name: EDITOR_SPLIT_KEY,
      partialize: (s) => ({ fileRatio: s.fileRatio }),
      // 读回值非法（手改、旧版本）时回落到默认比例。
      merge: (persisted, current) => {
        const saved = (persisted ?? {}) as { fileRatio?: unknown };
        return {
          ...current,
          fileRatio: isValidRatio(saved.fileRatio)
            ? saved.fileRatio
            : DEFAULT_FILE_RATIO,
        };
      },
    },
  ),
);

// 会话的标签组整条消失（关掉最后一个 / 全部关闭 / 被淘汰）= 分屏收起，占满随之作废。
useFilePreviewTabsStore.subscribe((tabs) => {
  const { fileFullBySession } = useEditorSplitStore.getState();
  let next: Record<number, true> | null = null;
  for (const key of Object.keys(fileFullBySession)) {
    const sessionId = Number(key);
    if (tabs.previewTabsBySession[sessionId]) continue;
    next = withoutSession(next ?? fileFullBySession, sessionId);
  }
  if (next !== null) useEditorSplitStore.setState({ fileFullBySession: next });
});
