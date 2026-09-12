import { describe, expect, it } from "vitest";

import { monacoLanguageForPath } from "./monaco-language";

describe("monacoLanguageForPath", () => {
  it("maps common extensions to monaco built-in language ids", () => {
    expect(monacoLanguageForPath("src/main.go")).toBe("go");
    expect(monacoLanguageForPath("src/App.tsx")).toBe("typescript");
    expect(monacoLanguageForPath("server.js")).toBe("javascript");
    expect(monacoLanguageForPath("styles.css")).toBe("css");
    expect(monacoLanguageForPath("README.md")).toBe("markdown");
    expect(monacoLanguageForPath("data.json")).toBe("json");
    expect(monacoLanguageForPath("script.sh")).toBe("shell");
    expect(monacoLanguageForPath("app.py")).toBe("python");
    expect(monacoLanguageForPath("lib.rs")).toBe("rust");
    expect(monacoLanguageForPath("config.yaml")).toBe("yaml");
    expect(monacoLanguageForPath("model.h")).toBe("cpp");
  });

  it("matches case-insensitively and works on nested paths", () => {
    expect(monacoLanguageForPath("/a/b/CONFIG.YML")).toBe("yaml");
    expect(monacoLanguageForPath("dir/sub/file.TSX")).toBe("typescript");
    expect(monacoLanguageForPath("cmd/agentred/main.go")).toBe("go");
  });

  it("recognizes Dockerfile by filename", () => {
    expect(monacoLanguageForPath("Dockerfile")).toBe("dockerfile");
    expect(monacoLanguageForPath("build/Dockerfile")).toBe("dockerfile");
  });

  it("returns plaintext for unknown or extensionless names", () => {
    expect(monacoLanguageForPath("archive.zip")).toBe("plaintext");
    expect(monacoLanguageForPath("noextension")).toBe("plaintext");
    expect(monacoLanguageForPath("")).toBe("plaintext");
    expect(monacoLanguageForPath("dir/.hidden")).toBe("plaintext");
    expect(monacoLanguageForPath("Makefile")).toBe("plaintext");
  });
});
