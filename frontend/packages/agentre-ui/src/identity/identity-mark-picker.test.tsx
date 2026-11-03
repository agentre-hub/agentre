import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "i18next";
import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";
import {
  IdentityMarkPicker,
  type IdentityMarkPickerProps,
} from "./identity-mark-picker";

const port = () => ({
  onUpload: vi.fn().mockResolvedValue(undefined),
  onDelete: vi.fn().mockResolvedValue(undefined),
});

async function open(over: Partial<IdentityMarkPickerProps> = {}) {
  const props: IdentityMarkPickerProps = {
    kind: "agent",
    name: "Code Reviewer",
    iconKey: "",
    color: "agent-3",
    onPickIcon: vi.fn(),
    onPickColor: vi.fn(),
    ...over,
  };
  const user = userEvent.setup();
  const view = render(<IdentityMarkPicker {...props} />);
  await user.click(
    screen.getByRole("button", {
      name: props.kind === "agent" ? "更改头像" : "更改图标",
    }),
  );
  return { ...view, user, props };
}

function tab(label: string) {
  return screen.getByRole("tab", { name: label });
}

beforeEach(async () => {
  i18n.addResourceBundle(
    "zh-CN",
    AGENTRE_UI_NAMESPACE,
    agentreUiResources["zh-CN"],
  );
  await i18n.changeLanguage("zh-CN");
});

describe("IdentityMarkPicker", () => {
  it.each(["agent", "department", "project"] as const)(
    "Given %s without an upload port When opened Then only preset, icon and letter tabs appear",
    async (kind) => {
      await open({ kind });
      expect(
        screen.getAllByRole("tab").map((el) => el.getAttribute("aria-label")),
      ).toEqual(["预设", "图标", "字母"]);
      expect(tab("字母")).toHaveAttribute("aria-selected", "true");
      expect(
        within(tab("字母")).getByText(kind === "agent" ? "CR" : "C"),
      ).toBeInTheDocument();
    },
  );

  it.each(["agent", "department", "project"] as const)(
    "Given an unnamed %s When opened Then the letter hint matches the placeholder mark",
    async (kind) => {
      await open({ kind, name: "" });
      expect(within(tab("字母")).getByText("?")).toBeInTheDocument();
    },
  );

  it("Given a remote URL instead of an uploaded raster When opened Then the picker uses its fallback without loading the remote image", async () => {
    const { user } = await open({
      upload: port(),
      iconKey: "rocket",
      avatarDataUrl: "https://other.example/track",
    });
    expect(tab("图标")).toHaveAttribute("aria-selected", "true");
    await user.click(tab("图片"));
    expect(
      document.querySelector('img[src="https://other.example/track"]'),
    ).toBeNull();
  });

  it("Given the image tab is open When its upload capability is withdrawn Then an available tab stays selected", async () => {
    const { user, props, rerender } = await open({
      upload: port(),
      iconKey: "rocket",
    });
    await user.click(tab("图片"));
    rerender(<IdentityMarkPicker {...props} upload={undefined} />);
    expect(screen.queryByRole("tab", { name: "图片" })).not.toBeInTheDocument();
    expect(tab("图标")).toHaveAttribute("aria-selected", "true");
    expect(
      screen.getByRole("textbox", { name: "搜索图标" }),
    ).toBeInTheDocument();
  });

  it.each([port(), "after-create"] as const)(
    "Given upload capability %s When opened Then the image tab follows the letter tab",
    async (upload) => {
      const { user } = await open({ upload });
      expect(
        screen.getAllByRole("tab").map((el) => el.getAttribute("aria-label")),
      ).toEqual(["预设", "图标", "字母", "图片"]);
      await user.click(tab("图片"));
      if (upload === "after-create") {
        expect(
          screen.getByText("创建完成后才能上传头像图片，先选个图标或字母吧。"),
        ).toBeInTheDocument();
        expect(
          screen.queryByRole("button", { name: "上传" }),
        ).not.toBeInTheDocument();
      } else
        expect(
          screen.getByRole("button", { name: "上传" }),
        ).toBeInTheDocument();
    },
  );

  it.each([
    ["preset:agent-architect", undefined, "预设"],
    ["rocket", undefined, "图标"],
    ["", undefined, "字母"],
    ["preset:unknown", undefined, "字母"],
    ["preset:agent-architect", "data:image/png;base64,AA", "图片"],
  ])(
    "Given key %s and image %s When opened Then active source %s is selected",
    async (iconKey, avatarDataUrl, label) => {
      const { user } = await open({ iconKey, avatarDataUrl, upload: port() });
      expect(tab(label!)).toHaveAttribute("aria-selected", "true");
      await user.click(tab(label === "字母" ? "预设" : "字母"));
      await user.keyboard("{Escape}");
      await user.click(screen.getByRole("button", { name: "更改头像" }));
      expect(tab(label!)).toHaveAttribute("aria-selected", "true");
    },
  );

  it.each([
    ["agent", "架构师", "preset:agent-architect", 32],
    ["department", "研发", "preset:team-dev", 16],
    ["project", "Git", "preset:project-git", 16],
  ] as const)(
    "Given %s When a preset is chosen Then its key replaces the icon and the popover closes",
    async (kind, label, key, count) => {
      const { user, props } = await open({ kind, iconKey: key });
      const radios = screen.getAllByRole("radio");
      expect(radios).toHaveLength(count);
      expect(screen.getByRole("radio", { name: label })).toHaveAttribute(
        "aria-checked",
        "true",
      );
      expect(
        radios.filter((el) => el.getAttribute("aria-checked") === "false"),
      ).toHaveLength(count - 1);
      expect(screen.getByRole("radio", { name: label })).toHaveAttribute(
        "title",
        label,
      );
      screen.getByRole("radio", { name: label }).focus();
      await user.keyboard("{Enter}");
      expect(props.onPickIcon).toHaveBeenCalledWith(key);
      await waitFor(() =>
        expect(screen.queryByRole("tablist")).not.toBeInTheDocument(),
      );
    },
  );

  it("Given an English locale When viewing presets Then radio names and checked state are localized", async () => {
    await i18n.changeLanguage("en");
    const user = userEvent.setup();
    render(
      <IdentityMarkPicker
        kind="agent"
        name="Test"
        iconKey="preset:agent-architect"
        color="agent-3"
        onPickIcon={vi.fn()}
        onPickColor={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Change avatar" }));
    expect(screen.getByRole("radio", { name: "Architect" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  it("Given a selected preset When cancelling it Then an empty icon key is emitted and the popover closes", async () => {
    const { user, props } = await open({ iconKey: "preset:agent-architect" });
    await user.click(screen.getByRole("button", { name: "取消预设" }));
    expect(props.onPickIcon).toHaveBeenCalledWith("");
    await waitFor(() =>
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument(),
    );
  });

  it("Given a selected icon When cancelling it Then an empty icon key is emitted", async () => {
    const { user, props } = await open({ iconKey: "rocket" });
    await user.click(screen.getByRole("button", { name: "取消图标" }));
    expect(props.onPickIcon).toHaveBeenCalledWith("");
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  });

  it.each(["agent", "project", "department"] as const)(
    "Given %s When changing tabs Then colour visibility and usage notes follow the kind",
    async (kind) => {
      const { user, props } = await open({
        kind,
        upload: kind === "agent" ? port() : undefined,
      });
      for (const label of kind === "agent"
        ? ["预设", "图标", "字母", "图片"]
        : ["预设", "图标", "字母"]) {
        await user.click(tab(label));
        const hidden = kind === "department" && label === "预设";
        expect(screen.queryByRole("group", { name: "颜色" }) !== null).toBe(
          !hidden,
        );
        if (!hidden) {
          const colors = screen.getByRole("group", { name: "颜色" });
          expect(
            within(colors).getByRole("button", { name: "agent-3" }),
          ).toHaveAttribute("aria-pressed", "true");
          await user.click(
            within(colors).getByRole("button", { name: "agent-7" }),
          );
          expect(props.onPickColor).toHaveBeenLastCalledWith("agent-7");
          if (kind !== "department")
            expect(
              screen.getByText(
                kind === "agent"
                  ? "@提及也用这个颜色"
                  : "对话标签、@提及也用这个颜色",
              ),
            ).toBeInTheDocument();
        }
      }
    },
  );

  it.each([
    [2 * 1024 * 1024 + 1, "image/png", "图片过大，请上传 2MB 以内的文件"],
    [10, "image/gif", "请选择 PNG / JPG / WEBP 图片"],
    [10, "", "请选择 PNG / JPG / WEBP 图片"],
  ])(
    "Given a file of size %s and type %s When selected Then it is rejected before upload",
    async (size, type, message) => {
      const upload = port();
      const { user } = await open({ upload });
      await user.click(tab("图片"));
      fireEvent.change(screen.getByLabelText("上传图片"), {
        target: {
          files: [
            new File([new Uint8Array(size as number)], "avatar", {
              type: type as string,
            }),
          ],
        },
      });
      expect(await screen.findByRole("alert")).toHaveTextContent(
        message as string,
      );
      expect(upload.onUpload).not.toHaveBeenCalled();
    },
  );

  it.each(["image/png", "image/jpeg", "image/webp"])(
    "Given a valid %s at exactly 2MB When selected Then the upload port receives it",
    async (type) => {
      const upload = port();
      const { user } = await open({ upload });
      await user.click(tab("图片"));
      const file = new File([new Uint8Array(2 * 1024 * 1024)], "avatar", {
        type,
      });
      fireEvent.change(screen.getByLabelText("上传图片"), {
        target: { files: [file] },
      });
      await waitFor(() => expect(upload.onUpload).toHaveBeenCalledWith(file));
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(
        screen.getByText("PNG / JPG / WEBP · 最大 2MB"),
      ).toBeInTheDocument();
    },
  );

  it("Given an uploaded image When replacing or deleting Then the corresponding port runs and pending actions are disabled", async () => {
    let finish!: () => void;
    const upload = port();
    upload.onUpload.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const { user } = await open({
      upload,
      avatarDataUrl: "data:image/png;base64,AA",
    });
    expect(screen.getByRole("button", { name: "替换" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("上传图片"), {
      target: { files: [new File(["ok"], "a.png", { type: "image/png" })] },
    });
    expect(screen.getByRole("button", { name: "替换" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "删除上传头像" })).toBeDisabled();
    finish();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "删除上传头像" }),
      ).toBeEnabled(),
    );
    await user.click(screen.getByRole("button", { name: "删除上传头像" }));
    expect(upload.onDelete).toHaveBeenCalledOnce();
  });

  it.each(["tab", "popover"])(
    "Given an upload in flight When the image panel is reopened via %s Then writes remain disabled and failures remain visible",
    async (navigation) => {
      let fail!: (error: Error) => void;
      const upload = port();
      upload.onUpload.mockImplementationOnce(
        () =>
          new Promise<void>((_, reject) => {
            fail = reject;
          }),
      );
      const { user } = await open({
        upload,
        avatarDataUrl: "data:image/png;base64,AA",
      });
      fireEvent.change(screen.getByLabelText("上传图片"), {
        target: { files: [new File(["ok"], "a.png", { type: "image/png" })] },
      });
      if (navigation === "tab") {
        await user.click(tab("字母"));
        await user.click(tab("图片"));
      } else {
        await user.keyboard("{Escape}");
        await user.click(screen.getByRole("button", { name: "更改头像" }));
      }
      expect(screen.getByRole("button", { name: "替换" })).toBeDisabled();
      expect(
        screen.getByRole("button", { name: "删除上传头像" }),
      ).toBeDisabled();
      fail(new Error("network unavailable"));
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "network unavailable",
      );
      await user.click(screen.getByRole("button", { name: "删除上传头像" }));
      expect(upload.onDelete).toHaveBeenCalledOnce();
    },
  );

  it("Given a failing upload or delete When the port rejects Then a localized failure is shown without an unhandled rejection", async () => {
    const upload = port();
    upload.onUpload.mockRejectedValueOnce(new Error("network unavailable"));
    upload.onDelete.mockRejectedValueOnce(null);
    const { user } = await open({
      upload,
      avatarDataUrl: "data:image/png;base64,AA",
    });
    fireEvent.change(screen.getByLabelText("上传图片"), {
      target: { files: [new File(["ok"], "a.png", { type: "image/png" })] },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "network unavailable",
    );
    await user.click(screen.getByRole("button", { name: "删除上传头像" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("删除失败"),
    );
  });

  it.each(["终端", "shell"])(
    "Given an icon search for %s When presets and icons match Then presets come first and picking writes their key",
    async (query) => {
      const { user, props } = await open({ iconKey: "rocket" });
      const search = screen.getByRole("textbox", { name: "搜索图标" });
      await user.type(search, query);
      const preset = screen.getByRole("radio", { name: "终端" });
      const icon = screen.getByRole("button", { name: "终端" });
      expect(
        preset.compareDocumentPosition(icon) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      await user.click(preset);
      expect(props.onPickIcon).toHaveBeenCalledWith("preset:agent-shell");
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    },
  );

  it("Given an English icon search When searching a localized name Then matching presets appear before icons and empty results are explicit", async () => {
    await i18n.changeLanguage("en");
    const user = userEvent.setup();
    render(
      <IdentityMarkPicker
        kind="agent"
        name="Test"
        iconKey="rocket"
        color="agent-3"
        onPickIcon={vi.fn()}
        onPickColor={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Change avatar" }));
    const search = screen.getByRole("textbox", { name: "Search icons" });
    await user.type(search, "terminal");
    expect(screen.getByRole("radio", { name: "Terminal" })).toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "zzzzzz");
    expect(screen.getByText("No matches")).toBeInTheDocument();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  });

  it("Given an uploaded image When visiting non-image tabs Then the active-upload and fallback hint remains visible", async () => {
    const { user } = await open({
      iconKey: "preset:agent-architect",
      upload: port(),
      avatarDataUrl: "data:image/png;base64,AA",
    });
    expect(
      screen.queryByText("当前生效：上传图片。", { exact: false }),
    ).not.toBeInTheDocument();
    for (const label of ["预设", "图标", "字母"]) {
      await user.click(tab(label));
      expect(
        screen.getByText("当前生效：上传图片。", { exact: false }),
      ).toHaveTextContent("预设 / 图标作为图片删除后的备用。");
    }
  });

  it("Given an open picker When pressing Escape or clicking outside Then it closes", async () => {
    const { user } = await open();
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument(),
    );
    await user.click(screen.getByRole("button", { name: "更改头像" }));
    await user.click(document.body);
    await waitFor(() =>
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument(),
    );
  });
});
