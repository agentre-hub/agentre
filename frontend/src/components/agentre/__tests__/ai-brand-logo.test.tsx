import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  AgentBackendLogo,
  LlmModelLogo,
  LlmProviderLogo,
  resolveModelBrand,
} from "@agentre-hub/agentre-ui";

describe("AI brand logos", () => {
  it("Given supported backends, when rendered, then official brand artwork identifies them", () => {
    render(
      <>
        <AgentBackendLogo backendType="claudecode" />
        <AgentBackendLogo backendType="codex" />
        <AgentBackendLogo backendType="piagent" />
        <AgentBackendLogo backendType="openclaw" />
      </>,
    );

    expect(screen.getByRole("img", { name: "Claude" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Codex" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Pi" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "OpenClaw" })).toBeInTheDocument();
  });

  it.each([
    ["claude-sonnet-4-6", "claude"],
    ["gpt-5.4", "openai"],
    ["o3", "openai"],
    ["codex-mini-latest", "codex"],
    ["deepseek-chat", "deepseek"],
    ["glm-4.5", "glm"],
    ["codegeex-4", "glm"],
    ["kimi-k2.5", "kimi"],
    ["moonshot-v1-128k", "kimi"],
    ["MiniMax-M2.1", "minimax"],
    ["MiniMax-M2.5", "minimax"],
    ["abab6.5s-chat", "minimax"],
    ["gemini-2.5-pro", "gemini"],
    ["qwen3-coder", "qwen"],
    ["qwq-32b", "qwen"],
    ["mistral-large-latest", "mistral"],
    ["mixtral-8x22b", "mistral"],
    ["codestral-latest", "mistral"],
    ["llama-4-maverick", "meta"],
    ["grok-4", "xai"],
  ])("resolves model %s to the %s brand", (model, brand) => {
    expect(resolveModelBrand(model)).toBe(brand);
  });

  it("Given monochrome brands, when rendered, then their artwork uses the theme foreground color", () => {
    render(
      <>
        <LlmProviderLogo providerType="anthropic" />
        <LlmModelLogo model="kimi-k2.5" providerType="openai-chat" />
        <LlmModelLogo model="gpt-5.4" providerType="openai-response" />
        <LlmModelLogo model="grok-4" providerType="openai-chat" />
      </>,
    );

    for (const [label, brand] of [
      ["Anthropic", "anthropic"],
      ["Kimi", "kimi"],
      ["OpenAI", "openai"],
      ["xAI", "xai"],
    ]) {
      const logo = screen.getByRole("img", { name: label });
      const mark = logo.firstElementChild as HTMLElement;

      expect(logo).toHaveAttribute("data-brand", brand);
      expect(logo.querySelector("img")).not.toBeInTheDocument();
      expect(mark).toHaveClass(
        "block",
        "bg-foreground",
        "[mask-image:var(--brand-logo)]",
        "[-webkit-mask-image:var(--brand-logo)]",
      );
      expect(mark.style.getPropertyValue("--brand-logo")).toContain("url(");
    }
  });

  it("Given a colored brand, when rendered, then its official image colors remain unchanged", () => {
    render(<LlmModelLogo model="claude-sonnet-4-6" providerType="anthropic" />);

    const logo = screen.getByRole("img", { name: "Claude" });
    expect(logo).toHaveAttribute("data-brand", "claude");
    expect(logo.querySelector("img")).toBeInTheDocument();
    expect(logo.querySelector(".bg-foreground")).not.toBeInTheDocument();
  });

  it("Given an unknown model, when rendered, then it falls back to its provider brand", () => {
    render(<LlmModelLogo model="private-model-v1" providerType="anthropic" />);

    expect(screen.getByRole("img", { name: "Anthropic" })).toBeInTheDocument();
  });

  it("identifies compatible providers by their official name or endpoint before protocol fallback", () => {
    render(
      <>
        <LlmProviderLogo providerType="openai-chat" providerName="DeepSeek" />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://open.bigmodel.cn/api/paas/v4"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://api.moonshot.cn/v1"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://generativelanguage.googleapis.com/v1beta"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://dashscope.aliyuncs.com/compatible-mode/v1"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://api.mistral.ai/v1"
        />
        <LlmProviderLogo providerType="openai-chat" providerName="Meta Llama" />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://api.x.ai/v1"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://api.minimax.chat/v1"
        />
        <LlmProviderLogo
          providerType="openai-chat"
          baseUrl="https://api.minimaxi.com/v1"
        />
        <LlmProviderLogo providerType="openai-chat" providerName="MiniMax" />
      </>,
    );

    expect(screen.getByRole("img", { name: "DeepSeek" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "GLM" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Kimi" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Gemini" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Qwen" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Mistral AI" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Meta" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "xAI" })).toBeInTheDocument();
    expect(screen.getAllByRole("img", { name: "MiniMax" })).toHaveLength(3);
  });

  it("Given an unknown provider, when rendered, then it uses a text-logo fallback", () => {
    render(<LlmProviderLogo providerType="custom-compatible" />);

    expect(
      screen.getByRole("img", { name: "Custom compatible" }),
    ).toHaveTextContent("C");
  });
});
