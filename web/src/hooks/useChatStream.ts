import { useCallback, useRef, useState } from "react";
import type { Message } from "../types";
import { api, auth } from "../api";

export type RunStatus = "idle" | "streaming" | "paused" | "error" | "done";

// id of the single transient bubble that accumulates streaming token deltas.
const STREAM_ID = "__stream__";

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
  const activeConvRef = useRef<string>("");

  const reset = useCallback(() => setMessages([]), []);

  const run = useCallback(async (p: RunParams) => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    activeConvRef.current = p.conversationID;
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

    // terminal carries the run's final status; lastSeq tracks the highest SSE
    // event id seen so a reconnect can replay only what was missed.
    const state = { terminal: "streaming" as RunStatus, lastSeq: 0 };

    const handleFrame = (frame: string) => {
      let data = "";
      for (const ln of frame.split("\n")) {
        if (ln.startsWith("id:")) {
          const n = parseInt(ln.slice(3).trim(), 10);
          if (!isNaN(n)) state.lastSeq = n;
        } else if (ln.startsWith("data:")) {
          data = ln.slice(5).trim();
        }
      }
      if (!data) return;
      try {
        const msg = JSON.parse(data) as Message;
        if (msg.type === "heartbeat") return;
        if (msg.type === "stream") {
          setMessages((m) => {
            const copy = [...m];
            const i = copy.findIndex((x) => x.id === STREAM_ID);
            if (i >= 0) {
              copy[i] = { ...copy[i], content: (copy[i].content || "") + (msg.content || "") };
            } else {
              copy.push({ ...msg, id: STREAM_ID });
            }
            return copy;
          });
          return;
        }
        setMessages((m) => {
          const base =
            msg.type === "chat" && msg.role === "assistant"
              ? m.filter((x) => x.id !== STREAM_ID)
              : m;
          return [...base, msg];
        });
        if (msg.type === "clarify") state.terminal = "paused";
        if (msg.type === "task" && msg.action === "done") state.terminal = "done";
        if (msg.type === "task" && msg.action === "failed") state.terminal = "error";
      } catch {
        /* skip malformed frame */
      }
    };

    const consume = async (res: Response) => {
      if (!res.body) throw new Error("no stream body");
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let idx: number;
        while ((idx = buf.indexOf("\n\n")) >= 0) {
          handleFrame(buf.slice(0, idx));
          buf = buf.slice(idx + 2);
        }
      }
    };

    try {
      const res = await fetch("/api/v1/controller/chat/run", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}),
        },
        body: JSON.stringify({
          message: p.message,
          conversation_id: p.conversationID,
          user_email: p.userEmail,
          enabled_tools: p.enabledTools,
          resume_key: p.resumeKey ?? "",
        }),
        signal: ctrl.signal,
      });
      await consume(res);

      // If the stream ended before a terminal event (network drop while the run
      // is still going), reconnect to /chat/attach and replay missed events.
      let attempts = 0;
      while (state.terminal === "streaming" && !ctrl.signal.aborted && attempts < 5) {
        attempts++;
        await new Promise((r) => setTimeout(r, 500 * attempts));
        try {
          const url =
            `/api/v1/controller/chat/attach?conversation_id=${encodeURIComponent(p.conversationID)}` +
            `&last_event_id=${state.lastSeq}`;
          const ar = await fetch(url, {
            headers: { ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}) },
            signal: ctrl.signal,
          });
          if (ar.status === 404) break; // run already gone (finished + lingered out)
          await consume(ar);
          attempts = 0; // progress made; reset backoff
        } catch {
          /* retry */
        }
      }
      setStatus(state.terminal);
    } catch (e) {
      if ((e as Error).name !== "AbortError") setStatus("error");
    }
  }, []);

  const kill = useCallback(() => {
    // Cancel the BACKEND run (it is detached on context.Background(), so
    // aborting the local SSE fetch alone leaves it running). /chat/kill cancels
    // the run's context, which also closes any GUI WebSocket and frees the
    // shared browser lock.
    if (activeConvRef.current) api.kill(activeConvRef.current).catch(() => {});
    abortRef.current?.abort();
    setStatus("idle");
  }, []);

  return { messages, status, run, kill, reset, setMessages };
}
