import { render, act } from "@testing-library/react";
import { Profiler, type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { LocalCommandsProvider } from "./access";
import {
  createFakeLocalCommands,
  type FakeLocalCommands,
} from "./__testing__/fake-local-commands";
import { OutputTerminal } from "./output-terminal";

// --- xterm mocks (mirror terminal-panel.test.tsx) ---
const writeMock = vi.fn();
const openMock = vi.fn();
const disposeMock = vi.fn();
const resizeMock = vi.fn();
vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation(function () {
    return {
      open: openMock,
      write: writeMock,
      loadAddon: vi.fn(),
      dispose: disposeMock,
      resize: resizeMock,
      focus: vi.fn(),
      cols: 80,
      rows: 24,
      buffer: { active: { length: 1, baseY: 0, cursorY: 0 } },
      options: { theme: undefined as Record<string, string> | undefined },
    };
  }),
}));
import { Terminal } from "@xterm/xterm";
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(function () {
    return { fit: vi.fn(), proposeDimensions: () => ({ cols: 80, rows: 24 }) };
  }),
}));
vi.mock("@xterm/addon-web-links", () => ({ WebLinksAddon: vi.fn() }));

// IntersectionObserver harness: lets a test fire (or withhold) intersection.
type IOCallback = (
  entries: IntersectionObserverEntry[],
  observer: IntersectionObserver,
) => void;
let ioInstances: Array<{ cb: IOCallback; target?: Element }> = [];
class IOMock {
  cb: IOCallback;
  constructor(cb: IOCallback) {
    this.cb = cb;
    ioInstances.push({ cb });
  }
  observe(el: Element) {
    const i = ioInstances.find((x) => x.cb === this.cb);
    if (i) i.target = el;
  }
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}
function fireIntersect() {
  for (const inst of ioInstances) {
    inst.cb(
      [
        {
          isIntersecting: true,
          target: inst.target as Element,
        } as unknown as IntersectionObserverEntry,
      ],
      inst as unknown as IntersectionObserver,
    );
  }
}

let commands: FakeLocalCommands;
let originalIO: typeof IntersectionObserver | undefined;

function renderWithHost(ui: ReactNode) {
  return render(
    <LocalCommandsProvider access={commands.access}>
      {ui}
    </LocalCommandsProvider>,
  );
}

beforeEach(() => {
  writeMock.mockClear();
  resizeMock.mockClear();
  vi.mocked(Terminal).mockClear();
  ioInstances = [];
  commands = createFakeLocalCommands();
  originalIO = globalThis.IntersectionObserver;
});
afterEach(() => {
  if (originalIO) {
    (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver =
      originalIO;
  } else {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
  }
});

describe("OutputTerminal", () => {
  it("writes raw ANSI output to xterm verbatim — no stripping, so xterm renders color instead of leaving 乱码 control bytes", () => {
    // No IntersectionObserver → eager-mount fallback (output must always show).
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    const raw = "\x1b[31mred\x1b[0m done";
    commands.start({ id: "t1", command: "echo" });
    commands.appendOutput("t1", raw);

    renderWithHost(<OutputTerminal terminalId="t1" />);

    expect(writeMock).toHaveBeenCalledWith(raw);
  });

  it("creates a read-only xterm (stdin disabled, no blinking cursor)", () => {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "t2", command: "echo" });

    renderWithHost(<OutputTerminal terminalId="t2" />);

    const opts = vi.mocked(Terminal).mock.calls[0][0]!;
    expect(opts.disableStdin).toBe(true);
    expect(opts.cursorBlink).toBe(false);
  });

  it("streams later output deltas to xterm as the command keeps running", () => {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "t3", command: "go test" });
    renderWithHost(<OutputTerminal terminalId="t3" />);
    writeMock.mockClear();

    act(() => commands.appendOutput("t3", "=== RUN\n"));

    expect(writeMock).toHaveBeenCalledWith("=== RUN\n");
  });

  /**
   * 流式输出的**性能守卫**。
   *
   * 输出是流:一条长跑命令会吐成百上千个 chunk。只要它经过 React 状态(反应式读
   * 一个含 output 的投影、或把 chunk 塞进 useState),就会变成每 chunk 一次重渲染 ——
   * 而这在桌面端只表现为"有点卡",没有任何机械信号。所以这里把"渲染次数不随
   * chunk 数增长"钉成断言,而不是写在注释里。
   *
   * 每个 chunk 单独 act():否则一个 act 里的多次追加会被 React 批处理成一次渲染,
   * 那样即使实现真的是反应式的,用例也照样绿 —— 是个假绿的守卫。
   *
   * 第一块输出单独喂:它把投影里的 hasOutput 从 false 翻成 true(占位分支的依据),
   * 那是一次**只发生一次**的状态变化,与 chunk 数无关,不该算进增长里。
   */
  it("Given a long-running command, When many output chunks arrive, Then the render count does not grow with the number of chunks", () => {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "perf", command: "go test ./..." });
    let renders = 0;
    renderWithHost(
      <Profiler id="output-terminal" onRender={() => (renders += 1)}>
        <OutputTerminal terminalId="perf" />
      </Profiler>,
    );
    // hasOutput false→true 的那一次:只发生一次,不随 chunk 数增长。
    act(() => commands.appendOutput("perf", "=== RUN\n"));
    const rendersBeforeStreaming = renders;
    writeMock.mockClear();

    const CHUNKS = 64;
    for (let i = 0; i < CHUNKS; i += 1) {
      act(() => commands.appendOutput("perf", `--- PASS: Test${i}\n`));
    }

    expect(renders).toBe(rendersBeforeStreaming);
    // 而输出确实全都到了 xterm —— 否则"零重渲染"只是因为什么都没发生。
    expect(writeMock).toHaveBeenCalledTimes(CHUNKS);
    expect(writeMock).toHaveBeenLastCalledWith(`--- PASS: Test${CHUNKS - 1}\n`);
  });

  it("lazy-mounts: builds no xterm until the card scrolls into view", () => {
    (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver =
      IOMock as unknown as typeof IntersectionObserver;
    commands.start({ id: "t4", command: "echo" });
    commands.appendOutput("t4", "hidden output");

    renderWithHost(<OutputTerminal terminalId="t4" />);
    // Not yet visible → no terminal constructed, nothing written.
    expect(Terminal).not.toHaveBeenCalled();
    expect(writeMock).not.toHaveBeenCalled();

    act(() => fireIntersect());

    expect(Terminal).toHaveBeenCalled();
    expect(writeMock).toHaveBeenCalledWith("hidden output");
  });

  it("sizes the container to content (min rows) instead of a fixed 176px void", () => {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "h1", command: "echo" });
    commands.appendOutput("h1", "one line\n");

    const { getByTestId } = renderWithHost(<OutputTerminal terminalId="h1" />);

    // 1 content row → clamped to MIN_ROWS(3): 3*18(fallback)+12 = 66px. No h-44.
    const box = getByTestId("local-command-terminal");
    expect(box.style.height).toBe("66px");
    expect(box.className).not.toContain("h-44");
  });

  it("finished command with empty output shows a 无输出 placeholder, builds no xterm", () => {
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "e1", command: "touch x" });
    commands.finish("e1", "done", 0);

    const { getByTestId } = renderWithHost(<OutputTerminal terminalId="e1" />);

    expect(getByTestId("local-command-terminal").textContent).toMatch(
      /No output/,
    );
    expect(Terminal).not.toHaveBeenCalled();
  });

  it("disposes the mounted xterm instead of leaking it when a running-empty command finishes with still-empty output", () => {
    // No IntersectionObserver → eager mount, so the terminal builds immediately
    // while the (silent, long-running) command is still "running".
    delete (globalThis as { IntersectionObserver?: unknown })
      .IntersectionObserver;
    commands.start({ id: "e2", command: "sleep 3" });

    const { getByTestId } = renderWithHost(<OutputTerminal terminalId="e2" />);

    // running + empty output → not isEmptyFinished → a real xterm was built.
    expect(Terminal).toHaveBeenCalled();
    disposeMock.mockClear();

    // Command finishes without ever having produced output.
    act(() => commands.finish("e2", "done", 0));

    // The component now renders the placeholder branch — the previously
    // mounted xterm must have been disposed, not leaked.
    expect(disposeMock).toHaveBeenCalled();
    expect(getByTestId("local-command-terminal").textContent).toMatch(
      /No output/,
    );
  });
});
