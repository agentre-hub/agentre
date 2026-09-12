import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  TranscriptPortsProvider,
  useTranscriptPorts,
  useOptionalPort,
} from "./ports-context";
import type { TranscriptPorts } from "./ports";

function makePorts(overrides: Partial<TranscriptPorts> = {}): TranscriptPorts {
  return {
    answerToolPermission: vi.fn(async () => {}),
    answerUserQuestion: vi.fn(async () => {}),
    answerToolApproval: vi.fn(async () => {}),
    resolveExecApproval: vi.fn(async () => ({ status: "ok" })),
    resolvePlanAction: vi.fn(async () => ({})),
    ...overrides,
  };
}

function PortConsumer() {
  const ports = useTranscriptPorts();
  return <span>{typeof ports.answerUserQuestion}</span>;
}

describe("TranscriptPortsProvider", () => {
  it("hands the injected ports to the transcript tree", () => {
    render(
      <TranscriptPortsProvider ports={makePorts()}>
        <PortConsumer />
      </TranscriptPortsProvider>,
    );

    expect(screen.getByText("function")).toBeTruthy();
  });

  it("fails loudly when the tree is mounted without a provider", () => {
    // 装配错误必须在挂载期就炸,而不是等用户点了审批按钮才发现没接线。
    // React 会把渲染异常打到 console.error,这里静音以免污染测试输出。
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(() => render(<PortConsumer />)).toThrow(/TranscriptPortsProvider/);

    spy.mockRestore();
  });

  it("reports an optional port as unavailable so callers can hide the affordance", () => {
    // 可选端口的语义是能力探测:宿主没实现就不该渲染那个按钮,
    // 而不是渲染出来点下去没反应。
    function OptionalConsumer() {
      const openPath = useOptionalPort("openPath");
      return <span>{openPath ? "available" : "unavailable"}</span>;
    }

    render(
      <TranscriptPortsProvider ports={makePorts()}>
        <OptionalConsumer />
      </TranscriptPortsProvider>,
    );

    expect(screen.getByText("unavailable")).toBeTruthy();
  });

  it("exposes an optional port once the host implements it", () => {
    function OptionalConsumer() {
      const openPath = useOptionalPort("openPath");
      return <span>{openPath ? "available" : "unavailable"}</span>;
    }

    render(
      <TranscriptPortsProvider ports={makePorts({ openPath: async () => {} })}>
        <OptionalConsumer />
      </TranscriptPortsProvider>,
    );

    expect(screen.getByText("available")).toBeTruthy();
  });
});
