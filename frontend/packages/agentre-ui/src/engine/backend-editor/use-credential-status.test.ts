import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useBackendCredentialStatus } from "./use-credential-status";

describe("useBackendCredentialStatus", () => {
  it("Given an OpenClaw backend with a saved sync_id, When the gate is clear, Then it queries once by type/syncId/deviceId", async () => {
    const backendCredentialStatus = vi.fn().mockResolvedValue({
      openClawTokenSaved: true,
      hermesLoggedIn: false,
    });

    const { result } = renderHook(() =>
      useBackendCredentialStatus({
        type: "openclaw",
        syncId: "sync-1",
        hermesQueryUrl: "",
        deviceId: "fp-a",
        gate: "",
        backendCredentialStatus,
      }),
    );

    await waitFor(() => expect(result.current).not.toBeNull());
    expect(backendCredentialStatus).toHaveBeenCalledTimes(1);
    expect(backendCredentialStatus).toHaveBeenCalledWith({
      type: "openclaw",
      syncId: "sync-1",
      hermesUrl: undefined,
      deviceId: "fp-a",
    });
    expect(result.current).toEqual({
      openClawTokenSaved: true,
      hermesLoggedIn: false,
    });
  });

  it("Given a gated device, When rendering, Then it sends no request", () => {
    const backendCredentialStatus = vi.fn().mockResolvedValue({
      openClawTokenSaved: true,
      hermesLoggedIn: false,
    });

    const { result } = renderHook(() =>
      useBackendCredentialStatus({
        type: "openclaw",
        syncId: "sync-1",
        hermesQueryUrl: "",
        deviceId: "fp-a",
        gate: "deviceOffline",
        backendCredentialStatus,
      }),
    );

    expect(backendCredentialStatus).not.toHaveBeenCalled();
    expect(result.current).toBeNull();
  });

  it("Given a claudecode backend (no credential concept), When rendering, Then it sends no request", () => {
    const backendCredentialStatus = vi.fn().mockResolvedValue({
      openClawTokenSaved: true,
      hermesLoggedIn: false,
    });

    renderHook(() =>
      useBackendCredentialStatus({
        type: "claudecode",
        syncId: "sync-1",
        hermesQueryUrl: "",
        deviceId: "fp-a",
        gate: "",
        backendCredentialStatus,
      }),
    );

    expect(backendCredentialStatus).not.toHaveBeenCalled();
  });

  it("Given the bound device changes, When it re-renders with a new deviceId, Then it queries again", async () => {
    const backendCredentialStatus = vi
      .fn()
      .mockResolvedValueOnce({
        openClawTokenSaved: true,
        hermesLoggedIn: false,
      })
      .mockResolvedValueOnce({
        openClawTokenSaved: false,
        hermesLoggedIn: false,
      });

    const { result, rerender } = renderHook(
      (props: { deviceId: string }) =>
        useBackendCredentialStatus({
          type: "openclaw",
          syncId: "sync-1",
          hermesQueryUrl: "",
          deviceId: props.deviceId,
          gate: "",
          backendCredentialStatus,
        }),
      { initialProps: { deviceId: "fp-a" } },
    );

    await waitFor(() =>
      expect(result.current).toEqual({
        openClawTokenSaved: true,
        hermesLoggedIn: false,
      }),
    );

    rerender({ deviceId: "fp-b" });

    await waitFor(() =>
      expect(result.current).toEqual({
        openClawTokenSaved: false,
        hermesLoggedIn: false,
      }),
    );
    expect(backendCredentialStatus).toHaveBeenCalledTimes(2);
  });

  it("Given the port rejects (device unreachable), When it resolves, Then the status stays null rather than throwing", async () => {
    const backendCredentialStatus = vi
      .fn()
      .mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() =>
      useBackendCredentialStatus({
        type: "hermes",
        syncId: "",
        hermesQueryUrl: "http://10.0.0.8:9119",
        deviceId: "fp-a",
        gate: "",
        backendCredentialStatus,
      }),
    );

    await waitFor(() =>
      expect(backendCredentialStatus).toHaveBeenCalledTimes(1),
    );
    expect(result.current).toBeNull();
  });
});
