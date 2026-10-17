import { describe, expect, it } from "vitest";
import { isStudio, pickStudioTab, studioLanguagesUrl, videoIdFromUrl } from "./url";

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

describe("pickStudioTab", () => {
  const studio = "https://studio.youtube.com/video/AbC_12-xyz9/edit";
  const other = "https://example.com/";
  it("uses the active Studio tab", () => {
    expect(pickStudioTab([{ id: 1, url: studio }, { id: 2, url: studio, active: true }], 1)).toBe(2);
  });
  it("then the tab it opened before, so a second click does not open another", () => {
    expect(pickStudioTab([{ id: 1, url: studio }, { id: 3, url: studio }, { id: 4, url: other, active: true }], 3)).toBe(3);
  });
  it("then any Studio tab", () => {
    expect(pickStudioTab([{ id: 5, url: other, active: true }, { id: 6, url: studio }], null)).toBe(6);
  });
  it("reuses the tab it opened even when Google asked to sign in there", () => {
    expect(pickStudioTab([{ id: 3, url: "https://accounts.google.com/v3/signin/identifier" }, { id: 4, url: other, active: true }], 3)).toBe(3);
  });
  it("never takes over another tab that is not on Studio", () => {
    expect(pickStudioTab([{ id: 3, url: other, active: true }], null)).toBeNull();
    expect(pickStudioTab([{ id: 3, url: other }], 7)).toBeNull(); // the one it opened was closed
    expect(pickStudioTab([], null)).toBeNull();
  });
});

