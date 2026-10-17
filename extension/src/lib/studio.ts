// Reads a video's title and description from the open Studio page, for the
// no-sign-in path. Read only: nothing is ever written to the page.

export interface StudioFields {
  title: string;
  description: string;
}

/** Runs inside the Studio page, so it must not use anything from this module. */
export function readStudioFieldsInPage(): StudioFields | null {
  const pick = (selectors: string[]): string => {
    for (const s of selectors) {
      const el = document.querySelector<HTMLElement>(s);
      const text = el?.innerText ?? "";
      if (text.trim()) return text;
    }
    return "";
  };
  const title = pick(["#title-textarea #textbox", "ytcp-video-title #textbox", "#title #textbox"]);
  const description = pick(["#description-textarea #textbox", "ytcp-video-description #textbox", "#description #textbox"]);
  if (!title.trim()) return null;
  return { title: title.trim(), description: description.replace(/\s+$/, "") };
}

/** Reads the fields from a tab; null when the page or the permission is not there. */
export async function readFromStudio(tabId: number): Promise<StudioFields | null> {
  try {
    const [res] = await chrome.scripting.executeScript({ target: { tabId }, func: readStudioFieldsInPage });
    return (res?.result as StudioFields | null) ?? null;
  } catch {
    return null;
  }
}

/** Asks Chrome for read access to Studio pages (a user gesture is needed). */
export async function askStudioAccess(): Promise<boolean> {
  try {
    return await chrome.permissions.request({ origins: ["https://studio.youtube.com/*"] });
  } catch {
    return false;
  }
}
