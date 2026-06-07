import { useCallback, useRef, useState } from "react";
import type { Message } from "../types";

export type RunStatus = "idle" | "streaming" | "paused" | "error" | "done";

export interface RunParams {
  message: string;
  conversationID: string;
  userEmail: string;
  enabledTools: string[];
  resumeKey?: string;
}

/** Streams /chat/run Server-Sent Events from a POST body via fetch + ReadableStream. */
export function useChatStream() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [status, setStatus] = useState<RunStatus>("idle");
  const abortRef = useRef<AbortController | null>(null);

  const reset = useCallback(() => setMessages([]), []);

  const run = useCallback(async (p: RunParams) => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setStatus("streaming");

    // optimistic echo of the user's message
    if (p.message && !p.resumeKey) {
      setMessages((m) => [
        ...m,
        {
          id: "local-" + Date.now(),
          type: "chat",
          role: "user",
          content: p.message,
          meta: { conversation_id: p.conversationID, task_id: "", trace_id: "" },
          ts: Date.now(),
        },
      ]);
    }

    try {
      const res = await fetch("/api/v1/controller/chat/run", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-User-Email": p.userEmail },
        body: JSON.stringify({
          message: p.message,
          conversation_id: p.conversationID,
          user_email: p.userEmail,
          enabled_tools: p.enabledTools,
          resume_key: p.resumeKey ?? "",
        }),
        signal: ctrl.signal,
      });
      if (!res.body) throw new Error("no stream body");

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let last: RunStatus = "streaming";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let idx: number;
        while ((idx = buf.indexOf("\n\n")) >= 0) {
          const frame = buf.slice(0, idx).trim();
          buf = buf.slice(idx + 2);
          const line = frame.startsWith("data:") ? frame.slice(5).trim() : frame;
          if (!line) continue;
          try {
            const msg = JSON.parse(line) as Message;
            if (msg.type === "heartbeat") continue; // shown via status, not feed
            setMessages((m) => [...m, msg]);
            if (msg.type === "clarify") last = "paused";
            if (msg.type === "task" && msg.action === "done") last = "done";
            if (msg.type === "task" && msg.action === "failed") last = "error";
          } catch {
            /* skip malformed frame */
          }
        }
      }
      setStatus(last);
    } catch (e) {
      if ((e as Error).name !== "AbortError") setStatus("error");
    }
  }, []);

  const kill = useCallback(() => {
    abortRef.current?.abort();
    setStatus("idle");
  }, []);

  return { messages, status, run, kill, reset, setMessages };
}
