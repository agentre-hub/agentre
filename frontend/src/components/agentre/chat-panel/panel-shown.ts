import * as React from "react";

// 这个聊天面板此刻是否真摆在用户眼前。宿主切走面板时只用 CSS 把它藏起来、不卸载
// （非当前 Tab 是 invisible，离开 /chat 时整块 display:none），挂在面板里的浮层收不到
// 任何关闭信号，就由宿主经这里告诉它们。不在任何面板里渲染时视为看得见。
export const PanelShownContext = React.createContext(true);

export function usePanelShown(): boolean {
  return React.useContext(PanelShownContext);
}
