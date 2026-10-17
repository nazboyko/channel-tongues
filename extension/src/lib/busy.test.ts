import { describe, expect, it } from "vitest";
import { Once } from "./busy";

const later = () => {
  let done!: () => void;
  let fail!: (e: Error) => void;
  const promise = new Promise<void>((res, rej) => {
    done = res;
    fail = rej;
  });
  return { promise, done, fail };
};

describe("Once", () => {
  it("runs a double click once", async () => {
    const once = new Once();
    const work = later();
    let calls = 0;
    const fn = () => {
      calls++;
      return work.promise;
    };
    // Two clicks in the same frame: no await between them.
    const first = once.run(fn);
    const second = once.run(fn);
    expect(once.busy).toBe(true);
    expect(await second).toBe(false);
    work.done();
    expect(await first).toBe(true);
    expect(calls).toBe(1);
    expect(once.busy).toBe(false);
  });

  it("comes back after an error, so the person can try again", async () => {
    const once = new Once();
    const work = later();
    const first = once.run(() => work.promise);
    work.fail(new Error("helper stopped"));
    await expect(first).rejects.toThrow("helper stopped");
    expect(once.busy).toBe(false);
    let ran = false;
    expect(await once.run(() => (ran = true))).toBe(true);
    expect(ran).toBe(true);
  });

  it("guards a button that does its work without waiting", async () => {
    const once = new Once();
    let calls = 0;
    await Promise.all([once.run(() => calls++), once.run(() => calls++)]);
    expect(calls).toBe(1);
  });
});
