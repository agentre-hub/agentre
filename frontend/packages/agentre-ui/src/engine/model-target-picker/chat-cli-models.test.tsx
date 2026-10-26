import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ModelTargetPicker } from "./index";
import { resolveCLIPillState } from "./cli-models";
import { ProviderPillTrigger } from "./provider-pill-trigger";

const catalog = {
  models: [{ value: "sonnet", label: "Sonnet", modelId: "claude-sonnet" }],
  effectiveModelId: "catalog-default-not-session",
};

describe("chat CLI model choice", () => {
  it("Given an unresolved native session, When rendering follow Agent, Then no catalog default or login placeholder is appended", () => {
    render(
      <ProviderPillTrigger
        state={resolveCLIPillState({ catalog, backendType: "claudecode" })}
      />,
    );
    expect(
      screen.getByText("Follow agent binding", { exact: true }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/CLI login state/)).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("provider-pill-dynamic-icon"),
    ).not.toBeInTheDocument();
  });
  it("Given a chat, When opening the picker, Then follow Agent precedes the CLI section and a rejection keeps the picker open", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn().mockResolvedValue(false);
    render(
      <ModelTargetPicker
        scenario="chat"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[]}
        cliModels={{ ...catalog, onChange, onRefresh: vi.fn() }}
      />,
    );
    await user.click(screen.getByRole("button"));
    expect(screen.getAllByRole("listbox")).toHaveLength(1);
    const rows = screen.getAllByRole("option");
    expect(rows[0]).toHaveTextContent("Follow agent binding");
    await user.click(screen.getByRole("option", { name: /Sonnet/ }));
    expect(onChange).toHaveBeenCalledWith({ mode: "fixed", value: "sonnet" });
    expect(screen.getByTestId("model-target-popover")).toBeInTheDocument();
  });
  it("Given a session report different from catalog default, When rendering CLI default, Then the face uses only the session report and shows follow-default", () => {
    const state = resolveCLIPillState({
      catalog,
      choice: { mode: "cli-default" },
      backendType: "claudecode",
      actualModelId: "own-session-model",
    });
    render(<ProviderPillTrigger state={state} />);
    expect(screen.getByText("own-session-model")).toBeInTheDocument();
    expect(
      screen.getByTestId("provider-pill-dynamic-icon"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("catalog-default-not-session"),
    ).not.toBeInTheDocument();
    expect(
      resolveCLIPillState({
        catalog,
        choice: { mode: "cli-default" },
        backendType: "claudecode",
      }).modelId,
    ).toBe("");
  });
  it("Given a running turn, When selecting a new model, Then the pill shows A → B until the next turn applies it", () => {
    const state = resolveCLIPillState({
      catalog,
      choice: { mode: "fixed", value: "sonnet" },
      backendType: "claudecode",
      actualModelId: "old-model",
      pending: true,
    });
    render(<ProviderPillTrigger state={state} />);
    expect(screen.getByText(/old-model → Sonnet/)).toBeInTheDocument();
  });
});
