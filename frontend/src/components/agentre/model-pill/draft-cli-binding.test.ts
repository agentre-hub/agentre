import { describe, expect, it } from "vitest";
import { draftCLIModelBinding } from "./draft-cli-binding";

describe("draft CLI binding", () => {
  it("Given an Agent backend and an optional override, When resolving its draft binding, Then another backend never borrows the primary backend's model", () => {
    const agent = { agentBackendId: 7, agentCLIModel: "opus" };
    expect(draftCLIModelBinding(agent, null)).toBe("opus");
    expect(draftCLIModelBinding(agent, 7)).toBe("opus");
    expect(draftCLIModelBinding(agent, 8)).toBeUndefined();
    expect(draftCLIModelBinding(undefined, null)).toBeUndefined();
  });
});
