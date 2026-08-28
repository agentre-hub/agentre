import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  ProviderPillResolution,
  ProviderPillTrigger,
  type ProviderPillState,
} from "./provider-pill-trigger";

function state(over: Partial<ProviderPillState> = {}): ProviderPillState {
  return {
    mode: "fixed",
    providerLabel: "",
    providerType: "",
    modelLabel: "",
    resolutionLabel: "",
    dynamic: false,
    cliLogin: false,
    ...over,
  };
}

describe("ProviderPillTrigger", () => {
  // 脸上写的是「实际会跑哪个模型」，而模式由图标承担。跟随态是唯一一个把模式
  // 写成字的：那句话正是用户问「为什么没和桌面端对齐」时指的那一格。
  it("跟随 Agent 绑定：写出这句话，并跟上解析到的模型", () => {
    render(
      <ProviderPillTrigger
        state={state({
          mode: "follow-agent",
          modelLabel: "claude-sonnet-4-6",
          providerLabel: "Anthropic",
          providerType: "anthropic",
        })}
      />,
    );
    expect(screen.getByText(/Follow agent binding/).textContent).toContain(
      "claude-sonnet-4-6",
    );
    expect(screen.getByTestId("follow-agent-icon")).toBeInTheDocument();
  });

  // 还没解析出任何东西时只写那句话，不硬凑一个「· 」的空尾巴。
  it("跟随绑定但还不知道解析到什么：只写那句话", () => {
    render(<ProviderPillTrigger state={state({ mode: "follow-agent" })} />);
    expect(screen.getByText("Follow agent binding")).toBeInTheDocument();
  });

  // 确知没绑供应商 → 下一轮的模型由 CLI 自己的登录账号决定，这才是真正的来源。
  it("确知没绑供应商：写 CLI 登录态而不是留空", () => {
    render(
      <ProviderPillTrigger
        state={state({ mode: "follow-agent", cliLogin: true })}
      />,
    );
    expect(screen.getByText(/Follow agent binding/).textContent).toContain(
      "CLI login state",
    );
  });

  it("固定模型：只写模型 ID，不写模式", () => {
    render(
      <ProviderPillTrigger
        state={state({ modelLabel: "gpt-5", providerType: "openai" })}
      />,
    );
    expect(screen.getByText("gpt-5")).toBeInTheDocument();
    expect(screen.queryByTestId("follow-agent-icon")).not.toBeInTheDocument();
  });

  // ↻ 只在「跟随供应商默认」时出现：模型会随供应商的默认值变，这是它与固定模型
  // 唯一的可见差别。
  it("跟随供应商默认：挂 ↻ 标记", () => {
    render(
      <ProviderPillTrigger
        state={state({
          mode: "provider-default",
          providerLabel: "Anthropic",
          providerType: "anthropic",
          dynamic: true,
        })}
      />,
    );
    expect(
      screen.getByTestId("provider-pill-dynamic-icon"),
    ).toBeInTheDocument();
  });

  it("失效态：写出解析目标 + 已失效，不重复挂图标", () => {
    render(
      <ProviderPillTrigger
        state={state({ mode: "invalid", modelLabel: "retired-model" })}
      />,
    );
    // 两段分处不同节点（模型 ID 走等宽），所以分开断言而不是拼字符串。
    expect(screen.getByText("retired-model")).toBeInTheDocument();
    expect(screen.getByText(/No longer valid/)).toBeInTheDocument();
  });
});

describe("ProviderPillResolution", () => {
  it("解析到供应商与模型：箭头 + 标识 + 供应商 · 模型", () => {
    render(
      <ProviderPillResolution
        boundProviderType="anthropic"
        boundProviderLabel="Anthropic"
        boundModelLabel="claude-sonnet-4-6"
        boundCliLogin={false}
      />,
    );
    const row = screen.getByTestId("special-resolution");
    expect(row.textContent).toContain("Anthropic");
    expect(row.textContent).toContain("claude-sonnet-4-6");
  });

  it("确知没绑供应商：箭头保留，但不画半个空标识", () => {
    render(
      <ProviderPillResolution
        boundProviderType=""
        boundProviderLabel=""
        boundModelLabel=""
        boundCliLogin
      />,
    );
    expect(screen.getByTestId("special-resolution").textContent).toContain(
      "Determined by the CLI's own login account",
    );
  });

  // 还不知道（既没解析出供应商，也不确知没绑）：回落纯文字，不摆那一行箭头 ——
  // 摆了就是在说「解析到了空」，而事实是「还没解析出来」。
  it("还不知道时回落纯文字，不摆解析行", () => {
    render(
      <ProviderPillResolution
        boundProviderType=""
        boundProviderLabel=""
        boundModelLabel=""
        boundCliLogin={false}
        fallbackLabel="Loading…"
      />,
    );
    expect(screen.queryByTestId("special-resolution")).not.toBeInTheDocument();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });
});
