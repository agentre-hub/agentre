import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ImagePreview } from "./image-view";

describe("ImagePreview", () => {
  // 图片档没有 Monaco：readFile 的应答自带 base64 正文与 MIME，直接渲染成 data URL。
  it("renders the base64 payload as a data URL image labelled by its file name", () => {
    render(
      <ImagePreview
        content="aGVsbG8="
        contentType="image/png"
        alt="logo.png"
      />,
    );

    const img = screen.getByAltText("logo.png");
    expect(img.getAttribute("src")).toBe("data:image/png;base64,aGVsbG8=");
  });

  // contentType 在应答里是可缺省字段（omitempty）：缺了就退到 octet-stream，
  // 而不是把字面量 "undefined" 拼进 data URL。
  it("falls back to a generic MIME type when the response carries none", () => {
    render(<ImagePreview content="aGVsbG8=" alt="mystery" />);

    expect(screen.getByAltText("mystery").getAttribute("src")).toBe(
      "data:application/octet-stream;base64,aGVsbG8=",
    );
  });
});
