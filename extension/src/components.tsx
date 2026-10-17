import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { Channel, Health, Language } from "./lib/api";

/** Splits text into plain parts and marked added words (whole words, any case). */
export function markWords(text: string, words: string[] | undefined): { text: string; added: boolean }[] {
  if (!words || words.length === 0) return [{ text, added: false }];
  const escaped = words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  const re = new RegExp(`(?<![\\p{L}\\p{N}])(${escaped.join("|")})(?![\\p{L}\\p{N}])`, "giu");
  const out: { text: string; added: boolean }[] = [];
  let last = 0;
  for (const m of text.matchAll(re)) {
    const i = m.index ?? 0;
    if (i > last) out.push({ text: text.slice(last, i), added: false });
    out.push({ text: m[0], added: true });
    last = i + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last), added: false });
  return out;
}

/** "Reads in English as: ..." under a translated title, added words marked. */
export function ReadsAs(props: {
  sourceName: string;
  title?: string;
  line?: string;
  added?: string[];
  addedLine?: string[];
  stale?: boolean;
  inline?: boolean; // inside a button, where a paragraph is not allowed
}) {
  if (!props.title) return null;
  const joiner = /[.!?。！？]$/.test(props.title.trim()) ? " " : ". ";
  const text = props.line ? `${props.title.trim()}${joiner}${props.line}` : props.title;
  const marked = (props.added?.length ?? 0) > 0;
  const Tag = props.inline ? "span" : "p";
  // Two lines when nothing is marked; in full when words are marked, so no
  // mark is ever cut off. Screen readers get the whole text either way.
  return (
    <Tag className={marked ? "reads marked" : "reads"} title={marked ? undefined : text}>
      Reads in {props.sourceName} as:{" "}
      {markWords(text, props.added).map((part, i) =>
        part.added ? (
          <mark key={i}>
            <span className="sr-only">added word: </span>
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
      {props.addedLine && props.addedLine.length > 0 && (
        <span className="desc-adds"> · description adds: {props.addedLine.map((w) => w.toLowerCase()).join(", ")}</span>
      )}
      {props.stale && <span className="stale"> (before your edit)</span>}
    </Tag>
  );
}

export function Mark() {
  // Our own mark: a speech bubble drawn as twelve ticks (the icon without its tile).
  return (
    <svg className="mark" viewBox="18 12 98 98" aria-hidden="true">
      <circle
        cx="66"
        cy="56"
        r="36"
        fill="none"
        stroke="var(--accent)"
        strokeWidth="11"
        strokeLinecap="round"
        strokeDasharray="7.92 10.93"
        transform="rotate(-96.30 66 56)"
      />
      <path d="M51.2 91.0 Q44.8 98.4 32.4 105.7 Q32.4 87.8 32.4 73.8 Z" fill="var(--accent)" stroke="var(--accent)" strokeWidth="5" strokeLinejoin="round" />
    </svg>
  );
}

export function Shell(props: { children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="panel">
      <header className="top">
        <Mark />
        <span className="brand">Channel Tongues</span>
      </header>
      <main className="body">{props.children}</main>
      {props.footer}
    </div>
  );
}

/** Heading that takes focus when its screen appears, so it is announced. */
export function Heading(props: { children: ReactNode; focus?: boolean }) {
  const ref = useRef<HTMLHeadingElement>(null);
  const focus = props.focus ?? true;
  useEffect(() => {
    if (focus) ref.current?.focus();
  }, [focus]);
  return (
    <h1 ref={ref} tabIndex={-1}>
      {props.children}
    </h1>
  );
}

export function CopyCode(props: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="code">
      <span>{props.text}</span>
      <button
        className="link"
        onClick={() => {
          navigator.clipboard.writeText(props.text).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
        aria-label={`Copy the command ${props.text}`}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

export function CopyButton(props: { text: string; label: string; what: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      className="link"
      aria-label={`${props.label}: ${props.what}`}
      onClick={() =>
        navigator.clipboard.writeText(props.text).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        })
      }
    >
      {copied ? "Copied" : props.label}
    </button>
  );
}

export interface ChipLang {
  code: string;
  native: string;
  english: string;
  has: boolean; // the video already has it
}

export function LanguageChips(props: {
  langs: ChipLang[];
  selected: Set<string>;
  onToggle: (code: string) => void;
}) {
  return (
    <div className="chips" role="group" aria-label="Languages to translate into">
      {props.langs.map((l) => {
        const on = props.selected.has(l.code);
        return (
          <button key={l.code} className={`chip${l.has && !on ? " has" : ""}`} aria-pressed={on} onClick={() => props.onToggle(l.code)}>
            <span lang={l.code}>{l.native}</span>
            {l.native !== l.english && <span className="sr-only"> ({l.english})</span>}
            {l.has && !on && <span className="tag">has it</span>}
          </button>
        );
      })}
    </div>
  );
}

/** "More languages": a search box over every language, with type-ahead. */
export function MoreLanguages(props: { all: Language[]; shown: Set<string>; onAdd: (code: string) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    return props.all
      .filter((l) => !props.shown.has(l.code))
      .filter((l) => !q || l.english.toLowerCase().includes(q) || l.native.toLowerCase().includes(q) || l.code.toLowerCase() === q)
      .slice(0, 50);
  }, [props.all, props.shown, query]);

  const wasOpen = useRef(false);
  useEffect(() => {
    if (open) inputRef.current?.focus();
    else if (wasOpen.current) buttonRef.current?.focus(); // back to "More languages" after Escape or Done
    wasOpen.current = open;
  }, [open]);
  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    if (open && matches[active]) document.getElementById(`lang-${matches[active].code}`)?.scrollIntoView({ block: "nearest" });
  }, [open, active, matches]);

  const close = () => {
    setOpen(false);
    setQuery("");
  };
  const pick = (code: string) => {
    props.onAdd(code);
    setQuery("");
    inputRef.current?.focus();
  };

  if (!open) {
    return (
      <button ref={buttonRef} className="link" onClick={() => setOpen(true)} aria-expanded="false">
        More languages
      </button>
    );
  }
  return (
    <div className="listbox">
      <input
        ref={inputRef}
        className="field"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded="true"
        aria-controls="more-langs"
        aria-activedescendant={matches[active] ? `lang-${matches[active].code}` : undefined}
        aria-label="Search languages"
        placeholder="Search languages"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            close();
          } else if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, matches.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter" && matches[active]) {
            e.preventDefault();
            pick(matches[active].code);
          }
        }}
      />
      <ul id="more-langs" role="listbox" aria-label="Languages">
        {matches.map((l, i) => (
          <li
            key={l.code}
            id={`lang-${l.code}`}
            role="option"
            aria-selected={i === active}
            onMouseDown={(e) => {
              e.preventDefault();
              pick(l.code);
            }}
          >
            <span lang={l.code}>{l.native}</span>
            <small>{l.english}</small>
          </li>
        ))}
        {matches.length === 0 && (
          <li role="option" aria-selected="false" aria-disabled="true">
            <small>No language matches.</small>
          </li>
        )}
      </ul>
      <button className="link" style={{ padding: "6px 12px" }} onClick={close}>
        Done
      </button>
    </div>
  );
}

export function StatusLine(props: { health: Health | null }) {
  const h = props.health;
  if (!h) return null;
  const model = h.model.model.startsWith("gemma4") ? "Gemma 4" : h.model.model;
  return (
    <div className="status" role="status">
      <span>
        <span className="dot" />
        Helper on
      </span>
      {h.model.state === "ready" && (
        <span>
          <span className="dot" />
          {model} ready
        </span>
      )}
      {h.mock && <span>Sample videos</span>}
    </div>
  );
}

/** The only settings: keep words and the signed-in channels. */
export function Settings(props: {
  health: Health;
  onKeepWords: (words: string[]) => Promise<void>;
  onSignOut: (c: Channel) => void;
  onAddChannel: () => void;
}) {
  const { health } = props;
  const [adding, setAdding] = useState("");
  const [error, setError] = useState("");
  const words = health.keepWords;
  const channels = health.channels;
  const summary =
    channels.length > 0
      ? `Keep these words · ${channels.length === 1 ? "1 channel" : `${channels.length} channels`}`
      : "Keep these words";
  const change = (next: string[]) =>
    props.onKeepWords(next).then(
      () => setError(""),
      () => setError("That did not save. Is the helper still running?"),
    );
  const add = () => {
    const w = adding.trim();
    if (!w) return;
    if (words.some((x) => x.toLowerCase() === w.toLowerCase())) {
      setAdding("");
      return;
    }
    change([...words, w]).then(() => setAdding(""));
  };
  return (
    <details className="settings">
      <summary>
        <span>{summary}</span>
        <span aria-hidden="true">▾</span>
      </summary>
      <div className="set">
        <h2>Keep these words as they are</h2>
        <div className="chips">
          {words.map((w) => (
            <button
              key={w}
              className="chip"
              aria-label={`Stop keeping ${w}`}
              onClick={() => change(words.filter((x) => x !== w))}
            >
              {w} <span className="x" aria-hidden="true">×</span>
            </button>
          ))}
        </div>
        <div className="addword">
          <input
            className="field"
            aria-label="Add a word to keep"
            placeholder="Add a word"
            value={adding}
            onChange={(e) => setAdding(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") add();
            }}
          />
          <button className="link" onClick={add} disabled={!adding.trim()}>
            Add
          </button>
        </div>
        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        {channels.length > 0 && (
          <>
            <h2>Channels</h2>
            {channels.map((c) => (
              <div className="line" key={c.id}>
                <span>{c.title}</span>
                <button className="link" onClick={() => props.onSignOut(c)} aria-label={`Sign out ${c.title}`}>
                  Sign out
                </button>
              </div>
            ))}
            <div className="line">
              <button className="link" onClick={props.onAddChannel}>
                Add another channel
              </button>
            </div>
          </>
        )}
      </div>
    </details>
  );
}
