// Decides which screen the panel shows. Pure, so it is easy to test.

import type { Health } from "./api";

export type Screen =
  | "loading"
  | "helper" // 1: helper not running
  | "pair" // 2: enter the pairing code
  | "model" // 3: Ollama down or model missing
  | "signin" // 4: sign in, or paste it yourself
  | "home" // 5: no video here
  | "paste" // Level 0 paste form
  | "video" // 6: pick languages
  | "translating" // 7
  | "review" // 8: look it over
  | "published" // 9
  | "copy"; // 9b: copy or save

export type HealthResult =
  | { kind: "pending" }
  | { kind: "down" }
  | { kind: "unpaired" }
  | { kind: "ok"; health: Health };

export interface PanelState {
  health: HealthResult;
  /** The person chose "I'll paste it myself" (Level 0). */
  pasteMode: boolean;
  /** Video id from the active Studio tab, the ?video= override or a pasted link. */
  videoId: string | null;
  /** A job is open: its stage decides the screen. */
  job: null | { stage: "running" | "done" | "published"; fromVideo: boolean };
}

export function screenFor(s: PanelState): Screen {
  switch (s.health.kind) {
    case "pending":
      return "loading";
    case "down":
      return "helper";
    case "unpaired":
      return "pair";
  }
  const h = s.health.health;
  // Finished work stays on screen even if Ollama is closed afterwards.
  if (s.job && s.job.stage !== "running") {
    if (s.job.stage === "published") return "published";
    return s.job.fromVideo ? "review" : "copy";
  }
  if (h.model.state !== "ready") return "model";
  if (s.job) return "translating";
  if (s.pasteMode) return "paste";
  if (!h.signedIn) return "signin";
  return s.videoId ? "video" : "home";
}
