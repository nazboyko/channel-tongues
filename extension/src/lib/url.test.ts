import { describe, expect, it } from "vitest";
import { isStudio, studioLanguagesUrl, videoIdFromUrl } from "./url";

describe("videoIdFromUrl", () => {
  const id = "AbC_12-xyz9";
  it.each([
    [`https://studio.youtube.com/video/${id}/edit`, id],
    [`https://studio.youtube.com/video/${id}/editor`, id],
    [`https://studio.youtube.com/video/${id}/edit?c=UCabc`, id],
    [`https://studio.youtube.com/video/${id}/translations`, id],
    [`https://www.youtube.com/watch?v=${id}&t=30s`, id],
    [`https://m.youtube.com/watch?v=${id}`, id],
    [`https://youtu.be/${id}?si=x`, id],
    [`https://www.youtube.com/shorts/${id}`, id],
    [id, id],
    [`  ${id}  `, id],
  ])("finds the id in %s", (url, want) => {
    expect(videoIdFromUrl(url)).toBe(want);
  });

  it.each([
    "https://studio.youtube.com/channel/UCabc/videos",
    "https://studio.youtube.com/video/short/edit",
    "https://www.youtube.com/watch?v=tooshort",
    "https://example.com/watch?v=AbC_12-xyz9",
    "not a url",
    "",
    undefined,
    null,
  ])("finds nothing in %s", (url) => {
    expect(videoIdFromUrl(url)).toBeNull();
  });
});

describe("isStudio", () => {
  it("knows a Studio page", () => {
    expect(isStudio("https://studio.youtube.com/channel/UCabc")).toBe(true);
    expect(isStudio("https://www.youtube.com/")).toBe(false);
    expect(isStudio(undefined)).toBe(false);
  });
});

describe("studioLanguagesUrl", () => {
  it("points at the video's translations page", () => {
    expect(studioLanguagesUrl("AbC_12-xyz9")).toBe("https://studio.youtube.com/video/AbC_12-xyz9/translations");
  });
});
