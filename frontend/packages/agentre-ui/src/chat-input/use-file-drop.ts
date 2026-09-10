import { useEffect, useRef, useState, type RefObject } from "react";

/**
 * 把一个元素登记成原生文件 drop 目标,并返回注销函数。
 *
 * 这是**宿主平台能力**,不是纯 DOM:桌面端那份要挂 Wails 的 `OnFileDrop`
 * (每窗口唯一的全局回调 + `--wails-drop-target` CSS 标记),浏览器里压根拿不到
 * 落盘的绝对路径。所以注册器由调用方注入,包只负责下面那段纯 DOM 的高亮逻辑。
 * 桌面端实现见 `src/lib/file-drop.ts`。
 */
export type DropZoneRegistrar = (
  element: HTMLElement,
  handler: (paths: string[]) => void,
) => () => void;

// useFileDropZone 把一个元素注册成原生文件 drop 目标,并用 HTML5 拖拽事件驱动
// 高亮状态。真实落地路径来自 registerDropZone(宿主的原生通道),HTML5 事件只负责:
//   - dragenter/dragleave → isDragOver 高亮(进入计数避免子元素抖动);
//   - dragover/drop preventDefault → 阻止 webview 把拖入文件当导航/打开。
export function useFileDropZone(opts: {
  ref: RefObject<HTMLElement | null>;
  enabled: boolean;
  onPaths: (paths: string[]) => void;
  registerDropZone: DropZoneRegistrar;
}): { isDragOver: boolean } {
  const { ref, enabled, onPaths, registerDropZone } = opts;
  const [isDragOver, setIsDragOver] = useState(false);
  const onPathsRef = useRef(onPaths);
  useEffect(() => {
    onPathsRef.current = onPaths;
  }, [onPaths]);

  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;

    const unregister = registerDropZone(el, (paths) =>
      onPathsRef.current(paths),
    );

    let depth = 0;
    const onDragEnter = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes("Files")) return;
      depth++;
      setIsDragOver(true);
    };
    const onDragOver = (e: DragEvent) => e.preventDefault();
    const onDragLeave = () => {
      depth = Math.max(0, depth - 1);
      if (depth === 0) setIsDragOver(false);
    };
    const onDrop = (e: DragEvent) => {
      e.preventDefault();
      depth = 0;
      setIsDragOver(false);
    };

    el.addEventListener("dragenter", onDragEnter);
    el.addEventListener("dragover", onDragOver);
    el.addEventListener("dragleave", onDragLeave);
    el.addEventListener("drop", onDrop);
    return () => {
      unregister();
      el.removeEventListener("dragenter", onDragEnter);
      el.removeEventListener("dragover", onDragOver);
      el.removeEventListener("dragleave", onDragLeave);
      el.removeEventListener("drop", onDrop);
    };
  }, [ref, enabled, registerDropZone]);

  return { isDragOver };
}
