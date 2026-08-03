import { describe, expect, it } from "vitest";
import { cleanReply, expressionFromText } from "$lib/expressions";

describe("expressionFromText", () => {
  it("maps legacy tagged expressions to BMO states", () => {
    expect(expressionFromText("[emotion: apologetic] Sorry about that.")).toBe("worried");
    expect(expressionFromText("[face: happy] Done.")).toBe("calm");
  });

  it("infers a useful fallback from response text", () => {
    expect(expressionFromText("I am checking that now.")).toBe("thinking");
    expect(expressionFromText("That failed to connect.")).toBe("worried");
  });

  it("removes expression tags from displayed replies", () => {
    expect(cleanReply("[expression: surprised] Unexpected result.")).toBe("Unexpected result.");
  });
});
