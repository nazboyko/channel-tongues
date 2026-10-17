// Work a button starts runs once at a time. The flag is a plain field, not
// React state, so two clicks in the same frame still see the first one running.

export class Once {
  private running = false;

  get busy(): boolean {
    return this.running;
  }

  /** Runs fn unless a run is in progress. Resolves to false when it was skipped; an error from fn is passed on. */
  async run(fn: () => unknown): Promise<boolean> {
    if (this.running) return false;
    this.running = true;
    try {
      await fn();
      return true;
    } finally {
      this.running = false;
    }
  }
}
