import { describe, it, expect, vi } from "vitest";
import type { Terminal as XTerminal } from "@xterm/xterm";
import {
  attachXtermRolloverGuard,
  shouldRolloverWrite,
  type XTermRolloverInternals,
} from "./xterm-rollover-guard";

// Builds a partial InputEvent good enough for shouldRolloverWrite. The guard
// only reads inputType/data/isComposing/composed, so we cast a plain object.
function inputEvent(over: Partial<InputEvent>): InputEvent {
  return {
    inputType: "insertText",
    data: "2",
    isComposing: false,
    composed: true,
    ...over,
  } as InputEvent;
}

const rolloverInternals: XTermRolloverInternals = {
  _keyDownSeen: true,
  _keyPressHandled: false,
};

describe("shouldRolloverWrite", () => {
  it("returns true for the key-rollover case xterm silently drops", () => {
    // composed insertText + _keyDownSeen===true (previous key not yet keyup'd)
    // is exactly the path xterm's _inputEvent skips, and keypress did not run.
    expect(shouldRolloverWrite(inputEvent({}), rolloverInternals, false)).toBe(
      true,
    );
  });

  it("returns false when xterm's keypress path already sent the char (avoid double-send)", () => {
    expect(
      shouldRolloverWrite(
        inputEvent({}),
        { _keyDownSeen: true, _keyPressHandled: true },
        false,
      ),
    ).toBe(false);
  });

  it("returns false when xterm itself will handle the input (_keyDownSeen false)", () => {
    expect(
      shouldRolloverWrite(
        inputEvent({}),
        { _keyDownSeen: false, _keyPressHandled: false },
        false,
      ),
    ).toBe(false);
  });

  it("returns false during real IME composition", () => {
    expect(
      shouldRolloverWrite(
        inputEvent({ isComposing: true }),
        rolloverInternals,
        false,
      ),
    ).toBe(false);
  });

  it("returns false for non-composed events", () => {
    expect(
      shouldRolloverWrite(
        inputEvent({ composed: false }),
        rolloverInternals,
        false,
      ),
    ).toBe(false);
  });

  it("returns false for non-insertText input types", () => {
    expect(
      shouldRolloverWrite(
        inputEvent({ inputType: "deleteContentBackward" }),
        rolloverInternals,
        false,
      ),
    ).toBe(false);
  });

  it("returns false when there is no data", () => {
    expect(
      shouldRolloverWrite(inputEvent({ data: null }), rolloverInternals, false),
    ).toBe(false);
  });

  it("returns false (conservative) when xterm internals are unavailable", () => {
    expect(shouldRolloverWrite(inputEvent({}), undefined, false)).toBe(false);
  });

  it("returns false in screen-reader mode (xterm reads the textarea itself)", () => {
    expect(shouldRolloverWrite(inputEvent({}), rolloverInternals, true)).toBe(
      false,
    );
  });
});

// Builds a fake xterm exposing the real textarea + _core internals the guard
// reaches into, so we exercise the actual DOM wiring.
function fakeTerm(internals: XTermRolloverInternals): {
  term: XTerminal;
  textarea: HTMLTextAreaElement;
} {
  const textarea = document.createElement("textarea");
  const term = {
    textarea,
    options: { screenReaderMode: false },
    _core: internals,
  } as unknown as XTerminal;
  return { term, textarea };
}

// composed/isComposing are read-only on Event, so set them through the
// InputEvent constructor's init dict rather than assigning after construction.
function dispatchInput(textarea: HTMLTextAreaElement, fields: InputEventInit) {
  const ev = new InputEvent("input", {
    bubbles: true,
    composed: true,
    inputType: "insertText",
    data: "2",
    isComposing: false,
    ...fields,
  });
  textarea.dispatchEvent(ev);
}

describe("attachXtermRolloverGuard", () => {
  it("re-sends the dropped character on a rollover input event", () => {
    const write = vi.fn();
    const { term, textarea } = fakeTerm({
      _keyDownSeen: true,
      _keyPressHandled: false,
    });
    attachXtermRolloverGuard(term, write);
    dispatchInput(textarea, { data: "2" });
    expect(write).toHaveBeenCalledWith("2");
  });

  it("does not re-send when xterm already handled the keypress", () => {
    const write = vi.fn();
    const { term, textarea } = fakeTerm({
      _keyDownSeen: true,
      _keyPressHandled: true,
    });
    attachXtermRolloverGuard(term, write);
    dispatchInput(textarea, { data: "2" });
    expect(write).not.toHaveBeenCalled();
  });

  it("stops re-sending after dispose", () => {
    const write = vi.fn();
    const { term, textarea } = fakeTerm({
      _keyDownSeen: true,
      _keyPressHandled: false,
    });
    const guard = attachXtermRolloverGuard(term, write);
    guard.dispose();
    dispatchInput(textarea, { data: "2" });
    expect(write).not.toHaveBeenCalled();
  });

  it("is a no-op when the terminal has no textarea", () => {
    const write = vi.fn();
    const term = { options: {} } as unknown as XTerminal;
    expect(() => attachXtermRolloverGuard(term, write).dispose()).not.toThrow();
    expect(write).not.toHaveBeenCalled();
  });
});
