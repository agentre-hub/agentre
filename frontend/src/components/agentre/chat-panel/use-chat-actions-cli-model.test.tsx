import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useChatActions } from "./use-chat-actions";
const send = vi.hoisted(() => vi.fn());
vi.mock("../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  SendChatMessage: send,
}));
it("Given a CLI model selected before the first message, When sending, Then only the new-session request carries the choice", async () => {
  send.mockResolvedValue({ sessionId: 42 });
  const { result } = renderHook(() =>
    useChatActions({
      sessionId: 0,
      session: null,
      setMessages: vi.fn(),
      reloadSession: vi.fn(),
      openStream: vi.fn(),
      followTranscriptBottom: vi.fn(),
      composerRef: { current: null },
      setSendInFlight: vi.fn(),
      setNotice: vi.fn(),
      streaming: false,
      activeBackendType: "claudecode",
      canSteer: true,
      isModeSwitchable: false,
      permissionModeValue: "",
      supportsImageInput: true,
      supportsCompactRPC: true,
      execTargetOverride: null,
      effectiveTarget: null,
      providerKey: "",
      modelKey: "",
      cliModelMode: "fixed",
      cliModel: "sonnet",
      reasoningEffort: "",
      recordDraftTarget: vi.fn(),
      editing: false,
      confirmEdit: vi.fn(),
    }),
  );
  await act(async () => {
    await result.current.doSend(0, 7, { text: "hello" });
  });
  expect(send.mock.calls[0][0]).toMatchObject({
    cliModelMode: "fixed",
    cliModel: "sonnet",
  });
  await act(async () => {
    await result.current.doSend(42, 7, { text: "next" });
  });
  expect(send.mock.calls[1][0].cliModelMode).toBeUndefined();
});
