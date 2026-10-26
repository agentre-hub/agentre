import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  ModelTargetPicker,
  ProviderPillTrigger,
  resolveCLIPillState,
} from "@agentre-hub/agentre-ui";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatCLIModel } from "./use-chat-cli-model";

const api = vi.hoisted(() => ({ list: vi.fn(), set: vi.fn() }));
vi.mock("../../../../wailsjs/go/app/App", () => ({
  ListCLIModels: api.list,
  SetChatSessionCLIModel: api.set,
}));

beforeEach(() => {
  vi.clearAllMocks();
  api.list.mockResolvedValue({
    models: [{ value: "sonnet", label: "Sonnet", modelId: "claude-sonnet" }],
    effectiveModelId: "catalog-only",
  });
  api.set.mockResolvedValue({ mode: "fixed", value: "sonnet" });
});
describe("local session CLI models", () => {
  it("Given a session's previous model, When the CLI rejects a switch, Then the popover shows the original rejection and the pill and notice callback stay unchanged", async () => {
    const originalError = "Model 'denied' not found";
    api.list.mockResolvedValue({
      models: [
        { value: "opus", displayName: "Opus", modelId: "claude-opus" },
        { value: "denied", displayName: "Denied model", modelId: "denied" },
      ],
    });
    api.set.mockRejectedValue(new Error(originalError));
    const onChosen = vi.fn();
    const onSwitched = vi.fn();
    function SessionModelPill() {
      const model = useChatCLIModel({
        backendId: 7,
        enabled: true,
        sessionId: 42,
        persistedMode: "fixed",
        persistedValue: "opus",
        onChosen,
        onSwitched,
      });
      return (
        <ModelTargetPicker
          scenario="chat"
          backendType="claudecode"
          selected={null}
          onChange={vi.fn()}
          catalog={[]}
          cliModels={model.cliModels}
          data-testid="session-model-pill"
          triggerLabel={
            <ProviderPillTrigger
              state={resolveCLIPillState({
                catalog: model.catalog,
                choice: model.choice,
                backendType: "claudecode",
                actualModelId: "claude-opus",
              })}
            />
          }
        />
      );
    }
    const user = userEvent.setup();
    render(<SessionModelPill />);
    const pill = screen.getByTestId("session-model-pill");
    await user.click(pill);
    await user.click(
      await screen.findByRole("option", { name: /Denied model/ }),
    );
    const popover = screen.getByTestId("model-target-popover");
    expect(
      await within(popover).findByText(
        `Switch did not take effect: ${originalError}`,
      ),
    ).toBeInTheDocument();
    expect(pill).toHaveTextContent("Opus");
    expect(pill).not.toHaveTextContent("Denied model");
    expect(
      within(popover).getByRole("option", { name: /Opus/ }),
    ).toHaveAttribute("aria-selected", "true");
    expect(onChosen).not.toHaveBeenCalled();
    expect(onSwitched).not.toHaveBeenCalled();
    expect(api.set).toHaveBeenCalledWith({
      sessionId: 42,
      mode: "fixed",
      value: "denied",
    });
  });
  it("Given a saved local native backend, When a draft chooses a CLI model, Then it stays transient and exposes first-send fields, not the catalog default as actual", async () => {
    const clear = vi.fn();
    const { result } = renderHook(() =>
      useChatCLIModel({
        backendId: 7,
        enabled: true,
        sessionId: 0,
        onChosen: clear,
      }),
    );
    await waitFor(() =>
      expect(result.current.cliModels?.models).toHaveLength(1),
    );
    await act(async () => {
      await result.current.cliModels?.onChange({
        mode: "fixed",
        value: "sonnet",
      });
    });
    expect(api.set).not.toHaveBeenCalled();
    expect(result.current.mode).toBe("fixed");
    expect(result.current.value).toBe("sonnet");
    expect(clear).toHaveBeenCalled();
  });
  it("Given an idle session, When its CLI rejects a choice, Then the choice remains unchanged and the original error is returned to the picker", async () => {
    api.set.mockRejectedValue(new Error("Model denied by CLI"));
    const clear = vi.fn();
    const { result } = renderHook(() =>
      useChatCLIModel({
        backendId: 7,
        enabled: true,
        sessionId: 42,
        onChosen: clear,
      }),
    );
    await act(async () => {
      expect(
        await result.current.cliModels?.onChange({
          mode: "fixed",
          value: "bad",
        }),
      ).toBe(false);
    });
    expect(result.current.mode).toBe("");
    expect(clear).not.toHaveBeenCalled();
    expect(result.current.error).toContain("Model denied by CLI");
  });
});
