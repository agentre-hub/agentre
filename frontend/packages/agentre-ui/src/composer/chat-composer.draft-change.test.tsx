import "@testing-library/jest-dom/vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Editor } from "@tiptap/react";

import {
  ChatComposer,
  type ChatComposerHandle,
  type ChatImageAttachment,
} from "./chat-composer";

type Draft = { text: string; images: ChatImageAttachment[] };

function setup(props: Record<string, unknown> = {}) {
  const onDraftChange = vi.fn<(draft: Draft) => void>();
  const onSubmit = vi.fn();
  const handle = createRef<ChatComposerHandle>();
  const editorRef = createRef<Editor | null>();
  render(
    <ChatComposer
      ref={handle}
      editorRef={editorRef}
      supportsImageInput
      onSubmit={onSubmit}
      onDraftChange={onDraftChange}
      {...props}
    />,
  );
  const last = (): Draft | undefined => onDraftChange.mock.lastCall?.[0];
  return { onDraftChange, onSubmit, handle, editorRef, last };
}

function imageInput(): HTMLElement {
  return screen.getByLabelText("Add image", { selector: "input" });
}
function png(name: string, bytes = [1, 2, 3]): File {
  return new File([new Uint8Array(bytes)], name, { type: "image/png" });
}
async function attach(name: string, bytes?: number[]) {
  fireEvent.change(imageInput(), { target: { files: [png(name, bytes)] } });
  await screen.findByAltText(name);
}
function sendButton(): HTMLElement {
  return screen.getByRole("button", { name: /Send|Save|Run command|Sending/ });
}

describe("ChatComposer · onDraftChange", () => {
  it("Given typed text, When the editor changes, Then the host gets the text exactly as submit would serialize it", async () => {
    const { editorRef, last, onSubmit } = setup();
    act(() => {
      editorRef.current?.commands.insertContent("line one");
    });
    await waitFor(() => expect(last()?.text).toBe("line one"));
    expect(last()?.images).toEqual([]);

    act(() => {
      editorRef.current?.commands.insertContent({
        type: "mention",
        attrs: { kind: "agent", refId: 7, label: "Bob", path: "" },
      });
    });
    await waitFor(() => expect(last()?.text).toContain("Bob"));
    const draftText = last()!.text;

    fireEvent.click(sendButton());
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    // submit 把首尾空白 trim 掉；草稿是同一段序列化、不 trim（还在敲的空格不能丢）
    expect(onSubmit.mock.calls[0][0].text).toBe(draftText.trim());
  });

  it("Given a trailing space is typed, Then the draft keeps it", async () => {
    const { editorRef, last } = setup();
    act(() => {
      editorRef.current?.commands.insertContent("hello ");
    });
    await waitFor(() => expect(last()?.text).toBe("hello "));
  });

  it("Given attachments, When added, removed and the last one removed, Then each change reports the same images submit would send", async () => {
    const { last, onSubmit } = setup();
    await attach("a.png", [1, 2, 3]);
    await waitFor(() => expect(last()?.images).toHaveLength(1));
    await attach("b.png", [9, 8, 7, 6]);
    await waitFor(() => expect(last()?.images).toHaveLength(2));
    const reported = last()!.images;

    fireEvent.click(sendButton());
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0].images).toEqual(reported);
  });

  it("Given two images, When removed one by one, Then the draft shrinks to empty", async () => {
    const { last } = setup();
    await attach("a.png");
    await attach("b.png", [4, 5]);
    await waitFor(() => expect(last()?.images).toHaveLength(2));
    fireEvent.click(screen.getAllByRole("button", { name: /remove/i })[0]);
    await waitFor(() =>
      expect(last()?.images.map((i) => i.name)).toEqual(["b.png"]),
    );
    fireEvent.click(screen.getByRole("button", { name: /remove/i }));
    await waitFor(() => expect(last()?.images).toEqual([]));
  });

  it("Given a draft, When the host clears it, Then an empty draft is reported", async () => {
    const { last, handle } = setup();
    await attach("a.png");
    await waitFor(() => expect(last()?.images).toHaveLength(1));
    act(() => handle.current?.clearDraft());
    await waitFor(() => expect(last()).toEqual({ text: "", images: [] }));
  });

  it("Given text and an image, When submitted, Then onSubmit comes before a final empty draft and the sent draft is never re-reported", async () => {
    const { editorRef, last, onSubmit, onDraftChange } = setup();
    await attach("a.png");
    act(() => {
      editorRef.current?.commands.insertContent("send me");
    });
    await waitFor(() => expect(last()?.text).toBe("send me"));
    const callsBefore = onDraftChange.mock.calls.length;

    fireEvent.click(sendButton());
    await waitFor(() => expect(last()).toEqual({ text: "", images: [] }));

    const submitOrder = onSubmit.mock.invocationCallOrder[0];
    const after = onDraftChange.mock.invocationCallOrder.filter(
      (_, i) => i >= callsBefore,
    );
    expect(after.every((o) => o > submitOrder)).toBe(true);
    for (const [draft] of onDraftChange.mock.calls.slice(callsBefore)) {
      expect(draft).toEqual({ text: "", images: [] });
    }
  });

  it("Given the last reported draft, When restored, Then text and images round-trip", async () => {
    const first = setup();
    await attach("a.png", [1, 2, 3, 4]);
    act(() => {
      first.editorRef.current?.commands.insertContent("keep me ");
    });
    await waitFor(() => expect(first.last()?.text).toBe("keep me "));
    await waitFor(() => expect(first.last()?.images).toHaveLength(1));
    const saved = first.last()!;
    document.body.innerHTML = "";

    const second = setup();
    act(() => second.handle.current?.restoreDraft(saved.text, saved.images));
    await screen.findByAltText("a.png");
    await waitFor(() => expect(second.last()).toEqual(saved));
  });

  it("Given no onDraftChange, When editing and attaching, Then nothing throws and submit is unchanged", async () => {
    const onSubmit = vi.fn();
    const editorRef = createRef<Editor | null>();
    render(
      <ChatComposer
        editorRef={editorRef}
        supportsImageInput
        onSubmit={onSubmit}
      />,
    );
    await attach("a.png");
    act(() => {
      editorRef.current?.commands.insertContent("hi");
    });
    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        images: [expect.objectContaining({ name: "a.png" })],
        text: "hi",
      }),
    );
  });

  it("Given edit mode, When the edited message loads, Then it is not reported as a draft", async () => {
    const { onDraftChange } = setup({
      editing: true,
      editDraft: "old message",
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(onDraftChange).not.toHaveBeenCalled();
  });
});
