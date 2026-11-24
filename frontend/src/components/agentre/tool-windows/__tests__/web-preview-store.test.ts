import { beforeEach, describe, expect, it } from "vitest";

import { selectActiveTab, useWebPreviewStore } from "../web-preview-store";

const S = 7;
const st = () => useWebPreviewStore.getState();
const tabs = () => st().bySession[S]?.tabs ?? [];

beforeEach(() => useWebPreviewStore.setState({ bySession: {} }));

describe("web preview store · port tabs", () => {
  it("Given listening ports, When synced, Then each becomes a tab keyed by port, the first is active", () => {
    st().syncPorts(S, [
      { port: 3000, command: "npm run dev" },
      { port: 8080, command: "go run ." },
    ]);

    expect(tabs().map((t) => [t.key, t.port, t.command, t.listening])).toEqual([
      ["port:3000", 3000, "npm run dev", true],
      ["port:8080", 8080, "go run .", true],
    ]);
    expect(selectActiveTab(st().bySession[S])?.key).toBe("port:3000");
    expect(selectActiveTab(st().bySession[S])?.history).toEqual([
      "http://localhost:3000/",
    ]);
  });

  it("Given the active tab's port stops listening, Then the tab stays flagged not listening, and the non-active tab is removed", () => {
    st().syncPorts(S, [
      { port: 3000, command: "a" },
      { port: 8080, command: "b" },
    ]);
    st().syncPorts(S, []);

    expect(tabs().map((t) => [t.key, t.listening])).toEqual([
      ["port:3000", false],
    ]);
  });

  it("Given the flagged tab's port comes back, Then it is listening again and keeps its history", () => {
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    st().navigate(S, "port:3000", "http://localhost:3000/x");
    st().syncPorts(S, []);
    st().syncPorts(S, [{ port: 3000, command: "a2" }]);

    const t = tabs()[0];
    expect(t.listening).toBe(true);
    expect(t.command).toBe("a2");
    expect(t.history).toEqual([
      "http://localhost:3000/",
      "http://localhost:3000/x",
    ]);
  });

  it("Given a sync with unchanged ports, Then state identity is kept", () => {
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    const before = st().bySession;
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    expect(st().bySession).toBe(before);
  });
});

describe("web preview store · navigation", () => {
  it("Given a tab, When navigating, going back and reloading, Then history, index and reload counter follow", () => {
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    st().navigate(S, "port:3000", "http://localhost:3000/a");
    st().navigate(S, "port:3000", "http://localhost:3000/b");
    st().back(S, "port:3000");
    expect(selectActiveTab(st().bySession[S])).toMatchObject({ index: 1 });
    st().navigate(S, "port:3000", "http://localhost:3000/c");
    expect(tabs()[0].history).toEqual([
      "http://localhost:3000/",
      "http://localhost:3000/a",
      "http://localhost:3000/c",
    ]);
    const seq = tabs()[0].reloadSeq;
    st().reload(S, "port:3000");
    expect(tabs()[0].reloadSeq).toBe(seq + 1);
  });

  it("Given the first entry, When going back, Then nothing changes", () => {
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    st().back(S, "port:3000");
    expect(tabs()[0].index).toBe(0);
  });
});

describe("web preview store · openUrl", () => {
  it("Given a localhost URL on a listening port, Then that tab navigates and activates", () => {
    st().syncPorts(S, [
      { port: 3000, command: "a" },
      { port: 8080, command: "b" },
    ]);
    st().openUrl(S, "http://127.0.0.1:8080/docs?q=1");

    const active = selectActiveTab(st().bySession[S]);
    expect(active?.key).toBe("port:8080");
    expect(active?.history.at(-1)).toBe("http://127.0.0.1:8080/docs?q=1");
    expect(tabs()).toHaveLength(2);
  });

  it("Given a URL no port tab owns, Then an ad-hoc tab keyed by origin opens and is never flagged by port syncs", () => {
    st().syncPorts(S, [{ port: 3000, command: "a" }]);
    st().openUrl(S, "http://localhost:9999/x");
    st().syncPorts(S, [{ port: 3000, command: "a" }]);

    const active = selectActiveTab(st().bySession[S]);
    expect(active).toMatchObject({
      key: "url:http://localhost:9999",
      port: null,
      listening: true,
    });
    st().openUrl(S, "http://localhost:9999/y");
    expect(tabs().filter((t) => t.key.startsWith("url:"))).toHaveLength(1);
    expect(selectActiveTab(st().bySession[S])?.history).toEqual([
      "http://localhost:9999/x",
      "http://localhost:9999/y",
    ]);
  });
});
