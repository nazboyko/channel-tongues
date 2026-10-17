// Talks to the helper's native messaging host, which Chrome starts on demand.
// It can start the helper and hand over the pairing code, so the person
// never has to open a terminal or type the code. Absent host: null.

const HOST = "com.channeltongues.helper";

export interface NativeReply {
  ok: boolean;
  running: boolean;
  code?: string;
  error?: string;
}

export function askNative(type: "ping" | "start" | "code"): Promise<NativeReply | null> {
  return new Promise((resolve) => {
    try {
      chrome.runtime.sendNativeMessage(HOST, { type }, (reply?: NativeReply) => {
        if (chrome.runtime.lastError || !reply) resolve(null);
        else resolve(reply);
      });
    } catch {
      resolve(null);
    }
  });
}
