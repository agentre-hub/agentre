import type * as React from "react";

// notice 取代旧 window.alert：所有 RPC 失败 / 重要提示统一渲染成 composer 上方的内联 Alert。
// kind=info 用于成功后的提醒（带 token 复制等），error 用于失败；用户点 × 关闭即可。
type ChatPanelNotice = {
  kind: "error" | "info";
  text: string;
  detail?: string;
  // 发送失败草稿保留后的补救动作:Retry 重发同一条消息,Discard 清掉恢复的草稿。
  actions?: {
    retry: () => void;
    discard: () => void;
  };
};

type SetChatPanelNotice = React.Dispatch<
  React.SetStateAction<ChatPanelNotice | null>
>;

export type { ChatPanelNotice, SetChatPanelNotice };
