import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ChatMessage } from "../transcript-row-view";

// 头像归属从 MessageRow 挪到了调用方：MessageRow 搬进 @agentre-ai/agentre-ui 后
// 不再内置任何头像（那里不认识桌面端的 16 色 agent 调色板 / icon-registry），
// 尺寸一致性因此改由 ChatMessage 保证。原先锁在 message-row.test.tsx 上的
// 「彩色头像用规范尺寸 size-7」断言跟着被守卫的代码一起搬到这里 ——
// 否则搬迁会顺手把这条一致性护栏丢掉。
describe("ChatMessage 头像列", () => {
  it("assistant 消息的彩色头像用规范尺寸 size-7(锁住一致性)", () => {
    render(
      <ChatMessage author="后端" avatarColor="agent-2" time="10:00">
        正文
      </ChatMessage>,
    );

    const avatar = screen.getByLabelText("后端");
    expect(avatar.className).toContain("size-7");
    expect(screen.getByText("正文")).toBeInTheDocument();
  });
});
