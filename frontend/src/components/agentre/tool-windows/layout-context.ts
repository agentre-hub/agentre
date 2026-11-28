import * as React from "react";

type ToolWindowLayoutContextValue = {
  /** 把焦点还给编辑区里的输入框（Esc / ⇧Esc 之后）。 */
  focusEditor: () => void;
};

export const ToolWindowLayoutContext =
  React.createContext<ToolWindowLayoutContextValue>({
    focusEditor: () => {},
  });

export function useToolWindowLayoutContext(): ToolWindowLayoutContextValue {
  return React.useContext(ToolWindowLayoutContext);
}
