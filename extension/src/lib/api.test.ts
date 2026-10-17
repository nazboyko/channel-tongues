import { describe, expect, it } from "vitest";
import { Api, ApiError, HelperDown } from "./api";
import { SseParser } from "./sse";

function fakeFetch(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    return handler(String(input), init ?? {});
  }) as typeof fetch;
  return { fn, calls };
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("Api", () => {
  it("sends the pairing code and parses JSON", async () => {
    const { fn, calls } = fakeFetch(() => json(200, { ok: true, signedIn: true }));
    const api = new Api({ code: async () => "ABCD-EFGH", fetchFn: fn });
    const h = await api.health();
    expect(h.signedIn).toBe(true);
    expect(calls[0].url).toBe("http://127.0.0.1:7312/health");
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe("Bearer ABCD-EFGH");
  });

  it("turns an error answer into an ApiError with the helper's sentence", async () => {
    const { fn } = fakeFetch(() => json(401, { code: "pair", error: "Enter the code the helper printed." }));
    const api = new Api({ code: async () => "", fetchFn: fn });
    await expect(api.health()).rejects.toMatchObject({ status: 401, code: "pair", message: "Enter the code the helper printed." });
    await expect(api.health()).rejects.toBeInstanceOf(ApiError);
  });

  it("reports a helper that is not running", async () => {
    const fn = (async () => {
      throw new TypeError("Failed to fetch");
    }) as typeof fetch;
    const api = new Api({ code: async () => "x", fetchFn: fn });
    await expect(api.health()).rejects.toBeInstanceOf(HelperDown);
  });

  it("posts JSON bodies", async () => {
    const { fn, calls } = fakeFetch(() => json(202, { jobId: "j1" }));
    const api = new Api({ code: async () => "x", fetchFn: fn, base: "http://127.0.0.1:9" });
    expect(await api.startJob({ videoId: "AbC_12-xyz9", languages: ["es"] })).toBe("j1");
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ videoId: "AbC_12-xyz9", languages: ["es"] });
  });

  it("streams job events until done", async () => {
    const body = [
      'event: snapshot\ndata: {"id":"j1","state":"running","languages":[]}\n\n',
      'event: lang\ndata: {"code":"es","status":"rea',
      'dy"}\n\n: still working\n\nevent: done\ndata: {"id":"j1","state":"done","languages":[]}\n\n',
    ];
    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        for (const b of body) c.enqueue(new TextEncoder().encode(b));
        c.close();
      },
    });
    const { fn } = fakeFetch(() => new Response(stream, { status: 200 }));
    const api = new Api({ code: async () => "x", fetchFn: fn });
    const snaps: string[] = [];
    const langs: string[] = [];
    await api.watch(
      "j1",
      (j) => snaps.push(j.state),
      (l) => langs.push(`${l.code}:${l.status}`),
    );
    expect(snaps).toEqual(["running", "done"]);
    expect(langs).toEqual(["es:ready"]);
  });
});

describe("SseParser", () => {
  it("joins multi-line data and skips comments", () => {
    const p = new SseParser();
    expect(p.push("data: a\r\ndata: b\r\n\r\n: ping\n\n")).toEqual([{ event: "message", data: "a\nb" }]);
  });
});
