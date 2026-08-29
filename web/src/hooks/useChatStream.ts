import { useCallback, useRef, useState } from "react";
import type { Message } from "../types";
import { api, auth } from "../api";

export type RunStatus = "idle" | "streaming" | "paused" | "error" | "done";

// id of the per-conversation transient bubble that accumulates token deltas.
const STREAM_ID = "__stream__";
// transient bubble accumulating a reasoning model's live "thinking" deltas.
const REASON_ID = "__reasoning__";

export interface RunParams {
  message: string;
  conversationID: string;
  userEmail: string;
  enabledTools: string[];
  resumeKey?: string;
  selectedVersion?: string; // "" = main model, "mini" = the cheaper/faster model
  activeSkill?: string; // user-locked skill mode (researcher / writer / …)
  fileIDs?: string[]; // uploaded attachment paths (text injected; images → VLM)
  confirmRisky?: boolean; // gate side-effecting tools behind user approval
}

interface ConvStream {
  messages: Message[];
  status: RunStatus;
}

const EMPTY: ConvStream = { messages: [], status: "idle" };

/**
 * useChatStreams manages an independent SSE run PER conversation, so several
 * conversations can run concurrently in the background while only the active one
 * is displayed. Each conversation keeps its own messages, status and abort
 * controller; switching conversations never interrupts a running one.
 */
export function useChatStreams() {
  const [streams, setStreams] = useState<Record<string, ConvStream>>({});
  const abortRefs = useRef<Record<string, AbortController>>({});

  // immutably update one conversation's stream slice
  const patch = useCallback((cid: string, fn: (c: ConvStream) => ConvStream) => {
    setStreams((s) => ({ ...s, [cid]: fn(s[cid] || EMPTY) }));
  }, []);

  const setConvMessages = useCallback(
    (cid: string, msgs: Message[] | ((m: Message[]) => Message[])) => {
      patch(cid, (c) => ({ ...c, messages: typeof msgs === "function" ? msgs(c.messages) : msgs }));
    },
    [patch],
  );

  const run = useCallback(
    async (p: RunParams) => {
      const cid = p.conversationID;
      abortRefs.current[cid]?.abort(); // only abort THIS conversation's prior run
      const ctrl = new AbortController();
      abortRefs.current[cid] = ctrl;
      patch(cid, (c) => ({ ...c, status: "streaming" }));

      // optimistic echo of the user's message
      if (p.message && !p.resumeKey) {
        setConvMessages(cid, (m) => [
          ...m,
          {
            id: "local-" + Date.now(),
            type: "chat",
            role: "user",
            content: p.message,
            meta: { conversation_id: cid, task_id: "", trace_id: "" },
            ts: Date.now(),
          },
        ]);
      }

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
          if (msg.type === "stream" && msg.action === "reset") {
            // The model call was retried/failed over mid-stream: drop the partial
            // text from the failed attempt so it isn't concatenated with the new one.
            setConvMessages(cid, (m) => m.filter((x) => x.id !== STREAM_ID && x.id !== REASON_ID));
            return;
          }
          if (msg.type === "stream") {
            // Reasoning ("thinking") deltas accumulate in their own transient
            // bubble so they render as a collapsible indicator, not the answer.
            const bucket = msg.action === "reasoning" ? REASON_ID : STREAM_ID;
            setConvMessages(cid, (m) => {
              // The first answer token ends the current thinking round → drop it.
              let copy = bucket === STREAM_ID ? m.filter((x) => x.id !== REASON_ID) : m;
              copy = [...copy];
              const i = copy.findIndex((x) => x.id === bucket);
              if (i >= 0) {
                copy[i] = { ...copy[i], content: (copy[i].content || "") + (msg.content || "") };
              } else {
                copy.push({ ...msg, id: bucket });
              }
              return copy;
            });
            return;
          }
          setConvMessages(cid, (m) => {
            // A real assistant message or a tool step ends the thinking round.
            const dropReason = msg.type === "tool" || (msg.type === "chat" && msg.role === "assistant");
            let base = dropReason ? m.filter((x) => x.id !== REASON_ID) : m;
            base = msg.type === "chat" && msg.role === "assistant" ? base.filter((x) => x.id !== STREAM_ID) : base;
            return [...base, msg];
          });
          if (msg.type === "clarify") state.terminal = "paused";
          if (msg.type === "task" && (msg.action === "done" || msg.action === "failed")) {
            state.terminal = msg.action === "done" ? "done" : "error";
            setConvMessages(cid, (m) => m.filter((x) => x.id !== REASON_ID));
          }
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
            conversation_id: cid,
            user_email: p.userEmail,
            enabled_tools: p.enabledTools,
            resume_key: p.resumeKey ?? "",
            selected_version: p.selectedVersion ?? "",
            active_skill: p.activeSkill ?? "",
            file_ids: p.fileIDs ?? [],
            confirm_risky: p.confirmRisky ?? false,
          }),
          signal: ctrl.signal,
        });
        await consume(res);

        // reconnect + replay missed events if the stream dropped mid-run
        let attempts = 0;
        while (state.terminal === "streaming" && !ctrl.signal.aborted && attempts < 5) {
          attempts++;
          await new Promise((r) => setTimeout(r, 500 * attempts));
          try {
            const url =
              `/api/v1/controller/chat/attach?conversation_id=${encodeURIComponent(cid)}` +
              `&last_event_id=${state.lastSeq}`;
            const ar = await fetch(url, {
              headers: { ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}) },
              signal: ctrl.signal,
            });
            if (ar.status === 404) break;
            await consume(ar);
            attempts = 0;
          } catch {
            /* retry */
          }
        }
        patch(cid, (c) => ({ ...c, status: state.terminal }));
      } catch (e) {
        if ((e as Error).name !== "AbortError") patch(cid, (c) => ({ ...c, status: "error" }));
      }
    },
    [patch, setConvMessages],
  );

  const kill = useCallback(
    (cid: string) => {
      if (!cid) return;
      // /chat/kill cancels the detached backend run (closing any GUI WS + freeing
      // the browser lock); aborting the local fetch alone wouldn't stop it.
      api.kill(cid).catch(() => {});
      abortRefs.current[cid]?.abort();
      patch(cid, (c) => ({ ...c, status: "idle" }));
    },
    [patch],
  );

  const messagesOf = useCallback((cid: string) => streams[cid]?.messages ?? [], [streams]);
  const statusOf = useCallback((cid: string): RunStatus => streams[cid]?.status ?? "idle", [streams]);
  const runningIds = Object.keys(streams).filter((cid) => streams[cid].status === "streaming");

  return { run, kill, setConvMessages, messagesOf, statusOf, runningIds };
}
