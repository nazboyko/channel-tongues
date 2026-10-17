import { describe, expect, it } from "vitest";
import type { Health } from "./api";
import { screenFor, type PanelState } from "./state";

const health = (over: Partial<Health> = {}): Health => ({
  ok: true,
  version: "0.1.0",
  mock: false,
  model: { state: "ready", model: "gemma4:e4b" },
  signedIn: true,
  canSignIn: true,
  channels: [{ id: "UC1", title: "The Slow Night Hours" }],
  keepWords: [],
  usedToday: 0,
  videosLeftToday: 170,
  ...over,
});

const base: PanelState = { health: { kind: "ok", health: health() }, pasteMode: false, videoId: null, job: null };

describe("screenFor", () => {
  it("waits for the first health answer", () => {
    expect(screenFor({ ...base, health: { kind: "pending" } })).toBe("loading");
  });
  it("asks to start the helper when it cannot be reached", () => {
    expect(screenFor({ ...base, health: { kind: "down" } })).toBe("helper");
  });
  it("asks for the pairing code when the helper refuses", () => {
    expect(screenFor({ ...base, health: { kind: "unpaired" } })).toBe("pair");
  });
  it("shows the model screen when Ollama is down or the model is missing", () => {
    expect(screenFor({ ...base, health: { kind: "ok", health: health({ model: { state: "down", model: "x" } }) } })).toBe("model");
    expect(screenFor({ ...base, health: { kind: "ok", health: health({ model: { state: "model-missing", model: "x" } }) } })).toBe(
      "model",
    );
  });
  it("offers sign-in or paste when nobody is signed in", () => {
    const signedOut = { ...base, health: { kind: "ok" as const, health: health({ signedIn: false, channels: [] }) } };
    expect(screenFor(signedOut)).toBe("signin");
    expect(screenFor({ ...signedOut, videoId: "AbC_12-xyz9" })).toBe("signin");
    expect(screenFor({ ...signedOut, pasteMode: true })).toBe("paste");
  });
  it("shows the video when there is one, the latest uploads otherwise", () => {
    expect(screenFor(base)).toBe("home");
    expect(screenFor({ ...base, videoId: "AbC_12-xyz9" })).toBe("video");
  });
  it("follows a job through its stages", () => {
    expect(screenFor({ ...base, videoId: "v", job: { stage: "running", fromVideo: true } })).toBe("translating");
    expect(screenFor({ ...base, videoId: "v", job: { stage: "done", fromVideo: true } })).toBe("review");
    expect(screenFor({ ...base, videoId: "v", job: { stage: "published", fromVideo: true } })).toBe("published");
    expect(screenFor({ ...base, pasteMode: true, job: { stage: "done", fromVideo: false } })).toBe("copy");
  });
  it("keeps finished work on screen when Ollama is closed afterwards", () => {
    const down = { kind: "ok" as const, health: health({ model: { state: "down", model: "x" } }) };
    expect(screenFor({ ...base, health: down, job: { stage: "done", fromVideo: true } })).toBe("review");
    expect(screenFor({ ...base, health: down, job: { stage: "published", fromVideo: true } })).toBe("published");
    expect(screenFor({ ...base, health: down, job: { stage: "done", fromVideo: false } })).toBe("copy");
    expect(screenFor({ ...base, health: down, job: { stage: "running", fromVideo: true } })).toBe("model");
  });
});
