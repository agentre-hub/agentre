import { renderHook, act } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  installClipboard,
  installCopyCommandModel,
  removeClipboard,
  restoreClipboardEnv,
} from "../../lib/__testing__/clipboard";
import type { PanelFlash } from "./use-provider-catalog";
import type { Provider } from ".";
import { useProviderActions } from "./use-provider-actions";

/**
 * 「复制 providerKey」这一手。
 *
 * 面板自己有就地反馈（flash 那一行），所以复制走的是不带 toast 的那一层；
 * 但**能不能复制成**不该由这里各判一遍——非安全上下文（宿主部署在
 * `http://<局域网 IP>:port` 上）没有 Clipboard API，共享的复制层有 execCommand
 * 兜底，直接摸 `navigator.clipboard` 会既复制不成、又把「复制失败」说成定局。
 */
describe("useProviderActions · handleCopyProviderKey", () => {
  afterEach(() => {
    restoreClipboardEnv();
  });

  function mountActions() {
    const setFlash = vi.fn<(flash: PanelFlash) => void>();
    const { result } = renderHook(() =>
      useProviderActions({
        bridge: {
          SetLLMModelEnabled: vi.fn(),
          SetLLMProviderEnabled: vi.fn(),
          TestLLMProvider: vi.fn(),
        } as unknown as Parameters<typeof useProviderActions>[0]["bridge"],
        selectedProvider: null,
        setFlash,
        refreshProviders: vi.fn().mockResolvedValue(undefined),
        refreshModels: vi.fn().mockResolvedValue(undefined),
      }),
    );
    return { result, setFlash };
  }

  const provider = { providerKey: "prov-key-42" } as Provider;

  it("Given a writable clipboard, When the provider key is copied, Then the flash says it landed", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    installClipboard(writeText);
    const { result, setFlash } = mountActions();

    await act(async () => {
      await result.current.handleCopyProviderKey(provider);
    });

    expect(writeText).toHaveBeenCalledWith("prov-key-42");
    expect(setFlash).toHaveBeenCalledWith({
      kind: "ok",
      text: "Provider Key copied",
    });
  });

  it("Given no Clipboard API, When the provider key is copied, Then the shared fallback copies it and the flash still says it landed", async () => {
    removeClipboard();
    const selectedAtCopy = installCopyCommandModel();
    const { result, setFlash } = mountActions();

    await act(async () => {
      await result.current.handleCopyProviderKey(provider);
    });

    expect(selectedAtCopy).toEqual(["prov-key-42"]);
    expect(setFlash).toHaveBeenCalledWith({
      kind: "ok",
      text: "Provider Key copied",
    });
  });

  it("Given no copy channel at all, When the provider key is copied, Then the flash says it failed instead of claiming success", async () => {
    removeClipboard();
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: vi.fn().mockReturnValue(false),
    });
    const { result, setFlash } = mountActions();

    await act(async () => {
      await result.current.handleCopyProviderKey(provider);
    });

    expect(setFlash).toHaveBeenCalledWith({
      kind: "err",
      text: "Failed to copy Provider Key",
    });
  });
});
