import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SessionIndexEmpty } from "./session-index-empty";

describe("SessionIndexEmpty", () => {
  it("Given a filter and search are both active, When no rows remain, Then the filter reason wins and only its restore action is offered", async () => {
    const user = userEvent.setup();
    const onShowAll = vi.fn();
    const onClearSearch = vi.fn();
    render(
      <SessionIndexEmpty
        filter="running"
        searching
        total={4}
        onShowAll={onShowAll}
        onClearSearch={onClearSearch}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent("No running sessions");
    expect(
      screen.getByText("4 sessions are outside this filter."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Clear search" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "View all sessions" }));
    expect(onShowAll).toHaveBeenCalledTimes(1);
    expect(onClearSearch).not.toHaveBeenCalled();
  });

  it("Given an unread filter and an unknown account total, When a restore port exists, Then it omits a made-up count but still offers All", () => {
    render(
      <SessionIndexEmpty
        filter="unread"
        total={undefined}
        onShowAll={vi.fn()}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent("No unread sessions");
    expect(screen.queryByText(/sessions are outside/)).toBeNull();
    expect(
      screen.getByRole("button", { name: "View all sessions" }),
    ).toBeTruthy();
  });

  it("Given a filter and a zero account total, When no rows remain, Then it does not offer a route to another empty view", () => {
    render(
      <SessionIndexEmpty filter="running" total={0} onShowAll={vi.fn()} />,
    );

    expect(screen.getByRole("status")).toHaveTextContent("No running sessions");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("Given a positive total but no restore port, When filter results are empty, Then no dead action is rendered", () => {
    render(<SessionIndexEmpty filter="unread" total={2} />);

    expect(
      screen.getByText("2 sessions are outside this filter."),
    ).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("Given only search is active, When no rows match, Then it explains the search and clears it through the supplied port", async () => {
    const user = userEvent.setup();
    const onClearSearch = vi.fn();
    render(
      <SessionIndexEmpty
        filter="all"
        searching
        onClearSearch={onClearSearch}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent(
      "No matching sessions",
    );
    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(onClearSearch).toHaveBeenCalledTimes(1);
  });

  it("Given true account emptiness and optional callbacks, When no filter or search is active, Then it shows no invalid recovery action", () => {
    render(
      <SessionIndexEmpty
        filter="all"
        onShowAll={vi.fn()}
        onClearSearch={vi.fn()}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent(
      "No conversations yet",
    );
    expect(screen.queryByRole("button")).toBeNull();
  });
});
