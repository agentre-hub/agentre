import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

const navigate = vi.fn();

vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

vi.mock("../../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import { useDesktopTranscriptPorts } from "../transcript-ports-desktop";

beforeEach(() => navigate.mockReset());

// 每种 @ 提及都有自己的去处。设备曾经会落进「不是 agent 就是项目」的 else 分支,
// 于是点一台机器会把人送到项目页 —— 这个用例守的就是那一步。
describe("useDesktopTranscriptPorts 的 openMention", () => {
  it.each([
    ["agent", "/org", undefined],
    ["project", "/projects", undefined],
    ["device", "/settings", { state: { settingsPage: "remote-devices" } }],
  ] as const)(
    "Given a %s mention chip, When it is clicked, Then the desktop routes to its own page",
    (kind, path, options) => {
      const { result } = renderHook(() => useDesktopTranscriptPorts());

      result.current.openMention?.({ kind, refId: 1, label: "x" });

      expect(navigate).toHaveBeenCalledWith(
        path,
        ...(options ? [options] : []),
      );
    },
  );
});
