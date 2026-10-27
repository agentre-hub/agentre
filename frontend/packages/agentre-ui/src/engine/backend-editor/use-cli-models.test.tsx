import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useCLIModels } from "./use-cli-models";
import type { CliModelCatalog } from "../model-target-picker/cli-models";

describe("useCLIModels", () => {
  it("Given a saved backend, When loading then refreshing after a query error, Then it sends refresh and replaces the error with CLI results", async () => {
    const listCLIModels = vi
      .fn()
      .mockResolvedValueOnce({ models: [], error: "unsupported" })
      .mockResolvedValueOnce({ models: [], effectiveModelId: "applied-id" });
    const { result } = renderHook(() =>
      useCLIModels({ backendId: 7, enabled: true, listCLIModels }),
    );
    await waitFor(() =>
      expect(result.current.catalog.error).toBe("unsupported"),
    );
    expect(listCLIModels).toHaveBeenCalledWith({
      backendId: 7,
      refresh: false,
    });
    act(() => result.current.refresh());
    await waitFor(() =>
      expect(result.current.catalog.effectiveModelId).toBe("applied-id"),
    );
    expect(listCLIModels).toHaveBeenLastCalledWith({
      backendId: 7,
      refresh: true,
    });
    expect(result.current.catalog.error).toBeUndefined();
  });
  it("Given an in-flight catalog, When backend changes, Then a late result cannot overwrite the new backend", async () => {
    let resolveOld!: (catalog: CliModelCatalog) => void;
    const listCLIModels = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<CliModelCatalog>((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValue({ models: [], effectiveModelId: "new-id" });
    const { result, rerender } = renderHook(
      ({ backendId }) =>
        useCLIModels({ backendId, enabled: true, listCLIModels }),
      { initialProps: { backendId: 7 } },
    );
    rerender({ backendId: 8 });
    await waitFor(() =>
      expect(result.current.catalog.effectiveModelId).toBe("new-id"),
    );
    await act(async () =>
      resolveOld({ models: [], effectiveModelId: "old-id" }),
    );
    expect(result.current.catalog.effectiveModelId).toBe("new-id");
  });
  it("Given unsaved CLI configuration changes, Then it clears the old catalog and does not query a saved configuration as if it were the draft", async () => {
    const listCLIModels = vi
      .fn()
      .mockResolvedValue({ models: [], effectiveModelId: "old-id" });
    const { result, rerender } = renderHook(
      ({ configurationChanged }) =>
        useCLIModels({
          backendId: 7,
          enabled: true,
          listCLIModels,
          configurationChanged,
        }),
      { initialProps: { configurationChanged: false } },
    );
    await waitFor(() =>
      expect(result.current.catalog.effectiveModelId).toBe("old-id"),
    );
    rerender({ configurationChanged: true });
    expect(result.current.catalog.effectiveModelId).toBeUndefined();
    expect(result.current.catalog.error).toMatch(/Save configuration changes/);
    act(() => result.current.refresh());
    expect(listCLIModels).toHaveBeenCalledTimes(1);
  });
});
