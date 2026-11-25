import * as React from "react";

export type DecisionCardEnv = {
  /** 触屏档:提问卡标题本地化、控件 ≥44px、不画回车键帽。 */
  touch: boolean;
  /** 执行端连接未就绪:提问卡的提交 / 跳过禁用。 */
  connecting: boolean;
};

// 决策类卡片(提问卡)的宿主环境。和 ShortcutHintsProvider 同理单独一个上下文:
// 决策面板这类宿主经 CanonicalToolRouter 画卡,不在转录渲染上下文里,也要能
// 把触屏与连接状态交给卡片。卡片的显式入参优先于这里。
const DecisionCardEnvContext = React.createContext<DecisionCardEnv>({
  touch: false,
  connecting: false,
});

export type DecisionCardEnvProviderProps = Partial<DecisionCardEnv> & {
  children: React.ReactNode;
};

/** 不传的字段沿用外层的设置(最外层缺省均为 false)。 */
export function DecisionCardEnvProvider({
  touch,
  connecting,
  children,
}: DecisionCardEnvProviderProps) {
  const inherited = React.useContext(DecisionCardEnvContext);
  const value = React.useMemo(
    () => ({
      touch: touch ?? inherited.touch,
      connecting: connecting ?? inherited.connecting,
    }),
    [touch, connecting, inherited],
  );
  return (
    <DecisionCardEnvContext.Provider value={value}>
      {children}
    </DecisionCardEnvContext.Provider>
  );
}

export function useDecisionCardEnv(): DecisionCardEnv {
  return React.useContext(DecisionCardEnvContext);
}
