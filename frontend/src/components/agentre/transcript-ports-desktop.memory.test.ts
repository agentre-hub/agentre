import { beforeEach, describe, expect, it, vi } from "vitest";

const app = vi.hoisted(() => ({
  MemoryGet: vi.fn(),
  MemoryUndoWrite: vi.fn(),
  MemoryUpdate: vi.fn(),
}));

vi.mock("../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
  ...app,
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import { desktopTranscriptPorts } from "./transcript-ports-desktop";

const memory = () => {
  const port = desktopTranscriptPorts.memory;
  if (!port) throw new Error("desktop host must provide the memory port");
  return port;
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe("desktop transcript memory port", () => {
  it("get projects the Wails item into the shared entry view", async () => {
    app.MemoryGet.mockResolvedValue({
      id: 7,
      summary: "Run lint",
      detail: "d",
      pinned: false,
      state: "archived",
      hasPrevious: true,
      creator: { kind: "ai", agentId: 5, sessionId: 70 },
      createtime: 1,
      updatetime: 2,
    });
    const view = await memory().get(7);
    expect(app.MemoryGet).toHaveBeenCalledWith(7);
    expect(view).toMatchObject({
      id: 7,
      summary: "Run lint",
      detail: "d",
      archived: true,
      hasPrevious: true,
    });
  });

  it("get returns null when the entry no longer exists", async () => {
    app.MemoryGet.mockResolvedValue(null);
    expect(await memory().get(7)).toBeNull();
  });

  it("undoWrite sends the id, session and action and relays the status", async () => {
    app.MemoryUndoWrite.mockResolvedValue({ status: "changed" });
    const result = await memory().undoWrite({
      id: 7,
      sessionId: 70,
      action: "updated",
    });
    expect(app.MemoryUndoWrite).toHaveBeenCalledWith(
      expect.objectContaining({ id: 7, sessionId: 70, action: "updated" }),
    );
    expect(result).toEqual({ status: "changed" });
  });

  it("undoWrite turns a coded error into a failure with the backend's sentence", async () => {
    app.MemoryUndoWrite.mockRejectedValue(
      new Error("agentre-code:21100 记忆不存在"),
    );
    const result = await memory().undoWrite({
      id: 7,
      sessionId: 70,
      action: "created",
    });
    expect(result.status).toBe("failed");
  });

  it("save updates through the user-side binding", async () => {
    app.MemoryUpdate.mockResolvedValue({});
    const result = await memory().save(7, { summary: "s", detail: "d" });
    expect(app.MemoryUpdate).toHaveBeenCalledWith({
      id: 7,
      summary: "s",
      detail: "d",
    });
    expect(result).toEqual({ ok: true });
  });

  it("save reports a failure without throwing", async () => {
    app.MemoryUpdate.mockRejectedValue(new Error("boom"));
    expect(await memory().save(7, { summary: "s", detail: "" })).toEqual({
      ok: false,
    });
  });
});
