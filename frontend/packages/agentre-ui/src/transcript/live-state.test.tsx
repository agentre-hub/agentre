import { act, render, screen } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

import {
  TranscriptLiveStateProvider,
  useMarkToolPermissionResolved,
  useIsStreamActive,
  type TranscriptLiveState,
} from "./live-state";

/**
 * 转录卡片要读宿主的「实时流」状态，但这层**不能**并进 TranscriptPorts：
 * 端口是一个模块级常量对象，往上面挂 `isStreamActive()` 只能拿到调用那一刻的值，
 * 流结束时没有任何东西通知 React 重渲染 —— 计划卡的按钮会一直停在禁用态。
 * 所以这层的契约是 **hook**，由宿主用自己的 store（桌面端是 zustand）实现。
 */

function StreamProbe({ sessionId }: { sessionId?: number }) {
  const active = useIsStreamActive(sessionId);

  return <span data-testid="probe">{active ? "active" : "idle"}</span>;
}

describe("useIsStreamActive", () => {
  it("给定没有 Provider 的宿主，当卡片询问流状态，则得到 idle 而不是抛错", () => {
    // 端口用 null + 抛错，因为「忘了接线」会让按钮点了没反应；实时流不一样：
    // 一个只读转录查看器（server 端第一版就是）本来就没有流，"没有流" 是这类
    // 宿主的**正确**答案，不是接线遗漏。
    render(<StreamProbe sessionId={1} />);

    expect(screen.getByTestId("probe")).toHaveTextContent("idle");
  });

  it("给定宿主报告流在跑，当会话 id 缺失，则仍是 idle（问不出所属会话就不该禁用动作）", () => {
    const liveState: TranscriptLiveState = {
      useIsStreamActive: () => true,
      markToolPermissionResolved: () => {},
    };

    render(
      <TranscriptLiveStateProvider value={liveState}>
        <StreamProbe sessionId={undefined} />
      </TranscriptLiveStateProvider>,
    );

    expect(screen.getByTestId("probe")).toHaveTextContent("idle");
  });

  it("给定流从跑到停，当宿主状态变化，则消费方重渲染 —— 这是它必须是 hook 的全部理由", () => {
    // 宿主实现用真的 useState 订阅，模拟 zustand selector 的反应式行为。
    let setActive: ((v: boolean) => void) | undefined;

    const liveState: TranscriptLiveState = {
      useIsStreamActive: (sessionId) => {
        const [active, set] = React.useState(true);
        setActive = set;
        return sessionId ? active : false;
      },
      markToolPermissionResolved: () => {},
    };

    render(
      <TranscriptLiveStateProvider value={liveState}>
        <StreamProbe sessionId={7} />
      </TranscriptLiveStateProvider>,
    );
    expect(screen.getByTestId("probe")).toHaveTextContent("active");

    act(() => setActive?.(false));

    expect(screen.getByTestId("probe")).toHaveTextContent("idle");
  });
});

function ResolveProbe() {
  const markResolved = useMarkToolPermissionResolved();

  return (
    <button
      onClick={() =>
        markResolved({
          sessionId: 1,
          assistantMessageId: 2,
          toolPermission: {
            requestId: "req-1",
            toolName: "Bash",
            toolInput: { command: "ls" },
            resolved: true,
            allowed: true,
          },
        })
      }
    >
      resolve
    </button>
  );
}

describe("useMarkToolPermissionResolved", () => {
  it("给定没有 Provider 的宿主，当卡片做乐观更新，则静默跳过而不是抛错", () => {
    // 乐观更新是纯装饰：权威状态仍由后端事件覆盖回来。没有实时 store 的宿主
    // 只是少了那一下即时反馈，不会停在错误状态上。
    render(<ResolveProbe />);

    expect(() => screen.getByText("resolve").click()).not.toThrow();
  });

  it("给定接了实时 store 的宿主，当卡片做乐观更新，则宿主收到原样载荷", () => {
    const markToolPermissionResolved = vi.fn();

    render(
      <TranscriptLiveStateProvider
        value={{
          useIsStreamActive: () => false,
          markToolPermissionResolved,
        }}
      >
        <ResolveProbe />
      </TranscriptLiveStateProvider>,
    );

    screen.getByText("resolve").click();

    expect(markToolPermissionResolved).toHaveBeenCalledWith({
      sessionId: 1,
      assistantMessageId: 2,
      toolPermission: {
        requestId: "req-1",
        toolName: "Bash",
        toolInput: { command: "ls" },
        resolved: true,
        allowed: true,
      },
    });
  });
});
