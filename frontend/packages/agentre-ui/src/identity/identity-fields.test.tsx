import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "i18next";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";
import { IdentityFields, type IdentityFieldsProps } from "./identity-fields";

function open(over: Partial<IdentityFieldsProps> = {}) {
  const props: IdentityFieldsProps = {
    kind: "project",
    name: "Atlas",
    description: "Maps",
    mark: {
      iconKey: "",
      color: "agent-1",
      onPickIcon: vi.fn(),
      onPickColor: vi.fn(),
    },
    onNameChange: vi.fn(),
    onDescriptionChange: vi.fn(),
    testIdPrefix: "identity",
    ...over,
  };
  return { ...render(<IdentityFields {...props} />), props };
}

beforeEach(async () => {
  i18n.addResourceBundle(
    "zh-CN",
    AGENTRE_UI_NAMESPACE,
    agentreUiResources["zh-CN"],
  );
  await i18n.changeLanguage("zh-CN");
});
afterEach(async () => {
  await i18n.changeLanguage("en");
});

describe("IdentityFields", () => {
  it("Given a controlled identity When edited Then only changes are reported and blur remains host-owned", async () => {
    const onNameBlur = vi.fn();
    const onDescriptionBlur = vi.fn();
    const { props } = open({ onNameBlur, onDescriptionBlur });
    const name = screen.getByRole("textbox", { name: "名称" });
    const description = screen.getByRole("textbox", { name: "描述" });
    fireEvent.change(name, { target: { value: "Atlas II" } });
    fireEvent.change(description, { target: { value: "New maps" } });
    expect(props.onNameChange).toHaveBeenCalledExactlyOnceWith("Atlas II");
    expect(props.onDescriptionChange).toHaveBeenCalledExactlyOnceWith(
      "New maps",
    );
    expect(onNameBlur).not.toHaveBeenCalled();
    expect(onDescriptionBlur).not.toHaveBeenCalled();
    expect(name).toHaveValue("Atlas");
    expect(description).toHaveValue("Maps");
    fireEvent.blur(name);
    fireEvent.blur(description);
    expect(onNameBlur).toHaveBeenCalledTimes(1);
    expect(onDescriptionBlur).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "更改图标" }));
    fireEvent.click(screen.getByRole("button", { name: "agent-5" }));
    expect(props.mark.onPickColor).toHaveBeenCalledExactlyOnceWith("agent-5");
    fireEvent.click(screen.getByRole("tab", { name: "预设" }));
    fireEvent.click(await screen.findByRole("radio", { name: "终端" }));
    expect(props.mark.onPickIcon).toHaveBeenCalledExactlyOnceWith(
      "preset:project-terminal",
    );
    expect(onNameBlur).toHaveBeenCalledTimes(1);
  });

  it("Given meta and a name error When rendered Then meta stays between the name and description with no separate identity fields", () => {
    const { container } = open({
      meta: <span>Saved</span>,
      nameError: "Taken",
    });
    const name = screen.getByRole("textbox", { name: "名称" });
    const error = screen.getByRole("alert");
    const meta = screen.getByText("Saved");
    const description = screen.getByRole("textbox", { name: "描述" });
    expect(name.nextElementSibling).toBe(error);
    expect(name).toHaveAttribute("aria-describedby", error.id);
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(
      error.compareDocumentPosition(meta) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      meta.compareDocumentPosition(description) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getAllByRole("textbox")).toHaveLength(2);
    expect(screen.getAllByRole("button")).toHaveLength(1);
    expect(container.querySelector("label")).toBeNull();
    for (const label of ["名称", "描述", "头像", "颜色"])
      expect(screen.queryByText(label)).toBeNull();
  });

  it("Given no meta, error or blur handlers When editing Then optional slots leave no empty error and editing is safe", () => {
    open({ autoFocusName: true, testIdPrefix: "draft" });
    const name = screen.getByTestId("draft-name");
    expect(name).toHaveFocus();
    fireEvent.blur(name);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(name).not.toHaveAttribute("aria-invalid");
    expect(name).not.toHaveAttribute("aria-describedby");
  });
});
