import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import {
  Api,
  ApiError,
  HelperDown,
  type Channel,
  type Health,
  type Job,
  type JobLang,
  type Language,
  type PublishResult,
  type Upload,
  type Video,
} from "./lib/api";
import { screenFor, type HealthResult } from "./lib/state";
import { isStudio, pickStudioTab, videoIdFromUrl } from "./lib/url";
import { askStudioAccess, readFromStudio, type StudioFields } from "./lib/studio";
import { askNative } from "./lib/native";
import {
  Action,
  CopyButton,
  CopyCode,
  Heading,
  ReadsAs,
  LanguageChips,
  MoreLanguages,
  Settings,
  Shell,
  StatusLine,
  type ChipLang,
} from "./components";

const params = new URLSearchParams(location.search);
const overrideVideo = params.get("video");
const overridePort = params.get("port");

async function storedCode(): Promise<string> {
  try {
    const got = await chrome.storage.local.get("pairingCode");
    return (got.pairingCode as string | undefined) ?? "";
  } catch {
    return "";
  }
}

const api = new Api({ base: overridePort ? `http://127.0.0.1:${overridePort}` : undefined, code: storedCode });

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function messageOf(e: unknown): string {
  if (e instanceof ApiError || e instanceof HelperDown) return e.message;
  return "Something went wrong. Try again.";
}

/** Opens a page in a new tab next to Studio. */
async function openTab(url: string) {
  try {
    await chrome.tabs.create({ url });
  } catch {
    window.open(url, "_blank");
  }
}

/** The tab Open in Studio opened last, so the next click reuses it. */
let studioTab: number | null = null;

/** Shows a Studio page in a Studio tab that is already open, else in one new tab that later clicks reuse. */
async function openInStudio(url: string) {
  try {
    const id = pickStudioTab(await chrome.tabs.query({ currentWindow: true }), studioTab);
    if (id !== null) {
      await chrome.tabs.update(id, { url, active: true });
      studioTab = id;
      return;
    }
    studioTab = (await chrome.tabs.create({ url })).id ?? null;
  } catch {
    window.open(url, "_blank");
  }
}

type Stage = "running" | "done" | "published";

interface Draft {
  title: string;
  description: string;
  error?: string;
}

export function App() {
  const [health, setHealth] = useState<HealthResult>({ kind: "pending" });
  const [tabVideo, setTabVideo] = useState<string | null>(overrideVideo);
  const [tabId, setTabId] = useState<number | null>(null);
  const [pickedVideo, setPickedVideo] = useState<string | null>(null);
  const [pasteMode, setPasteMode] = useState(false);
  const [languages, setLanguages] = useState<Language[]>([]);

  const [job, setJob] = useState<Job | null>(null);
  const [stage, setStage] = useState<Stage | null>(null);
  const [jobVideo, setJobVideo] = useState<Video | null>(null);
  const [replace, setReplace] = useState<string[]>([]);
  const [published, setPublished] = useState<PublishResult | null>(null);
  const currentJob = useRef<string | null>(null);
  const stream = useRef<AbortController | null>(null);

  const videoId = pickedVideo ?? tabVideo;
  const h = health.kind === "ok" ? health.health : null;

  // When the helper is down or the code is missing, ask Chrome's native
  // host once to start it and hand over the code. Without the host (setup
  // not run) the panel shows the command or the code field instead.
  const nativeTried = useRef<{ start: boolean; code: boolean }>({ start: false, code: false });
  const [starting, setStarting] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const next = await api.health();
      setHealth({ kind: "ok", health: next });
    } catch (e) {
      const unpaired = e instanceof ApiError && (e.code === "pair" || e.status === 401 || e.status === 429);
      const which = unpaired ? "code" : "start";
      if (!overridePort && !nativeTried.current[which]) {
        nativeTried.current[which] = true;
        setStarting(which === "start");
        const reply = await askNative(which);
        setStarting(false);
        if (reply?.code) {
          await chrome.storage.local.set({ pairingCode: reply.code });
          if (reply.running) return refresh();
        }
      }
      setHealth({ kind: unpaired ? "unpaired" : "down" });
    }
  }, []);

  // Health: quickly while waiting for something to be fixed, slowly otherwise.
  // While unpaired it waits for the code instead of asking with a wrong one.
  const waiting = health.kind === "down" || health.kind === "pending" || (h !== null && (h.model.state !== "ready" || (!h.signedIn && !pasteMode)));
  useEffect(() => {
    if (health.kind === "unpaired") return;
    if (health.kind === "pending") refresh();
    const t = setInterval(refresh, waiting ? 3000 : 20000);
    return () => clearInterval(t);
  }, [refresh, health.kind, waiting]);

  // Follow the active tab of this window, unless ?video= is given.
  useEffect(() => {
    if (overrideVideo) return;
    let windowId: number | undefined;
    const read = async () => {
      try {
        const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
        windowId = tab?.windowId;
        const id = isStudio(tab?.url) ? videoIdFromUrl(tab?.url) : null;
        setTabVideo(id);
        setTabId(id && tab?.id !== undefined ? tab.id : null);
      } catch {
        setTabVideo(null);
      }
    };
    read();
    const onActivated = (info: chrome.tabs.OnActivatedInfo) => {
      if (windowId === undefined || info.windowId === windowId) read();
    };
    const onUpdated = (_id: number, info: chrome.tabs.OnUpdatedInfo, tab: chrome.tabs.Tab) => {
      if (info.url && tab.active && (windowId === undefined || tab.windowId === windowId)) read();
    };
    chrome.tabs.onActivated.addListener(onActivated);
    chrome.tabs.onUpdated.addListener(onUpdated);
    return () => {
      chrome.tabs.onActivated.removeListener(onActivated);
      chrome.tabs.onUpdated.removeListener(onUpdated);
    };
  }, []);

  const clearJob = useCallback(() => {
    stream.current?.abort();
    stream.current = null;
    currentJob.current = null;
    setJob(null);
    setStage(null);
    setPublished(null);
  }, []);

  // Opening another video in Studio ends a finished job for a different
  // video. Other tabs (or pasted text) leave the work on screen.
  useEffect(() => {
    if (!tabVideo) return;
    setPickedVideo(null);
    if (stage && stage !== "running" && job?.videoId && job.videoId !== tabVideo) clearJob();
  }, [tabVideo]); // eslint-disable-line react-hooks/exhaustive-deps

  // The language list; asked for again on every health answer until it arrives.
  useEffect(() => {
    if (h && languages.length === 0) api.languages("").then(setLanguages, () => undefined);
  }, [h, languages.length]);

  const screen = screenFor({
    health,
    pasteMode,
    videoId,
    job: stage ? { stage, fromVideo: Boolean(job?.videoId) } : null,
  });

  // Follows one job until it ends. Events from an older job are ignored. If
  // the stream breaks or ends early, it asks for the job every two seconds.
  const follow = (id: string) => {
    const ctrl = new AbortController();
    stream.current = ctrl;
    const mine = () => currentJob.current === id && !ctrl.signal.aborted;
    const settle = (j: Job) => {
      if (!mine()) return;
      setJob(j);
      if (j.state === "done") setStage("done");
      if (j.state === "cancelled") clearJob();
    };
    const poll = async () => {
      while (mine()) {
        const j = await api.job(id).catch(() => null);
        if (!mine()) return;
        if (j) {
          settle(j);
          if (j.state !== "running") return;
        }
        await new Promise((r) => setTimeout(r, 2000));
      }
    };
    api
      .watch(
        id,
        settle,
        (l) => {
          if (mine()) setJob((cur) => (cur && cur.id === id ? { ...cur, languages: cur.languages.map((x) => (x.code === l.code ? { ...x, ...l } : x)) } : cur));
        },
        ctrl.signal,
      )
      .then(poll, poll);
  };

  const startJob = async (req: { videoId?: string; title?: string; description?: string; languages: string[] }, video: Video | null, replaceCodes: string[]) => {
    const id = await api.startJob(req);
    stream.current?.abort();
    currentJob.current = id;
    setJobVideo(video);
    setReplace(replaceCodes);
    setPublished(null);
    setStage("running");
    const first = await api.job(id).catch(() => null);
    if (first && currentJob.current === id) setJob(first);
    follow(id);
  };

  const signIn = async () => {
    const url = await api.authUrl();
    if (url !== "about:blank") openTab(url);
  };

  const settings =
    h && ["home", "video", "paste", "review", "published", "copy"].includes(screen) ? (
      <Settings
        health={h}
        onKeepWords={async (words) => {
          await api.setKeepWords(words);
          await refresh();
        }}
        onSignOut={async (c: Channel) => {
          await api.signOut(c.id);
          await refresh();
        }}
        onAddChannel={signIn}
      />
    ) : null;
  const footer = (
    <>
      {settings}
      <StatusLine health={h} />
    </>
  );

  switch (screen) {
    case "loading":
      return <Shell>{starting && <p role="status">Starting the helper…</p>}</Shell>;
    case "helper":
      return (
        <HelperScreen
          onRetry={() => {
            nativeTried.current.start = false; // the host may have been installed since
            return refresh();
          }}
        />
      );
    case "pair":
      return <PairScreen onPaired={refresh} />;
    case "model":
      return <ModelScreen health={h!} onRetry={refresh} footer={footer} />;
    case "signin":
      return <SignInScreen health={h!} onSignIn={signIn} onPaste={() => setPasteMode(true)} footer={footer} />;
    case "home":
      return <HomeScreen onPick={setPickedVideo} onSignIn={signIn} footer={footer} />;
    case "paste":
      return (
        <PasteScreen
          key={tabId ?? "none"}
          studioTab={tabId}
          languages={languages}
          footer={footer}
          onTranslate={(title, description, codes) => startJob({ title, description, languages: codes }, null, [])}
        />
      );
    case "video":
      return (
        <VideoScreen
          key={videoId!}
          videoId={videoId!}
          languages={languages}
          health={h!}
          picked={pickedVideo !== null}
          onBack={() => setPickedVideo(null)}
          onSignIn={signIn}
          onTranslate={(video, codes) =>
            startJob(
              { videoId: video.id, languages: codes },
              video,
              codes.filter((c) => video.languages.some((l) => l.code === c)),
            )
          }
          footer={footer}
        />
      );
    case "translating":
      return (
        <TranslatingScreen
          job={job}
          onCancel={async () => {
            if (job) await api.cancel(job.id).catch(() => undefined);
            clearJob();
          }}
          footer={<StatusLine health={h} />}
        />
      );
    case "review":
      return (
        <ReviewScreen
          key={job!.id}
          job={job!}
          sourceName={sourceName(job!, languages)}
          video={jobVideo}
          replace={replace}
          videosLeft={h?.videosLeftToday ?? 999}
          onPublished={(res) => {
            setPublished(res);
            setStage("published");
          }}
          onStartOver={clearJob}
          footer={footer}
        />
      );
    case "published":
      return <PublishedScreen key={job!.id} job={job!} result={published!} onStartOver={clearJob} footer={footer} />;
    case "copy":
      return (
        <CopyScreen
          key={job!.id}
          job={job!}
          sourceName={sourceName(job!, languages)}
          health={h!}
          onSignIn={() => {
            setPasteMode(false);
            clearJob();
          }}
          footer={footer}
        />
      );
  }
}

/** The English name of the job's source language ("English" when unknown). */
function sourceName(job: Job, languages: Language[]): string {
  const code = job.sourceLanguage || "en";
  return languages.find((l) => l.code === code || code.startsWith(l.code + "-"))?.english ?? (code.startsWith("en") ? "English" : "the video's language");
}

/* 1 */
function HelperScreen(props: { onRetry: () => unknown }) {
  return (
    <Shell>
      <Heading>Start the helper to continue</Heading>
      <p>Run this in a terminal, then come back.</p>
      <CopyCode text="tongues serve" />
      <Action kind="primary" className="push" busy="Checking…" onRun={props.onRetry}>
        Try again
      </Action>
    </Shell>
  );
}

/* 2 */
function PairScreen(props: { onPaired: () => void }) {
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const submit = async () => {
    const clean = code.toUpperCase().replace(/[^A-Z0-9]/g, "");
    if (clean.length !== 8) {
      setError("The code has eight letters and numbers, like ABCD-EFGH.");
      return;
    }
    setError("");
    const formatted = `${clean.slice(0, 4)}-${clean.slice(4)}`;
    await chrome.storage.local.set({ pairingCode: formatted });
    try {
      await api.health();
      props.onPaired();
    } catch (e) {
      setError(e instanceof ApiError && e.status === 401 ? "That code does not match. Check the terminal and try again." : messageOf(e));
    }
  };
  return (
    <Shell>
      <Heading focus={false}>Enter the code from the helper</Heading>
      <p id="pair-hint">It is printed in the terminal where the helper runs.</p>
      <input
        className="field"
        aria-label="Pairing code"
        aria-describedby="pair-hint"
        placeholder="ABCD-EFGH"
        autoComplete="off"
        spellCheck={false}
        maxLength={9}
        value={code}
        onChange={(e) => setCode(e.target.value.toUpperCase())}
        onKeyDown={(e) => {
          if (e.key === "Enter") document.getElementById("connect")?.click(); // the same guarded click
        }}
        autoFocus
      />
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      <Action kind="primary" className="push" id="connect" busy="Connecting…" onRun={submit}>
        Connect
      </Action>
    </Shell>
  );
}

/* 3 */
function ModelScreen(props: { health: Health; onRetry: () => unknown; footer: ReactNode }) {
  const missing = props.health.model.state === "model-missing";
  return (
    <Shell footer={<StatusLine health={props.health} />}>
      <Heading>{missing ? "The model is not downloaded yet" : "Ollama is not running"}</Heading>
      <p>{missing ? "Download it once in a terminal, then come back." : "Start the Ollama app, or run this in a terminal."}</p>
      <CopyCode text={missing ? `ollama pull ${props.health.model.model}` : "ollama serve"} />
      <Action kind="primary" className="push" busy="Checking…" onRun={props.onRetry}>
        Try again
      </Action>
    </Shell>
  );
}

/* 4 */
function SignInScreen(props: { health: Health; onSignIn: () => Promise<void>; onPaste: () => void; footer: ReactNode }) {
  const [error, setError] = useState("");
  const [waiting, setWaiting] = useState(false);
  const signIn = async () => {
    setError("");
    try {
      await props.onSignIn();
      setWaiting(true);
    } catch (e) {
      setError(messageOf(e));
    }
  };
  return (
    <Shell footer={props.footer}>
      <Heading>How do you want to add translations?</Heading>
      <p>Sign in and they are published for you, or paste them yourself.</p>
      <div className="card choice">
        <span className="h">Publish for me</span>
        <p>Uses your own Google project. The sign-in stays on this computer and can be removed any time.</p>
      </div>
      <div className="card choice">
        <span className="h">I'll paste it myself</span>
        <p>No sign-in. You get the translations to copy or saved as a file.</p>
      </div>
      {waiting && <p role="status">Finish signing in in the new tab. This panel updates by itself.</p>}
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      <div className="actions">
        <Action kind="primary" busy="Opening the sign-in…" onRun={signIn}>
          Sign in to publish for me
        </Action>
        <Action kind="secondary" onRun={props.onPaste}>
          I'll paste it myself
        </Action>
      </div>
    </Shell>
  );
}

/* 5 */
function HomeScreen(props: { onPick: (id: string) => void; onSignIn: () => Promise<void>; footer: ReactNode }) {
  const [uploads, setUploads] = useState<Upload[] | null>(null);
  const [error, setError] = useState("");
  const [expired, setExpired] = useState(false);
  const [link, setLink] = useState("");
  useEffect(() => {
    api.uploads(6).then(setUploads, (e) => {
      setUploads([]);
      setError(messageOf(e));
      setExpired(e instanceof ApiError && e.code === "signin");
    });
  }, []);
  const tryLink = (value: string) => {
    const id = videoIdFromUrl(value);
    if (id) props.onPick(id);
    else if (value.trim()) setError("That does not look like a video link.");
  };
  return (
    <Shell footer={props.footer}>
      <Heading>Open a video in Studio</Heading>
      <p>Or pick one of your latest uploads.</p>
      {uploads && uploads.length > 0 && (
        <ul className="rows uploads" aria-label="Latest uploads">
          {uploads.map((u) => (
            <li key={u.id} style={{ display: "contents" }}>
              <button className="row" onClick={() => props.onPick(u.id)}>
                <span className="u">
                  <b>{u.title}</b>
                  <small>
                    {u.channelTitle} · {u.languages === 0 ? "no translations yet" : `has ${plural(u.languages, "language")}`}
                  </small>
                </span>
                <span className="chev" aria-hidden="true">
                  ›
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <input
        className="field"
        aria-label="Video link"
        placeholder="Or paste a video link"
        value={link}
        onChange={(e) => {
          setLink(e.target.value);
          setError("");
        }}
        onPaste={(e) => {
          const text = e.clipboardData.getData("text");
          if (videoIdFromUrl(text)) {
            e.preventDefault();
            tryLink(text);
          }
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") tryLink(link);
        }}
      />
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {expired && (
        <Action kind="primary" className="push" busy="Opening the sign-in…" onRun={() => props.onSignIn().catch((e) => setError(messageOf(e)))}>
          Sign in again
        </Action>
      )}
    </Shell>
  );
}

function chipLangs(codes: string[], languages: Language[], has: (code: string) => boolean): ChipLang[] {
  const byCode = new Map(languages.map((l) => [l.code, l]));
  return codes.map((code) => {
    const l = byCode.get(code);
    return { code, native: l?.native ?? code, english: l?.english ?? code, has: has(code) };
  });
}

/* Level 0: paste the text */
function PasteScreen(props: {
  studioTab: number | null;
  languages: Language[];
  onTranslate: (title: string, description: string, codes: string[]) => Promise<void>;
  footer: ReactNode;
}) {
  const popular = props.languages.filter((l) => l.popular).map((l) => l.code);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [extra, setExtra] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string> | null>(null);
  const [error, setError] = useState("");
  const [filled, setFilled] = useState<"no" | "yes" | "ask">("no");
  const fill = (f: StudioFields | null) => {
    if (!f) return false;
    setTitle(f.title);
    setDescription(f.description);
    setFilled("yes");
    return true;
  };
  // On a Studio video page, read the two fields so there is nothing to paste.
  useEffect(() => {
    if (props.studioTab === null) return;
    readFromStudio(props.studioTab).then((f) => {
      if (!fill(f)) setFilled("ask");
    });
  }, [props.studioTab]); // eslint-disable-line react-hooks/exhaustive-deps
  // Tick the languages picked last time; the twelve stay as chips.
  useEffect(() => {
    api.usedLanguages().then(
      (used) => {
        if (used.length === 0) return; // the twelve stay ticked
        setSelected((cur) => cur ?? new Set(used));
        setExtra((cur) => [...cur, ...used.filter((c) => !popular.includes(c) && !cur.includes(c))]);
      },
      () => undefined,
    );
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const chosen = selected ?? new Set(popular);
  const codes = [...popular, ...extra.filter((c) => !popular.includes(c))];
  const toggle = (c: string) => {
    const next = new Set(chosen);
    if (next.has(c)) next.delete(c);
    else next.add(c);
    setSelected(next);
  };
  // The helper finds the pasted text's language and leaves it out.
  const picked = codes.filter((c) => chosen.has(c));
  const count = picked.length;
  return (
    <Shell footer={props.footer}>
      <Heading>{filled === "yes" ? "Check the title and description" : "Paste the title and description"}</Heading>
      <p>{filled === "yes" ? "Filled in from the Studio page. Change anything, then translate." : "Copy them from the video's Details page in Studio."}</p>
      {filled === "ask" && props.studioTab !== null && (
        <Action
          kind="small"
          busy="Reading…"
          onRun={async () => {
            if ((await askStudioAccess()) && props.studioTab !== null && !fill(await readFromStudio(props.studioTab))) {
              setError("The title was not found on this page. Paste it instead.");
            }
          }}
        >
          Read them from the Studio page
        </Action>
      )}
      <input className="field" aria-label="Title" placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} />
      <textarea
        className="field"
        aria-label="Description"
        placeholder="Description"
        rows={5}
        value={description}
        onChange={(e) => setDescription(e.target.value)}
      />
      <div className="sect">
        <h2 style={{ fontSize: 14, margin: 0 }}>Translate into</h2>
        <span className="n">{count} selected</span>
      </div>
      <LanguageChips
        langs={chipLangs(codes, props.languages, () => false)}
        selected={chosen}
        onToggle={toggle}
      />
      <MoreLanguages
        all={props.languages}
        shown={new Set(codes)}
        onAdd={(c) => {
          setExtra([...extra, c]);
          setSelected(new Set([...chosen, c]));
        }}
      />
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      <Action
        kind="primary"
        className="push"
        busy="Starting…"
        disabled={!title.trim() || count === 0}
        onRun={() => {
          setError("");
          return props.onTranslate(title, description, picked).catch((e) => setError(messageOf(e)));
        }}
      >
        {count === 0 ? "Pick a language" : `Translate ${plural(count, "language")}`}
      </Action>
    </Shell>
  );
}

/* 6 */
function VideoScreen(props: {
  videoId: string;
  languages: Language[];
  health: Health;
  picked: boolean;
  onBack: () => void;
  onSignIn: () => Promise<void>;
  onTranslate: (video: Video, codes: string[]) => Promise<void>;
  footer: ReactNode;
}) {
  const [video, setVideo] = useState<Video | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [extra, setExtra] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [startError, setStartError] = useState("");

  const load = useCallback(() => {
    setError(null);
    return api.video(props.videoId).then(
      (v) => {
        setVideo(v);
        const has = new Set(v.languages.map((l) => l.code));
        setSelected(new Set(v.suggested.filter((c) => !has.has(c))));
      },
      (e) => setError(e instanceof ApiError ? e : new ApiError(0, "error", messageOf(e))),
    );
  }, [props.videoId]);
  useEffect(() => {
    load();
  }, [load]);

  const back = props.picked ? (
    <Action kind="small" onRun={props.onBack}>
      Back to your videos
    </Action>
  ) : null;

  if (error) {
    return (
      <Shell footer={props.footer}>
        <Heading>{error.code === "notfound" ? "This video is not on your channels" : "This video could not be opened"}</Heading>
        <p>{error.message}</p>
        {back}
        {startError && (
          <div className="error" role="alert">
            {startError}
          </div>
        )}
        {error.code === "signin" ? (
          <Action
            kind="primary"
            className="push"
            busy="Opening the sign-in…"
            onRun={() => {
              setStartError("");
              return props.onSignIn().catch((e) => setStartError(messageOf(e)));
            }}
          >
            Sign in again
          </Action>
        ) : (
          <Action kind="primary" className="push" busy="Checking…" onRun={load}>
            Try again
          </Action>
        )}
      </Shell>
    );
  }
  if (!video) {
    return (
      <Shell footer={props.footer}>
        <p role="status">Opening the video…</p>
      </Shell>
    );
  }

  const has = (c: string) => video.languages.some((l) => l.code === c);
  const codes = [...video.shown, ...extra.filter((c) => !video.shown.includes(c))];
  // Base subtag with the old codes YouTube still uses mapped, like the helper does.
  const old: Record<string, string> = { iw: "he", in: "id", ji: "yi", nb: "no", tl: "fil" };
  const base = (c: string) => {
    const b = c.split("-")[0].toLowerCase();
    return old[b] ?? b;
  };
  const isSource = (c: string) => !!video.defaultLanguage && base(c) === base(video.defaultLanguage);
  const chosen = codes.filter((c) => selected.has(c) && !isSource(c));
  const count = chosen.length;
  const summary = `${video.sourceName || "No language set"} · ${
    video.languages.length === 0 ? "no translations yet" : `has ${plural(video.languages.length, "language")}`
  }`;
  const toggle = (c: string) => {
    const next = new Set(selected);
    if (next.has(c)) next.delete(c);
    else next.add(c);
    setSelected(next);
  };

  if (!video.defaultLanguage) {
    return (
      <Shell footer={props.footer}>
        <div className="video">
          <div className="t">{video.title}</div>
          <div className="s">{summary}</div>
        </div>
        <Heading>Set the video's language first</Heading>
        <p>In Studio: Details, Show more, Title and description language. Then press Check again.</p>
        {back}
        <Action kind="primary" className="push" busy="Checking…" onRun={load}>
          Check again
        </Action>
      </Shell>
    );
  }

  return (
    <Shell footer={props.footer}>
      <div className="video">
        <div className="t">{video.title}</div>
        <div className="s">{summary}</div>
      </div>
      <div className="sect">
        <Heading>Translate into</Heading>
        <span className="n" aria-live="polite">
          {count} selected
        </span>
      </div>
      <LanguageChips
        langs={chipLangs(codes, props.languages, has)}
        selected={selected}
        onToggle={toggle}
      />
      <MoreLanguages
        all={props.languages.filter((l) => !isSource(l.code))}
        shown={new Set(codes)}
        onAdd={(c) => {
          setExtra([...extra, c]);
          setSelected(new Set([...selected, c]));
        }}
      />
      {props.health.videosLeftToday < 20 && <p>You can publish {plural(props.health.videosLeftToday, "more video")} today.</p>}
      {back}
      {startError && (
        <div className="error" role="alert">
          {startError}
        </div>
      )}
      <Action
        kind="primary"
        className="push"
        busy="Starting…"
        disabled={count === 0}
        onRun={() => {
          setStartError("");
          return props.onTranslate(video, chosen).catch((e) => setStartError(messageOf(e)));
        }}
      >
        {count === 0 ? "Pick a language" : `Translate ${plural(count, "language")}`}
      </Action>
    </Shell>
  );
}

function StatusText(props: { lang: JobLang }) {
  switch (props.lang.status) {
    case "ready":
      return (
        <span className="st ok">
          <span className="dot" />
          Ready
        </span>
      );
    case "check":
      return <span className="st check">⚠ Check this one</span>;
    case "translating":
      return (
        <span className="st busy">
          <span className="spin" aria-hidden="true" />
          Translating
        </span>
      );
    case "checking":
      return (
        <span className="st busy">
          <span className="spin" aria-hidden="true" />
          Checking
        </span>
      );
    case "failed":
      return <span className="st err">Could not translate</span>;
    case "cancelled":
      return <span className="st">Stopped</span>;
    default:
      return <span className="st">Waiting</span>;
  }
}

/* 7 */
function TranslatingScreen(props: { job: Job | null; onCancel: () => unknown; footer: ReactNode }) {
  const langs = props.job?.languages ?? [];
  const finished = langs.filter((l) => ["ready", "check", "failed"].includes(l.status)).length;
  if (langs.length > 20) {
    // Many languages: one bar instead of a long list of rows.
    const current = langs.find((l) => l.status === "translating") ?? langs.find((l) => l.status === "checking");
    const spoken = finished === langs.length ? finished : Math.floor(finished / 10) * 10;
    return (
      <Shell footer={props.footer}>
        <Heading>Translating</Heading>
        <p>{finished === 0 ? "Warming up the model. The first run takes a few seconds longer." : "Each language is checked before you see it."}</p>
        <div className="progress">
          <div
            className="bar"
            role="progressbar"
            aria-label="Languages translated"
            aria-valuemin={0}
            aria-valuemax={langs.length}
            aria-valuenow={finished}
            aria-valuetext={`${finished} of ${langs.length}`}
          >
            <span style={{ width: `${(100 * finished) / langs.length}%` }} />
          </div>
          <div className="meta">
            <span aria-hidden="true">
              {finished} of {langs.length}
            </span>
            {current && (
              <span>
                Now: <span lang={current.code}>{current.name}</span>
              </span>
            )}
          </div>
        </div>
        <p className="sr-only" aria-live="polite">
          {/* Spoken every ten languages, not on each one. */}
          {spoken > 0 && `${spoken} of ${langs.length} translated`}
        </p>
        <Action kind="secondary" className="push" busy="Stopping…" onRun={props.onCancel}>
          Cancel
        </Action>
      </Shell>
    );
  }
  return (
    <Shell footer={props.footer}>
      <Heading>Translating</Heading>
      <p>{finished === 0 ? "Warming up the model. The first run takes a few seconds longer." : "Each language is checked before you see it."}</p>
      <ul className="rows" aria-live="polite" aria-label="Progress">
        {langs.map((l) => (
          <li className="row" key={l.code} aria-atomic="true">
            <span lang={l.code}>{l.name}</span>
            <StatusText lang={l} />
          </li>
        ))}
      </ul>
      <div className="actions">
        <button className="primary" disabled>
          Translating {Math.min(finished + 1, Math.max(langs.length, 1))} of {langs.length}
        </button>
        <Action kind="secondary" busy="Stopping…" onRun={props.onCancel}>
          Cancel
        </Action>
      </div>
    </Shell>
  );
}

function TitleCounter(props: { id: string; title: string }) {
  const n = [...props.title].length;
  return (
    <div className="meta">
      <span>Title</span>
      <span id={props.id} className={n > 100 ? "over" : undefined}>
        {n} of 100
      </span>
    </div>
  );
}

/* 8 */
function ReviewScreen(props: {
  job: Job;
  sourceName: string;
  video: Video | null;
  replace: string[];
  videosLeft: number;
  onPublished: (r: PublishResult) => void;
  onStartOver: () => void;
  footer: ReactNode;
}) {
  const usable = props.job.languages.filter((l) => l.status === "ready" || l.status === "check");
  const failed = props.job.languages.filter((l) => l.status === "failed");
  const [ticked, setTicked] = useState<Set<string>>(() => new Set(usable.filter((l) => l.status === "ready").map((l) => l.code)));
  const [drafts, setDrafts] = useState<Record<string, Draft>>(() =>
    Object.fromEntries(usable.map((l) => [l.code, { title: l.title, description: l.description }])),
  );
  const [showOriginal, setShowOriginal] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [focusId, setFocusId] = useState("");
  const [error, setError] = useState("");

  const save = async (code: string) => {
    const l = props.job.languages.find((x) => x.code === code)!;
    const d = draftOf(l);
    try {
      await api.edit(props.job.id, code, d.title, d.description);
      setDrafts((cur) => ({ ...cur, [code]: { ...d, error: undefined } }));
      return true;
    } catch (e) {
      setDrafts((cur) => ({ ...cur, [code]: { ...d, error: messageOf(e) } }));
      return false;
    }
  };
  const draftOf = (l: JobLang): Draft => drafts[l.code] ?? { title: l.title, description: l.description };
  const dirty = (l: JobLang) => draftOf(l).title !== l.title || draftOf(l).description !== l.description;

  const publish = async () => {
    setError("");
    for (const l of usable) {
      if (ticked.has(l.code) && dirty(l) && !(await save(l.code))) {
        setError("Fix the marked language first.");
        return;
      }
    }
    try {
      const codes = usable.filter((l) => ticked.has(l.code)).map((l) => l.code);
      const res = await api.publish(
        props.job.id,
        codes,
        props.replace.filter((c) => codes.includes(c)),
      );
      props.onPublished(res);
    } catch (e) {
      setError(messageOf(e));
    }
  };

  useEffect(() => {
    // Opening a row moves focus into its card; folding it moves focus back.
    if (focusId) document.getElementById(focusId)?.focus();
  }, [focusId, expanded]);
  const toggleRow = (code: string, open: boolean) => {
    const next = new Set(expanded);
    if (open) next.add(code);
    else next.delete(code);
    setExpanded(next);
    setFocusId(open ? `t-${code}` : `o-${code}`);
  };

  const count = ticked.size;
  const toCheck = usable.filter((l) => l.status === "check").length;
  const allTicked = usable.length > 0 && usable.every((l) => ticked.has(l.code));
  const tickAll = () => setTicked(allTicked ? new Set() : new Set(usable.map((l) => l.code)));
  // With many languages, the ones to check come first as full cards and the
  // ready ones fold into one row each; with a dozen it looks as before.
  const many = usable.length > 20;
  const checks = usable.filter((l) => l.status === "check");
  const readies = usable.filter((l) => l.status === "ready");
  const card = (l: JobLang) => {
    const d = draftOf(l);
    const titleId = `t-${l.code}`;
    const counterId = `c-${l.code}`;
    return (
      <section className="card" key={l.code} aria-label={l.english}>
        <div className="h">
          <span lang={l.code}>{l.name}</span>
          <label>
            <span className="sr-only">Publish {l.english}</span>
            <input
              type="checkbox"
              checked={ticked.has(l.code)}
              onChange={(e) => {
                const next = new Set(ticked);
                if (e.target.checked) next.add(l.code);
                else next.delete(l.code);
                setTicked(next);
              }}
            />
          </label>
        </div>
        {l.status === "check" && <div className="warn">⚠ Check this one: {l.note}.</div>}
        <input
          id={titleId}
          className="field"
          lang={l.code}
          aria-label={`${l.english} title`}
          aria-describedby={counterId}
          value={d.title}
          onChange={(e) => setDrafts({ ...drafts, [l.code]: { ...d, title: e.target.value } })}
          onBlur={() => dirty(l) && save(l.code)}
        />
        <TitleCounter id={counterId} title={d.title} />
        <ReadsAs
          sourceName={props.sourceName}
          title={l.backTitle}
          line={l.backLine}
          added={l.added}
          addedLine={l.addedLine}
          stale={l.edited || d.title !== l.title}
        />
        <textarea
          className="field"
          lang={l.code}
          aria-label={`${l.english} description`}
          rows={4}
          value={d.description}
          onChange={(e) => setDrafts({ ...drafts, [l.code]: { ...d, description: e.target.value } })}
          onBlur={() => dirty(l) && save(l.code)}
        />
        {d.error && (
          <div className="error" role="alert">
            {d.error}
          </div>
        )}
        <Action
          kind="small"
          expanded={showOriginal.has(l.code)}
          onRun={() => {
            const next = new Set(showOriginal);
            if (next.has(l.code)) next.delete(l.code);
            else next.add(l.code);
            setShowOriginal(next);
          }}
        >
          {showOriginal.has(l.code) ? "Hide original" : "Show original"}
        </Action>
        {showOriginal.has(l.code) && (
          <div className="original">
            {props.job.sourceTitle}
            {"\n\n"}
            {props.job.sourceDescription}
          </div>
        )}
        {many && expanded.has(l.code) && (
          <Action kind="small" expanded label={`Show less of ${l.english}`} onRun={() => toggleRow(l.code, false)}>
            Show less
          </Action>
        )}
      </section>
    );
  };
  return (
    <Shell footer={props.footer}>
      {props.video && (
        <div className="video">
          <div className="t">{props.video.title}</div>
          <div className="s">{props.video.channelTitle}</div>
        </div>
      )}
      <Heading>Look it over</Heading>
      {many ? (
        // "71 ready, 14 to check · Tick all": one line, the link right after it.
        <p>
          {`${props.job.ready} ready${toCheck > 0 ? `, ${toCheck} to check` : ""} · `}
          <Action kind="small" className="inline" onRun={tickAll}>
            {allTicked ? "Untick all" : "Tick all"}
          </Action>
        </p>
      ) : (
        <div className="sect">
          <p>
            {usable.length === 0
              ? "Nothing could be translated this time."
              : `${props.job.ready} ready${toCheck > 0 ? `, ${toCheck} to check` : ""}. Edit anything, then publish.`}
          </p>
          {usable.length > 1 && (
            <Action kind="small" onRun={tickAll}>
              {allTicked ? "Untick all" : "Tick all"}
            </Action>
          )}
        </div>
      )}
      {many ? (
        <>
          {checks.map(card)}
          {readies.length > 0 && (
            <ul className="rows compact" aria-label="Ready languages">
              {readies.map((l) =>
                expanded.has(l.code) ? (
                  <li key={l.code} className="expanded">
                    {card(l)}
                  </li>
                ) : (
                  <li key={l.code} className="row compact-row">
                    <input
                      type="checkbox"
                      checked={ticked.has(l.code)}
                      aria-label={`Publish ${l.english}`}
                      onChange={(e) => {
                        const next = new Set(ticked);
                        if (e.target.checked) next.add(l.code);
                        else next.delete(l.code);
                        setTicked(next);
                      }}
                    />
                    <button id={`o-${l.code}`} className="compact-open" aria-expanded="false" onClick={() => toggleRow(l.code, true)}>
                      <span className="cname" lang={l.code}>
                        {l.name}
                      </span>
                      <span className="ctitle" lang={l.code}>
                        {draftOf(l).title}
                      </span>
                      <ReadsAs inline sourceName={props.sourceName} title={l.backTitle} line={l.backLine} added={l.added} addedLine={l.addedLine} />
                      <span className="sr-only">, open to edit</span>
                    </button>
                  </li>
                ),
              )}
            </ul>
          )}
        </>
      ) : (
        usable.map(card)
      )}
      {failed.length > 0 && (
        <>
          <p>Could not translate: {failed.map((l) => l.name).join(", ")}.</p>
          <Action kind="small" onRun={props.onStartOver}>
            Translate again
          </Action>
        </>
      )}
      <div className="footer">
        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        <Action kind="primary" busy="Publishing…" disabled={count === 0 || props.videosLeft === 0} onRun={publish}>
          {count === 0 ? "Tick a language to publish" : `Publish ${plural(count, "language")}`}
        </Action>
      </div>
    </Shell>
  );
}

/* 9 */
function PublishedScreen(props: { job: Job; result: PublishResult; onStartOver: () => void; footer: ReactNode }) {
  const [undone, setUndone] = useState(false);
  const [error, setError] = useState("");
  const sent = new Set(props.result.results.map((r) => r.lang));
  const notSent = props.job.languages.filter((l) => l.status === "check" && !sent.has(l.code));
  const word = (o: string) => {
    switch (o) {
      case "kept":
        return <span className="st">Kept the existing one</span>;
      case "failed":
        return <span className="st err">Did not save, try again</span>;
      case "same":
        return <span className="st ok">✓ Already there</span>;
      default:
        return (
          <span className="st ok">
            <span aria-hidden="true">✓</span>
            <span className="sr-only">Published</span>
          </span>
        );
    }
  };
  if (undone) {
    return (
      <Shell footer={props.footer}>
        <Heading key="undone">Undone</Heading>
        <p>The video's translations are back to how they were.</p>
        <div className="actions">
          <Action kind="primary" busy="Opening…" onRun={() => openInStudio(props.result.studioUrl)}>
            Open in Studio
          </Action>
          <Action kind="secondary" onRun={props.onStartOver}>
            Translate again
          </Action>
        </div>
      </Shell>
    );
  }
  return (
    <Shell footer={props.footer}>
      <Heading>Published</Heading>
      <p>{plural(props.result.published, "language")} added to this video.</p>
      <ul className="rows" aria-label="What was published">
        {props.result.results.map((r) => (
          <li className="row" key={r.lang}>
            <span lang={r.lang}>{r.name}</span>
            {word(r.outcome)}
          </li>
        ))}
        {notSent.map((l) => (
          <li className="row" key={l.code}>
            <span lang={l.code}>{l.name}</span>
            <span className="st check">Not published, check it first</span>
          </li>
        ))}
      </ul>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      <div className="actions">
        <Action kind="primary" busy="Opening…" onRun={() => openInStudio(props.result.studioUrl)}>
          Open in Studio
        </Action>
        {props.result.backupId && (
          <Action
            kind="secondary"
            busy="Undoing…"
            onRun={async () => {
              setError("");
              try {
                await api.restore(props.result.videoId, props.result.backupId);
                setUndone(true);
              } catch (e) {
                setError(messageOf(e));
              }
            }}
          >
            Undo
          </Action>
        )}
      </div>
    </Shell>
  );
}

/* 9b */
function CopyScreen(props: { job: Job; sourceName: string; health: Health; onSignIn: () => void; footer: ReactNode }) {
  const usable = props.job.languages.filter((l) => l.title);
  const [folder, setFolder] = useState("");
  const [error, setError] = useState("");
  const saveAll = async () => {
    setError("");
    try {
      const r = await api.save(props.job.id);
      setFolder(r.folder);
    } catch (e) {
      setError(messageOf(e));
    }
  };
  return (
    <Shell footer={props.footer}>
      <Heading>Copy or save</Heading>
      <p>{folder ? "Saved. Paste each one into Studio's Languages tab." : "Paste each one into the video's Languages tab in Studio."}</p>
      {usable.map((l) => (
        <section className="card" key={l.code} aria-label={l.english}>
          <div className="h">
            <span lang={l.code}>{l.name}</span>
            <CopyButton text={`${l.title}\n\n${l.description}`} label="Copy both" what={`${l.english} title and description`} />
          </div>
          {l.status === "check" && <div className="warn">⚠ Check this one: {l.note}.</div>}
          <input className="field" lang={l.code} readOnly value={l.title} aria-label={`${l.english} title`} />
          <div className="meta">
            <span>Title</span>
            <CopyButton text={l.title} label="Copy" what={`${l.english} title`} />
          </div>
          <ReadsAs sourceName={props.sourceName} title={l.backTitle} line={l.backLine} added={l.added} addedLine={l.addedLine} />
          {l.description && (
            <>
              <textarea className="field" lang={l.code} readOnly rows={4} value={l.description} aria-label={`${l.english} description`} />
              <div className="meta">
                <span>Description</span>
                <CopyButton text={l.description} label="Copy" what={`${l.english} description`} />
              </div>
            </>
          )}
        </section>
      ))}
      <div className="footer">
        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        {folder ? (
          <Action kind="primary" busy="Opening…" onRun={() => api.openFolder(folder).catch((e) => setError(messageOf(e)))}>
            Open folder
          </Action>
        ) : (
          <Action kind="primary" busy="Saving…" onRun={saveAll}>
            Save all to a folder
          </Action>
        )}
        {props.health.canSignIn && (
          <Action kind="secondary" onRun={props.onSignIn}>
            Sign in to publish next time
          </Action>
        )}
      </div>
    </Shell>
  );
}

