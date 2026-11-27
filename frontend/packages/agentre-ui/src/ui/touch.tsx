import * as React from "react";

// 触屏档位：菜单行 44px、按钮按下态等「只在触屏上换」的外观，由宿主显式打开。
// 不靠 `(pointer: coarse)` 媒体查询——桌面端的触摸屏笔记本也会命中，而本包的
// 约定（ChatComposer inputMode="touch"、session-row touch）一直是宿主说了算。
// 宿主在手机壳外包一层 TouchProvider，或对单个组件传 `touch`；显式入参优先。
const TouchContext = React.createContext(false);

function TouchProvider({
  touch = true,
  children,
}: {
  touch?: boolean;
  children?: React.ReactNode;
}) {
  return <TouchContext value={touch}>{children}</TouchContext>;
}

function useTouch(override?: boolean): boolean {
  const inherited = React.useContext(TouchContext);
  return override ?? inherited;
}

export { TouchProvider, useTouch };

/** 触屏档的菜单行：44px 高、15px 字（两种菜单共用，样式只写一份）。 */
const TOUCH_MENU_ROW = "min-h-11 text-prose";
export { TOUCH_MENU_ROW };
