import { render, screen } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

import type { LocalCommandsAccess } from "./access";
import {
  LocalCommandsProvider,
  useLocalCommand,
  useLocalCommandsAccess,
} from "./access";

function makeAccess(
  overrides: Partial<LocalCommandsAccess> = {},
): LocalCommandsAccess {
  return {
    useLocalCommand: () => undefined,
    subscribeOutput: vi.fn(() => () => {}),
    toggleExpanded: vi.fn(),
    remove: vi.fn(),
    ...overrides,
  };
}

function AccessConsumer() {
  const access = useLocalCommandsAccess();
  return <span>{typeof access.subscribeOutput}</span>;
}

function ViewConsumer({ id }: { id: string }) {
  const view = useLocalCommand(id);
  return <span data-testid="view">{view ? view.command : "none"}</span>;
}

describe("LocalCommandsProvider", () => {
  it("Given a host that wired the local-command access, When a card mounts, Then it reads that access from context", () => {
    render(
      <LocalCommandsProvider access={makeAccess()}>
        <AccessConsumer />
      </LocalCommandsProvider>,
    );

    expect(screen.getByText("function")).toBeTruthy();
  });

  it("Given no access was provided, When a card mounts anyway, Then it fails loudly at mount instead of rendering dead controls", () => {
    // 没有诚实的空实现:一个 no-op access 会渲染出「移除 / 折叠」按钮却点了没反应,
    // 正是 ports.ts 头注释里说的那类只有用户能发现的失败。装配漏接必须当场炸。
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(() => render(<AccessConsumer />)).toThrow(/LocalCommandsProvider/);

    spy.mockRestore();
  });

  it("Given the host entry changes, When the reactive read is used, Then the consumer re-renders with the new projection", () => {
    // 反应式读必须是 hook —— 宿主用自己的订阅机制实现(桌面端是 zustand selector),
    // 状态变化要能推着卡片重渲染。这里用 React 自己的 state 扮演那份宿主状态。
    let publish: ((command: string) => void) | undefined;
    const access = makeAccess({
      useLocalCommand(id) {
        const [command, setCommand] = React.useState("go test");
        publish = setCommand;
        return {
          id,
          sessionId: 1,
          command,
          status: "running",
          createdAt: 1,
          hasOutput: false,
        };
      },
    });

    render(
      <LocalCommandsProvider access={access}>
        <ViewConsumer id="t1" />
      </LocalCommandsProvider>,
    );
    expect(screen.getByTestId("view").textContent).toBe("go test");

    React.act(() => publish?.("go build"));

    expect(screen.getByTestId("view").textContent).toBe("go build");
  });
});
