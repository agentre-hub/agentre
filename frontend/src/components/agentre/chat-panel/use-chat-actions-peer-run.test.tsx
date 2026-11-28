import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useChatActions } from "./use-chat-actions";

const peerRunFresh = vi.hoisted(() => vi.fn());
const send = vi.hoisted(() => vi.fn());
vi.mock("../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  PeerRunFresh: peerRunFresh,
  SendChatMessage: send,
}));

function render(
  overrides: Partial<Parameters<typeof useChatActions>[0]> = {},
) {
  return renderHook(() =>
    useChatActions({
      sessionId: 0,
      session: null,
      newSessionContext: { projectId: 9 },
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
      isModeSwitchable: true,
      permissionModeValue: "acceptEdits",
      supportsImageInput: true,
      supportsCompactRPC: true,
      execTargetOverride: null,
      effectiveTarget: {
        kind: "desktop",
        deviceId: "sha256:peer",
        deviceName: "Studio",
        backendType: "claudecode",
        llmProviderKey: "",
        llmModelKey: "",
      },
      providerKey: "prov",
      modelKey: "model",
      cliModelMode: "fixed",
      cliModel: "opus",
      reasoningEffort: "high",
      recordDraftTarget: vi.fn(),
      editing: false,
      confirmEdit: vi.fn(),
      ...overrides,
    }),
  );
}

it("Given a chosen desktop, engine profile and session overrides, When the first message is dispatched, Then PeerRunFresh carries all of them", async () => {
  peerRunFresh.mockResolvedValue({ conversationId: "c1" });
  const { result } = render({ engineProfileSyncId: "profile-sync-31" });
  await act(async () => {
    await result.current.doSend(0, 7, { text: "hello" });
  });
  expect(peerRunFresh).toHaveBeenCalledTimes(1);
  expect(send).not.toHaveBeenCalled();
  expect(peerRunFresh.mock.calls[0][0]).toMatchObject({
    fingerprint: "sha256:peer",
    agentId: 7,
    projectId: 9,
    engineProfileSyncId: "profile-sync-31",
    providerKey: "prov",
    modelKey: "model",
    cliModelMode: "fixed",
    cliModel: "opus",
    reasoningEffort: "high",
    permissionMode: "acceptEdits",
  });
});

it("Given no engine profile was picked, When dispatching to a desktop, Then the request names none so the receiver uses the member's effective profile", async () => {
  peerRunFresh.mockResolvedValue({ conversationId: "c1" });
  const { result } = render({ cliModelMode: "", cliModel: "" });
  await act(async () => {
    await result.current.doSend(0, 7, { text: "hello" });
  });
  const req = peerRunFresh.mock.calls.at(-1)![0];
  expect(req.engineProfileSyncId).toBeUndefined();
  expect(req.cliModelMode).toBeUndefined();
});
