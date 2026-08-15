import { describe, expect, it } from "vitest";

import { classifyLink } from "./link-classify";

const CWD = "/Users/me/proj";

describe("classifyLink", () => {
  describe("URL forms", () => {
    it("when https://… then kind=url, url=original", () => {
      expect(classifyLink("https://example.com/a/b", CWD)).toEqual({
        kind: "url",
        url: "https://example.com/a/b",
      });
    });

    it("when http://… then kind=url", () => {
      expect(classifyLink("http://example.com", CWD)).toMatchObject({
        kind: "url",
        url: "http://example.com",
      });
    });

    it("when www.… then kind=url with http:// prefix added", () => {
      expect(classifyLink("www.example.com", CWD)).toEqual({
        kind: "url",
        url: "http://www.example.com",
      });
    });

    it("when mailto: then kind=url", () => {
      expect(classifyLink("mailto:a@b.com", CWD)).toEqual({
        kind: "url",
        url: "mailto:a@b.com",
      });
    });

    it("when tel: then kind=url", () => {
      expect(classifyLink("tel:+1234", CWD)).toEqual({
        kind: "url",
        url: "tel:+1234",
      });
    });
  });

  describe("Local absolute paths", () => {
    it("Given an encoded non-ASCII absolute path, when classified, then OpenPath receives the decoded filesystem path", () => {
      expect(
        classifyLink("/Users/me/proj/docs/%E6%9C%AC%E5%9C%B0%20E2E.md", CWD),
      ).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/docs/本地 E2E.md",
        pathKind: "file",
        relPath: "docs/本地 E2E.md",
      });
    });

    it("Given a malformed percent escape, when classified, then it remains usable instead of throwing", () => {
      expect(classifyLink("/Users/me/proj/docs/100%.md", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/docs/100%.md",
        pathKind: "file",
        relPath: "docs/100%.md",
      });
    });

    it("when POSIX absolute path inside cwd then kind=local-internal with relPath", () => {
      expect(classifyLink("/Users/me/proj/src/foo.go", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/src/foo.go",
        pathKind: "file",
        relPath: "src/foo.go",
      });
    });

    it("when POSIX absolute path with :line then line is parsed", () => {
      expect(classifyLink("/Users/me/proj/src/foo.go:42", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/src/foo.go",
        pathKind: "file",
        relPath: "src/foo.go",
        line: 42,
      });
    });

    it("when POSIX absolute path with :line:col then both parsed", () => {
      expect(classifyLink("/Users/me/proj/src/foo.go:42:7", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/src/foo.go",
        pathKind: "file",
        relPath: "src/foo.go",
        line: 42,
        col: 7,
      });
    });

    it("when POSIX absolute path outside cwd then kind=local-external", () => {
      expect(classifyLink("/usr/local/bin/agentred", CWD)).toEqual({
        kind: "local-external",
        fullPath: "/usr/local/bin/agentred",
        pathKind: "file",
      });
    });

    it("when POSIX absolute path ends with slash then pathKind=folder", () => {
      expect(classifyLink("/Users/me/proj/docs/", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/docs/",
        pathKind: "folder",
        relPath: "docs/",
      });
    });

    it("when cwd is empty/undefined then absolute path is local-external", () => {
      expect(classifyLink("/Users/me/proj/foo.go", undefined)).toEqual({
        kind: "local-external",
        fullPath: "/Users/me/proj/foo.go",
        pathKind: "file",
      });
    });

    it("when Windows absolute path then kind=local-external (no cwd match)", () => {
      const got = classifyLink("C:\\Users\\x\\foo.go:10", CWD);
      expect(got).toEqual({
        kind: "local-external",
        fullPath: "C:\\Users\\x\\foo.go",
        pathKind: "file",
        line: 10,
      });
    });

    it("Given a Windows absolute path inside a Windows cwd, when classified, then it remains internal with a slash-normalized relPath", () => {
      expect(
        classifyLink("C:\\work\\proj\\src\\foo.go", "C:\\work\\proj"),
      ).toEqual({
        kind: "local-internal",
        fullPath: "C:\\work\\proj\\src\\foo.go",
        pathKind: "file",
        relPath: "src/foo.go",
      });
    });

    it("when href is exactly cwd then relPath is empty", () => {
      expect(classifyLink(CWD, CWD)).toEqual({
        kind: "local-internal",
        fullPath: CWD,
        pathKind: "folder",
        relPath: "",
      });
    });
  });

  describe("file:// protocol", () => {
    it("when file:///path then treated as POSIX absolute", () => {
      expect(classifyLink("file:///Users/me/proj/foo.go", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/foo.go",
        pathKind: "file",
        relPath: "foo.go",
      });
    });

    it("when file:// with URL-encoded chars then decoded", () => {
      expect(classifyLink("file:///Users/me/proj/a%20b.go", CWD)).toMatchObject(
        {
          kind: "local-internal",
          fullPath: "/Users/me/proj/a b.go",
          pathKind: "file",
        },
      );
    });

    it("Given a localhost file URL, when classified, then it resolves as a local absolute path", () => {
      expect(classifyLink("file://localhost/Users/me/proj/a.go", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/a.go",
        pathKind: "file",
        relPath: "a.go",
      });
    });

    it("Given a file URL with a remote authority, when classified, then it is rejected instead of opening a process-relative path", () => {
      expect(classifyLink("file://server/share/a.go", CWD)).toEqual({
        kind: "unknown",
        href: "file://server/share/a.go",
      });
    });
  });

  describe("Relative paths", () => {
    it("Given a cwd, when a relative file carries line and column, then it resolves to an internal absolute target", () => {
      expect(classifyLink("frontend/src/chat.tsx:42:7", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/frontend/src/chat.tsx",
        pathKind: "file",
        relPath: "frontend/src/chat.tsx",
        line: 42,
        col: 7,
      });
    });

    it("Given a cwd, when a trusted filename is classified, then it resolves from that cwd", () => {
      expect(classifyLink("README.md", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/README.md",
        pathKind: "file",
        relPath: "README.md",
      });
    });

    it("Given a multi-segment relative directory, when classified, then it resolves as an internal folder", () => {
      expect(classifyLink("frontend/src/components", CWD)).toEqual({
        kind: "local-internal",
        fullPath: "/Users/me/proj/frontend/src/components",
        pathKind: "folder",
        relPath: "frontend/src/components",
      });
    });

    it("Given parent traversal, when the resolved target leaves cwd, then it is classified as external", () => {
      expect(classifyLink("../README.md:8", CWD)).toEqual({
        kind: "local-external",
        fullPath: "/Users/me/README.md",
        pathKind: "file",
        line: 8,
      });
    });

    it("Given a Windows cwd, when a slash-separated relative path is classified, then it preserves the native absolute target", () => {
      expect(classifyLink("src/main.go:9", "C:\\work\\proj")).toEqual({
        kind: "local-internal",
        fullPath: "C:\\work\\proj\\src\\main.go",
        pathKind: "file",
        relPath: "src/main.go",
        line: 9,
      });
    });

    it("Given no cwd, when a relative path is classified, then it remains unknown", () => {
      expect(classifyLink("internal/foo.go", undefined)).toEqual({
        kind: "unknown",
        href: "internal/foo.go",
      });
    });
  });

  describe("Unknown forms", () => {
    it("when href is empty then kind=unknown", () => {
      expect(classifyLink(undefined, CWD)).toEqual({
        kind: "unknown",
        href: "",
      });
    });

    it("when javascript: scheme then kind=unknown", () => {
      // 即使是 well-formed URL prefix，但安全考虑只白名单 http/https/mailto/tel/www
      expect(classifyLink("javascript:alert(1)", CWD)).toEqual({
        kind: "unknown",
        href: "javascript:alert(1)",
      });
    });
  });
});
