import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

import { Popover, PopoverAnchor } from "../ui/popover";
import {
  SessionGroupOverflow,
  type SessionGroupOverflowPage,
} from "./session-group-overflow";

type Row = { id: string; label: string };

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
};

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function page(
  rows: Row[],
  total = rows.length,
  nextCursor: string | null = null,
): SessionGroupOverflowPage<Row> {
  return { rows, total, nextCursor };
}

function scrollToBottom(element: HTMLElement): void {
  Object.defineProperty(element, "scrollHeight", {
    configurable: true,
    value: 1000,
  });
  Object.defineProperty(element, "clientHeight", {
    configurable: true,
    value: 400,
  });
  element.scrollTop = 600;
  fireEvent.scroll(element);
}

function Overflow({
  loadPage,
  onClose = () => {},
  avatar,
}: {
  loadPage: (cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>;
  onClose?: () => void;
  avatar?: React.ReactNode;
}) {
  return (
    <Popover open>
      <PopoverAnchor />
      <SessionGroupOverflow
        title="Engineering"
        avatar={avatar}
        loadPage={loadPage}
        getRowKey={(row) => row.id}
        renderRow={(row, close) => (
          <button type="button" onClick={close}>
            {row.label}
          </button>
        )}
        onClose={onClose}
      />
    </Popover>
  );
}

describe("SessionGroupOverflow shared state machine", () => {
  it("Given the overflow mounts, When its first opaque page is pending then resolves empty, Then it loads null once, shows a stable skeleton, empty state, and a 0 / 0 footer", async () => {
    const first = deferred<SessionGroupOverflowPage<Row>>();
    const loadPage = vi.fn(() => first.promise);

    render(<Overflow loadPage={loadPage} />);

    expect(loadPage).toHaveBeenCalledTimes(1);
    expect(loadPage).toHaveBeenCalledWith(null);
    expect(screen.getByTestId("session-group-overflow-list")).toHaveAttribute(
      "aria-busy",
      "true",
    );
    expect(
      document.querySelector('[data-slot="session-row-skeleton"]'),
    ).not.toBeNull();

    first.resolve(page([], 0));

    expect(await screen.findByText("No sessions")).toBeInTheDocument();
    expect(screen.getByText("Loaded 0 / 0")).toBeInTheDocument();
    expect(
      document.querySelector('[data-slot="session-row-skeleton"]'),
    ).toBeNull();
  });

  it("Given a page returns an opaque cursor, When Load more is pressed, Then that cursor is passed verbatim and rows append while the footer tracks loaded / total", async () => {
    const loadPage = vi
      .fn<(cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>>()
      .mockResolvedValueOnce(
        page([{ id: "a", label: "alpha" }], 2, "opaque::next/二"),
      )
      .mockResolvedValueOnce(page([{ id: "b", label: "beta" }], 2));
    const user = userEvent.setup();

    render(<Overflow loadPage={loadPage} />);
    expect(await screen.findByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("Loaded 1 / 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("beta")).toBeInTheDocument();
    expect(loadPage).toHaveBeenLastCalledWith("opaque::next/二");
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("Loaded 2 / 2")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("Given explicit and near-bottom loading share one in-flight action, When both fire together, Then the page is fetched once and auto-loading advances through the same cursor path", async () => {
    const second = deferred<SessionGroupOverflowPage<Row>>();
    const loadPage = vi
      .fn<(cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>>()
      .mockResolvedValueOnce(page([{ id: "a", label: "alpha" }], 3, "page-two"))
      .mockImplementationOnce(() => second.promise)
      .mockResolvedValueOnce(page([{ id: "c", label: "gamma" }], 3));
    const user = userEvent.setup();

    render(<Overflow loadPage={loadPage} />);
    await screen.findByText("alpha");

    await user.click(screen.getByRole("button", { name: "Load more" }));
    const list = screen.getByTestId("session-group-overflow-list");
    scrollToBottom(list);
    scrollToBottom(list);
    expect(loadPage).toHaveBeenCalledTimes(2);
    expect(loadPage).toHaveBeenLastCalledWith("page-two");

    second.resolve(page([{ id: "b", label: "beta" }], 3, "page-three"));
    expect(await screen.findByText("beta")).toBeInTheDocument();

    scrollToBottom(list);
    expect(await screen.findByText("gamma")).toBeInTheDocument();
    expect(loadPage).toHaveBeenLastCalledWith("page-three");
  });

  it("Given an appended page fails, When retry is offered, Then existing rows remain, raw errors stay hidden, and retry resumes from the failed cursor", async () => {
    const loadPage = vi
      .fn<(cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>>()
      .mockResolvedValueOnce(
        page([{ id: "a", label: "alpha" }], 2, "resume-here"),
      )
      .mockRejectedValueOnce(new Error("secret transport detail"))
      .mockResolvedValueOnce(page([{ id: "b", label: "beta" }], 2));
    const user = userEvent.setup();

    render(<Overflow loadPage={loadPage} />);
    await screen.findByText("alpha");
    await user.click(screen.getByRole("button", { name: "Load more" }));

    expect(
      await screen.findByText("Could not load sessions"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/secret transport detail/)).toBeNull();
    expect(screen.getByText("alpha")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("beta")).toBeInTheDocument();
    expect(loadPage).toHaveBeenLastCalledWith("resume-here");
  });

  it("Given the first page fails, When Retry is pressed, Then the generic recovery restarts at the null cursor", async () => {
    const loadPage = vi
      .fn<(cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>>()
      .mockRejectedValueOnce("private backend failure")
      .mockResolvedValueOnce(page([{ id: "a", label: "alpha" }]));
    const user = userEvent.setup();

    render(<Overflow loadPage={loadPage} />);
    expect(
      await screen.findByText("Could not load sessions"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/private backend failure/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("alpha")).toBeInTheDocument();
    expect(loadPage).toHaveBeenNthCalledWith(2, null);
  });

  it("Given an overflow closes after paging, When it is mounted again, Then it starts from a clean first page instead of retaining rows or cursor", async () => {
    const reopened = deferred<SessionGroupOverflowPage<Row>>();
    const loadPage = vi
      .fn<(cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>>()
      .mockResolvedValueOnce(page([{ id: "a", label: "alpha" }], 2, "next"))
      .mockResolvedValueOnce(page([{ id: "b", label: "beta" }], 2))
      .mockImplementationOnce(() => reopened.promise);
    const user = userEvent.setup();

    function Harness() {
      const [open, setOpen] = React.useState(true);
      return (
        <>
          <button type="button" onClick={() => setOpen((value) => !value)}>
            toggle
          </button>
          {open ? <Overflow loadPage={loadPage} /> : null}
        </>
      );
    }

    render(<Harness />);
    await screen.findByText("alpha");
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByText("beta");
    await user.click(screen.getByRole("button", { name: "toggle" }));
    await user.click(screen.getByRole("button", { name: "toggle" }));

    expect(loadPage).toHaveBeenLastCalledWith(null);
    expect(screen.queryByText("alpha")).toBeNull();
    expect(screen.queryByText("beta")).toBeNull();
    expect(
      document.querySelector('[data-slot="session-row-skeleton"]'),
    ).not.toBeNull();

    reopened.resolve(page([{ id: "fresh", label: "fresh" }]));
    expect(await screen.findByText("fresh")).toBeInTheDocument();
  });

  it("Given an old first-page request is still pending, When a new loader cycle wins, Then the stale response cannot overwrite current rows", async () => {
    const stale = deferred<SessionGroupOverflowPage<Row>>();
    const current = deferred<SessionGroupOverflowPage<Row>>();
    const oldLoader = vi.fn(() => stale.promise);
    const newLoader = vi.fn(() => current.promise);
    const view = render(<Overflow loadPage={oldLoader} />);

    view.rerender(<Overflow loadPage={newLoader} />);
    current.resolve(page([{ id: "new", label: "current row" }]));
    expect(await screen.findByText("current row")).toBeInTheDocument();

    stale.resolve(page([{ id: "old", label: "stale row" }]));
    await waitFor(() => expect(screen.queryByText("stale row")).toBeNull());
    expect(screen.getByText("current row")).toBeInTheDocument();
  });

  it("Given an optional avatar and host row renderer, When rows render and a row acts, Then the avatar has no reserved fallback and the host receives the unmodified row plus close action", async () => {
    const onClose = vi.fn();
    const loadPage = vi.fn(() =>
      Promise.resolve(
        page([{ id: "device:3/session:42", label: "composite" }]),
      ),
    );
    const user = userEvent.setup();
    const view = render(<Overflow loadPage={loadPage} onClose={onClose} />);

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByTestId("avatar")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "composite" }));
    expect(onClose).toHaveBeenCalledTimes(1);

    view.rerender(
      <Overflow
        loadPage={loadPage}
        onClose={onClose}
        avatar={<span data-testid="avatar">A</span>}
      />,
    );
    expect(await screen.findByTestId("avatar")).toBeInTheDocument();
  });
});
