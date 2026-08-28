import { act, render } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { useFileDropZone, type DropZoneRegistrar } from "../use-file-drop";

// 原生 drop 通道是宿主能力（桌面端接 Wails 的 OnFileDrop），这里注入一个空实现：
// 本组用例只覆盖包自己那段纯 DOM 的高亮/preventDefault 逻辑。
const noopRegistrar: DropZoneRegistrar = vi.fn(() => () => {});

function Harness() {
  const ref = useRef<HTMLDivElement>(null);
  const { isDragOver } = useFileDropZone({
    ref,
    enabled: true,
    onPaths: () => {},
    registerDropZone: noopRegistrar,
  });
  return (
    <div ref={ref} data-testid="zone">
      {isDragOver ? "over" : "idle"}
    </div>
  );
}

// 带 dataTransfer.types 的拖拽事件(happy-dom 没有 DataTransfer 构造)。
function fireDrag(el: Element, type: string, hasFiles = true): Event {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "dataTransfer", {
    value: { types: hasFiles ? ["Files"] : [] },
  });
  act(() => {
    el.dispatchEvent(ev);
  });
  return ev;
}

describe("useFileDropZone", () => {
  it("dragenter/dragleave 切换 isDragOver", () => {
    const { getByTestId } = render(<Harness />);
    const zone = getByTestId("zone");
    fireDrag(zone, "dragenter");
    expect(zone.textContent).toBe("over");
    fireDrag(zone, "dragleave");
    expect(zone.textContent).toBe("idle");
  });

  it("非文件拖拽不触发高亮", () => {
    const { getByTestId } = render(<Harness />);
    const zone = getByTestId("zone");
    fireDrag(zone, "dragenter", false);
    expect(zone.textContent).toBe("idle");
  });

  it("dragover/drop 调 preventDefault(阻止 webview 导航)", () => {
    const { getByTestId } = render(<Harness />);
    const zone = getByTestId("zone");
    const over = fireDrag(zone, "dragover");
    const drop = fireDrag(zone, "drop");
    expect(over.defaultPrevented).toBe(true);
    expect(drop.defaultPrevented).toBe(true);
  });

  it("drop 后 isDragOver 复位", () => {
    const { getByTestId } = render(<Harness />);
    const zone = getByTestId("zone");
    fireDrag(zone, "dragenter");
    expect(zone.textContent).toBe("over");
    fireDrag(zone, "drop");
    expect(zone.textContent).toBe("idle");
  });
});
