import * as React from "react";

// 审批卡上的 ↵ / Esc 键帽是否隐藏。单独一个上下文而不是挂在
// TranscriptRenderContext 上:决策面板这类宿主直接用 CanonicalToolRouter 画卡,
// 不在转录的渲染上下文里,也得能把键帽关掉(触屏上没有键盘可按)。
const ShortcutHintsHiddenContext = React.createContext(false);

export type ShortcutHintsProviderProps = {
  /** true 去掉键帽;不传则沿用外层的设置(最外层缺省为显示)。 */
  hidden?: boolean;
  children: React.ReactNode;
};

export function ShortcutHintsProvider({
  hidden,
  children,
}: ShortcutHintsProviderProps) {
  const inherited = React.useContext(ShortcutHintsHiddenContext);
  return (
    <ShortcutHintsHiddenContext.Provider value={hidden ?? inherited}>
      {children}
    </ShortcutHintsHiddenContext.Provider>
  );
}

export function useShortcutHintsHidden(): boolean {
  return React.useContext(ShortcutHintsHiddenContext);
}
