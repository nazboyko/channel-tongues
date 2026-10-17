// Parses a server-sent event stream that arrives in arbitrary chunks.

export interface SseEvent {
  event: string;
  data: string;
}

/** Feeds text chunks and returns complete events; keeps the unfinished tail. */
export class SseParser {
  private buffer = "";

  push(chunk: string): SseEvent[] {
    this.buffer += chunk.replace(/\r\n/g, "\n");
    const out: SseEvent[] = [];
    let end = this.buffer.indexOf("\n\n");
    while (end >= 0) {
      const block = this.buffer.slice(0, end);
      this.buffer = this.buffer.slice(end + 2);
      let event = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith(":")) continue; // comment, a keep-alive
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
      }
      if (data.length > 0) out.push({ event, data: data.join("\n") });
      end = this.buffer.indexOf("\n\n");
    }
    return out;
  }
}
