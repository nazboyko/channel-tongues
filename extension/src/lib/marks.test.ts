import { describe, expect, it } from "vitest";
import { markWords } from "../components";

describe("markWords", () => {
  it("marks whole added words in any case", () => {
    expect(markWords("Rain on a Mountain cabin, mountain air", ["mountain"])).toEqual([
      { text: "Rain on a ", added: false },
      { text: "Mountain", added: true },
      { text: " cabin, ", added: false },
      { text: "mountain", added: true },
      { text: " air", added: false },
    ]);
  });
  it("does not mark inside other words", () => {
    expect(markWords("mountainous road", ["mountain"])).toEqual([{ text: "mountainous road", added: false }]);
  });
  it("leaves text alone without added words", () => {
    expect(markWords("calm night", undefined)).toEqual([{ text: "calm night", added: false }]);
  });
  it("escapes words with special characters", () => {
    expect(markWords("a (b) c", ["(b)"]).filter((p) => p.added)).toEqual([{ text: "(b)", added: true }]);
    expect(markWords("a b c", ["(b)"]).some((p) => p.added)).toBe(false);
    expect(markWords("sleep-inducing sound", ["inducing"])).toEqual([
      { text: "sleep-", added: false },
      { text: "inducing", added: true },
      { text: " sound", added: false },
    ]);
  });
});
