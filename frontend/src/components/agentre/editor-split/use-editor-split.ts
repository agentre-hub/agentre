import { useEditorSplitStore } from "@/stores/editor-split-store";
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";

export type EditorSplitLayout = "chat" | "split" | "file-full";

export type EditorSplit = {
  /** 编辑区此刻的样子：只有对话 / 对话 | 文件 / 文件占满（对话缩成窄栏）。 */
  layout: EditorSplitLayout;
  /** 文件一侧占编辑区宽度的比例（全局一份，跨重启）。 */
  fileRatio: number;
  setFileRatio: (ratio: number) => void;
  /** 占满 ⇄ 还原（按钮与 ⇧⌘' 共用）；没有打开的文件时不做事。 */
  toggleFileFull: () => void;
};

/**
 * 一个会话的编辑区分屏状态。会话开着文件标签（file-preview-tabs-store 按会话记）
 * 就分屏，关掉最后一个就收起；占满也是按会话的。
 */
export function useEditorSplit(sessionId: number): EditorSplit {
  const open = useFilePreviewTabsStore(
    (s) => selectActivePreviewTab(s, sessionId) !== null,
  );
  const fileFull = useEditorSplitStore(
    (s) => s.fileFullBySession[sessionId] === true,
  );
  const fileRatio = useEditorSplitStore((s) => s.fileRatio);
  const setFileRatio = useEditorSplitStore((s) => s.setFileRatio);
  const toggle = useEditorSplitStore((s) => s.toggleFileFull);
  return {
    layout: !open ? "chat" : fileFull ? "file-full" : "split",
    fileRatio,
    setFileRatio,
    toggleFileFull: () => toggle(sessionId),
  };
}
