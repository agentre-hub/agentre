import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  ListChatAgents: vi.fn(),
}));

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import { useAgentIdentity } from "./use-agent-identity";

// agents 的形态就是 ListChatAgents 的 ChatAgentItem；这里只种身份相关的几个字段。
function seedAgent(over: Record<string, unknown>) {
  useChatAgentsStore.setState({
    agents: [
      {
        id: 3,
        name: "设计师",
        avatarColor: "agent-9",
        avatarIcon: "preset:agent-assistant",
        avatarDataUrl: "data:image/png;base64,AAAA",
        ...over,
      },
    ] as never,
  });
}

describe("useAgentIdentity", () => {
  beforeEach(() => {
    useChatAgentsStore.getState().__reset();
  });

  it("agent 在 store 里时给出完整身份（名字 / 颜色 / 图标 / 上传图片）", () => {
    seedAgent({});

    const { result } = renderHook(() => useAgentIdentity(3));

    expect(result.current).toEqual({
      name: "设计师",
      color: "agent-9",
      avatarIcon: "preset:agent-assistant",
      avatarDataUrl: "data:image/png;base64,AAAA",
    });
  });

  it("agent 不在 store 里（或没有 agentId）时返回 null，交给调用方回落 meta", () => {
    seedAgent({});

    expect(renderHook(() => useAgentIdentity(99)).result.current).toBeNull();
    expect(renderHook(() => useAgentIdentity(0)).result.current).toBeNull();
    expect(renderHook(() => useAgentIdentity(null)).result.current).toBeNull();
  });
});
