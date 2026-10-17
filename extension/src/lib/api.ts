// Talks to the local helper on 127.0.0.1 with the pairing code.

import { SseParser } from "./sse";

export type ModelState = "down" | "model-missing" | "ready";

export interface Channel {
  id: string;
  title: string;
}

export interface Health {
  ok: boolean;
  version: string;
  mock: boolean;
  model: { state: ModelState; model: string };
  signedIn: boolean;
  canSignIn: boolean;
  channels: Channel[];
  keepWords: string[];
  usedToday: number;
  videosLeftToday: number;
}

export interface LangRef {
  code: string;
  name: string;
}

export interface Video {
  id: string;
  title: string;
  description: string;
  defaultLanguage: string;
  sourceName: string;
  channelId: string;
  channelTitle: string;
  languages: LangRef[];
  shown: string[];
  suggested: string[];
  studioUrl: string;
}

export interface Upload {
  id: string;
  title: string;
  channelTitle: string;
  languages: number;
  hasLanguage: boolean;
}

export interface Language {
  code: string;
  english: string;
  native: string;
  rank: number;
  popular: boolean;
}

export type Status = "waiting" | "translating" | "checking" | "ready" | "check" | "failed" | "cancelled";

export interface JobLang {
  code: string;
  name: string;
  english: string;
  status: Status;
  title: string;
  description: string;
  note?: string;
  edited: boolean;
  /** The translation read back into the video's language, title and first line. */
  backTitle?: string;
  backLine?: string;
  /** Words in the read-back that the source does not have. */
  added?: string[];
  /** The ones found only in the description: marked, but the card stays ticked. */
  addedLine?: string[];
}

export interface Job {
  id: string;
  videoId?: string;
  channelTitle?: string;
  state: "running" | "done" | "cancelled";
  sourceTitle: string;
  sourceDescription: string;
  sourceLanguage: string;
  languages: JobLang[];
  ready: number;
  toCheck: number;
  failed: number;
  finished: number;
}

export type Outcome = "added" | "updated" | "replaced" | "kept" | "same" | "failed" | "removed";

export interface PublishResult {
  backupId: string;
  videoId: string;
  results: { lang: string; name: string; outcome: Outcome }[];
  published: number;
  kept: number;
  failed: number;
  studioUrl: string;
}

/** An answer from the helper that is not OK. message is a sentence for the person. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

/** The helper could not be reached at all. */
export class HelperDown extends Error {
  constructor() {
    super("The helper is not running.");
  }
}

export interface ApiOptions {
  base?: string;
  code: () => Promise<string>;
  fetchFn?: typeof fetch;
}

export class Api {
  private base: string;
  private fetchFn: typeof fetch;

  constructor(private opts: ApiOptions) {
    this.base = opts.base ?? "http://127.0.0.1:7312";
    this.fetchFn = opts.fetchFn ?? fetch.bind(globalThis);
  }

  private async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const code = await this.opts.code();
    let res: Response;
    try {
      res = await this.fetchFn(this.base + path, {
        method,
        headers: {
          Authorization: `Bearer ${code}`,
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal,
      });
    } catch (e) {
      if (signal?.aborted) throw e;
      throw new HelperDown();
    }
    const text = await res.text();
    let data: unknown = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = null;
    }
    if (!res.ok) {
      const d = (data ?? {}) as { error?: string; code?: string };
      throw new ApiError(res.status, d.code ?? "error", d.error ?? "Something went wrong. Try again.");
    }
    return data as T;
  }

  health = () => this.request<Health>("GET", "/health");
  video = (id: string) => this.request<Video>("GET", `/video/${encodeURIComponent(id)}`);
  uploads = (limit = 6) => this.request<{ videos: Upload[] }>("GET", `/channel/videos?limit=${limit}`).then((r) => r.videos);
  languages = (source: string) =>
    this.request<{ languages: Language[] }>("GET", `/languages?source=${encodeURIComponent(source)}`).then((r) => r.languages);
  /** The last pick on any channel, for pasted text (the popular twelve at first). */
  usedLanguages = () => this.request<{ used: string[] }>("GET", "/languages").then((r) => r.used);
  startJob = (req: { videoId?: string; title?: string; description?: string; sourceLanguage?: string; languages: string[] }) =>
    this.request<{ jobId: string }>("POST", "/jobs", req).then((r) => r.jobId);
  job = (id: string) => this.request<Job>("GET", `/jobs/${id}`);
  cancel = (id: string) => this.request<unknown>("POST", `/jobs/${id}/cancel`);
  edit = (id: string, lang: string, title: string, description: string) =>
    this.request<JobLang>("PUT", `/jobs/${id}/translations`, { lang, title, description });
  publish = (id: string, languages: string[], replace: string[]) =>
    this.request<PublishResult>("POST", `/jobs/${id}/publish`, { languages, replace });
  restore = (videoId: string, backupId: string) =>
    this.request<PublishResult>("POST", `/videos/${encodeURIComponent(videoId)}/restore`, { backupId });
  save = (id: string) => this.request<{ folder: string }>("POST", `/jobs/${id}/save`);
  openFolder = (folder: string) => this.request<unknown>("POST", "/open-folder", { folder });
  setKeepWords = (keepWords: string[]) => this.request<{ keepWords: string[] }>("PUT", "/settings", { keepWords });
  authUrl = () => this.request<{ url: string }>("GET", "/auth/url").then((r) => r.url);
  signOut = (channelId: string) => this.request<unknown>("POST", "/auth/signout", { channelId });

  /**
   * Streams a job's progress. onSnapshot gets the whole job first and when it
   * ends; onLang gets each language as it changes. Resolves when done.
   */
  async watch(id: string, onSnapshot: (j: Job) => void, onLang: (l: JobLang) => void, signal?: AbortSignal): Promise<void> {
    const code = await this.opts.code();
    let res: Response;
    try {
      res = await this.fetchFn(`${this.base}/jobs/${id}/events`, { headers: { Authorization: `Bearer ${code}` }, signal });
    } catch (e) {
      if (signal?.aborted) return;
      throw new HelperDown();
    }
    if (!res.ok || !res.body) {
      throw new ApiError(res.status, "error", "Could not follow the progress. Try again.");
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    const parser = new SseParser();
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      for (const ev of parser.push(decoder.decode(value, { stream: true }))) {
        const data = JSON.parse(ev.data);
        if (ev.event === "lang") onLang(data as JobLang);
        else if (ev.event === "snapshot") onSnapshot(data as Job);
        else if (ev.event === "done") {
          onSnapshot(data as Job);
          return;
        }
      }
    }
  }
}
