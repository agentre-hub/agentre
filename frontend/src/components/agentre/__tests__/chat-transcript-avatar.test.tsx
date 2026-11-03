import { render, renderHook, screen } from "@testing-library/react";
import { describe, expect, it, onTestFinished } from "vitest";

import {
  ChatMessage,
  MESSAGE_AVATAR_CLASS,
  presetForKey,
} from "@agentre-hub/agentre-ui";

import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { useTranscriptRowView } from "../chat/transcript/use-transcript-row-view";
import { AgentAvatar } from "../primitives";

// 头像归属从 MessageRow 挪到了调用方：MessageRow 搬进 @agentre-hub/agentre-ui 后
// 不再内置任何头像（那里不认识桌面端的 16 色 agent 调色板 / icon-registry），
// 尺寸一致性因此改由 ChatMessage 保证。原先锁在 message-row.test.tsx 上的
// 「彩色头像用规范尺寸 size-7」断言跟着被守卫的代码一起搬到这里 ——
// 否则搬迁会顺手把这条一致性护栏丢掉。
describe("ChatMessage 头像列", () => {
  it("assistant 消息的彩色头像用规范尺寸 size-7(锁住一致性)", () => {
    render(
      // 与 chat/transcript/use-transcript-row-view.tsx 里 renderCtx.agentAvatar 的构造保持一致 —— 这条守卫要锁的
      // 正是「宿主这样构造出来的头像，落在规范的头像列尺寸上」。
      <ChatMessage
        author="后端"
        avatar={
          <AgentAvatar
            name="后端"
            initials="后"
            color="agent-2"
            size="md"
            className={MESSAGE_AVATAR_CLASS}
          />
        }
        time="10:00"
      >
        正文
      </ChatMessage>,
    );

    const avatar = screen.getByLabelText("后端");
    expect(avatar.className).toContain("size-7");
    expect(screen.getByText("正文")).toBeInTheDocument();
  });
});

// 规格 2026-11-02 决策 17：转录消息头像此前只画首字母 + 颜色，现在按
// 上传图片 ▸ 预设 ▸ 图标 ▸ 首字母显示完整身份。会话 meta 没有图标 / 上传图片，
// 所以 useTranscriptRowView 按 sessionId 找回 agentId 再去 chat-agents-store 取。
describe("转录消息头像按完整身份显示", () => {
  const sessionId = 42;
  const agentId = 7;

  function renderAvatar(identity: {
    avatarIcon?: string;
    avatarDataUrl?: string;
  }) {
    useChatAgentsStore.setState({
      agents: [
        {
          id: agentId,
          name: "旧名字",
          avatarColor: "agent-9",
          avatarIcon: "",
          avatarDataUrl: "",
          sessionIds: [],
          ...identity,
        },
      ] as never,
    });
    onTestFinished(() => useChatAgentsStore.getState().__reset());
    onTestFinished(() => useSessionMetaStore.getState().__reset());
    useSessionMetaStore.getState().setMeta(sessionId, {
      agentId,
      agentName: "旧名字",
      agentColor: "agent-2",
      title: "t",
    });

    const { result } = renderHook(() =>
      useTranscriptRowView({
        agentName: "旧名字",
        agentColor: "agent-2",
        sessionId,
        lastAssistantId: null,
        streaming: false,
        liveCompacting: false,
        reconnecting: false,
        fallbackModel: "",
      }),
    );
    // 头像节点由 hook 交给包的 Context，这里把它单独渲染出来看这一枚记号。
    render(<>{result.current.renderCtx.agentAvatar}</>);
    return screen.getByRole("img", { name: "旧名字" });
  }

  it("上传图片：头像画的是图片", () => {
    const avatar = renderAvatar({
      avatarDataUrl: "data:image/png;base64,AAAA",
    });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      "data:image/png;base64,AAAA",
    );
  });

  it("预设 key：头像画注册表里的预设图", () => {
    const avatar = renderAvatar({ avatarIcon: "preset:agent-assistant" });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      presetForKey("preset:agent-assistant")?.src,
    );
  });

  it("图标 key：头像画词表里的图标，而不是首字母", () => {
    const avatar = renderAvatar({ avatarIcon: "hammer" });

    expect(avatar.querySelector("svg")).not.toBeNull();
    expect(avatar).not.toHaveTextContent("旧");
  });

  it("没有图标：头像照旧画首字母", () => {
    const avatar = renderAvatar({});

    expect(avatar).toHaveTextContent("旧");
  });
});
