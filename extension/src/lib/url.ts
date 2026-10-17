// Reads the video id from a YouTube Studio or watch page address.

const ID = /^[A-Za-z0-9_-]{11}$/;

/** Returns the 11-character video id in a Studio, watch, shorts or youtu.be URL, or null. */
export function videoIdFromUrl(raw: string | undefined | null): string | null {
  if (!raw) return null;
  const text = raw.trim();
  if (ID.test(text)) return text;
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return null;
  }
  const host = url.hostname.toLowerCase();
  const parts = url.pathname.split("/").filter(Boolean);
  if (host === "studio.youtube.com") {
    // /video/<id>/edit, /video/<id>/editor, /video/<id>/translations ...
    const i = parts.indexOf("video");
    return i >= 0 && ID.test(parts[i + 1] ?? "") ? parts[i + 1] : null;
  }
  if (host === "youtu.be") {
    return ID.test(parts[0] ?? "") ? parts[0] : null;
  }
  if (host === "youtube.com" || host.endsWith(".youtube.com")) {
    const v = url.searchParams.get("v");
    if (v && ID.test(v)) return v;
    if ((parts[0] === "shorts" || parts[0] === "live" || parts[0] === "embed") && ID.test(parts[1] ?? "")) {
      return parts[1];
    }
  }
  return null;
}

/** True when the URL is a YouTube Studio page. */
export function isStudio(raw: string | undefined | null): boolean {
  if (!raw) return false;
  try {
    return new URL(raw).hostname.toLowerCase() === "studio.youtube.com";
  } catch {
    return false;
  }
}

/** The Studio page where the video's translations are listed. */
export function studioLanguagesUrl(videoId: string): string {
  return `https://studio.youtube.com/video/${videoId}/translations`;
}

export interface TabLike {
  id?: number;
  url?: string;
  active?: boolean;
}

/**
 * The tab Open in Studio shows the page in: the active Studio tab, then the
 * one it opened before (still open, even if Google asked to sign in there),
 * then any other Studio tab. Null means a new tab. Another tab that is not on
 * Studio is never taken over.
 */
export function pickStudioTab(tabs: TabLike[], opened: number | null): number | null {
  const studio = tabs.filter((t) => t.id !== undefined && isStudio(t.url));
  const pick = studio.find((t) => t.active) ?? tabs.find((t) => t.id !== undefined && t.id === opened) ?? studio[0];
  return pick?.id ?? null;
}
