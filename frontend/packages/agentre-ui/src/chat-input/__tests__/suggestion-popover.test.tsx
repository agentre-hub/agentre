import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SuggestionPopover } from "../suggestion-popover";

describe("SuggestionPopover viewport anchoring", () => {
  it("Given the composer is inside an offset clipping container, When suggestions open, Then the floating layer stays in the viewport coordinate space", () => {
    render(
      <div data-testid="composer-container" className="overflow-hidden">
        <SuggestionPopover
          open
          anchorRect={{ left: 67, top: 404, bottom: 424 }}
          selectedIndex={0}
          itemCount={1}
          ariaLabel="Suggestions"
        >
          {(activeRef) => (
            <button ref={activeRef} role="option" type="button">
              Suggestion
            </button>
          )}
        </SuggestionPopover>
      </div>,
    );

    const listbox = screen.getByRole("listbox", { name: "Suggestions" });
    expect(listbox.parentElement).toBe(document.body);
    expect(listbox).toHaveStyle({
      position: "fixed",
      left: "67px",
      bottom: `${window.innerHeight - 404 + 4}px`,
    });
  });
});

describe("SuggestionPopover outside dismissal", () => {
  const anchorRect = { left: 67, top: 404, bottom: 424 };

  function renderOpen({
    onDismiss,
    editorElement,
  }: {
    onDismiss: () => void;
    editorElement?: HTMLElement | null;
  }) {
    render(
      <SuggestionPopover
        open
        anchorRect={anchorRect}
        selectedIndex={0}
        itemCount={1}
        ariaLabel="Suggestions"
        editorElement={editorElement ?? null}
        onDismiss={onDismiss}
      >
        {(activeRef) => (
          <button ref={activeRef} role="option" type="button">
            Suggestion
          </button>
        )}
      </SuggestionPopover>,
    );
  }

  it("Given the popover is open, When the user pointer-downs outside it and outside the editor, Then onDismiss is called", () => {
    const onDismiss = vi.fn();
    const editor = document.createElement("div");
    const outside = document.createElement("button");
    document.body.append(editor, outside);

    try {
      renderOpen({ onDismiss, editorElement: editor });

      fireEvent.pointerDown(outside);

      expect(onDismiss).toHaveBeenCalledTimes(1);
    } finally {
      editor.remove();
      outside.remove();
    }
  });

  it("Given the popover is open, When the user pointer-downs an option inside it, Then onDismiss is not called", () => {
    const onDismiss = vi.fn();

    renderOpen({ onDismiss });

    fireEvent.pointerDown(screen.getByRole("option"));

    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("Given the popover is open, When the user pointer-downs inside the editor, Then onDismiss is not called", () => {
    const onDismiss = vi.fn();
    const editor = document.createElement("div");
    document.body.append(editor);

    try {
      renderOpen({ onDismiss, editorElement: editor });

      fireEvent.pointerDown(editor);

      expect(onDismiss).not.toHaveBeenCalled();
    } finally {
      editor.remove();
    }
  });
});
