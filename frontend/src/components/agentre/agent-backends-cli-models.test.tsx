import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ListCliModels } from "@agentre-hub/agentre-ui";

const mocks = vi.hoisted(() => ({
  listCLIModels: vi.fn(),
  sharedProps: {} as { listCLIModels?: ListCliModels },
}));
vi.mock("../../../wailsjs/go/app/App", () => ({
  ListCLIModels: mocks.listCLIModels,
}));
vi.mock("./engine-ports-desktop", () => ({
  createDesktopEngineSettingsPorts: () => ({}),
}));
vi.mock("./use-refresh-signal", () => ({ useRefreshSignal: () => 0 }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsOn: vi.fn() }));
vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agentre-hub/agentre-ui")>()),
  AgentBackendsPanel: (props: { listCLIModels?: ListCliModels }) => {
    mocks.sharedProps = props;
    return null;
  },
}));

import { AgentBackendsPanel } from "./agent-backends";

describe("desktop CLI model adapter", () => {
  it("Given CLI wire fields, When the shared editor queries, Then display names and model identities are mapped without losing groups, legacy or errors", async () => {
    mocks.listCLIModels.mockResolvedValue({
      models: [
        {
          value: "sonnet",
          displayName: "CLI Sonnet",
          modelId: "actual-id",
          group: "account",
          legacy: true,
        },
      ],
      effectiveValue: "sonnet",
      effectiveModelId: "actual-id",
      error: "raw error",
    });
    render(<AgentBackendsPanel />);
    expect(mocks.sharedProps.listCLIModels).toBeTypeOf("function");
    const result = await mocks.sharedProps.listCLIModels!({
      backendId: 7,
      refresh: true,
    });
    expect(mocks.listCLIModels).toHaveBeenCalledWith({
      backendId: 7,
      refresh: true,
    });
    expect(result).toEqual({
      models: [
        {
          value: "sonnet",
          label: "CLI Sonnet",
          modelId: "actual-id",
          group: "account",
          legacy: true,
        },
      ],
      effectiveValue: "sonnet",
      effectiveModelId: "actual-id",
      error: "raw error",
    });
  });
});
