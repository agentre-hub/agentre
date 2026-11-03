import { render, screen, act, fireEvent } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import {
  IdentityMarkPicker,
  agentreUiResources,
} from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

/** 共享选择器接入宿主 i18next 后，语言切换仍须实时更新。 */
describe("icon registry localization", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("Given a rendered mark picker showing the selected icon label, When the language switches to zh-CN, Then the label is re-rendered in the new language", async () => {
    render(
      <IdentityMarkPicker
        kind="department"
        name="Engineering"
        iconKey="hammer"
        color="agent-1"
        onPickIcon={() => {}}
        onPickColor={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Change icon" }));

    expect(
      screen.getByText(agentreUiResources.en.iconRegistry.icons.hammer),
    ).toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });

    expect(
      screen.getByText(agentreUiResources["zh-CN"].iconRegistry.icons.hammer),
    ).toBeInTheDocument();
  });
});
