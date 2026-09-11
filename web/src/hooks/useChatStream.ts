import { useCallback, useRef, useState } from "react";
import type { Message } from "../types";
import { hydrateConversation, terminalStatus, type ChatStatus } from "../lib/runRecovery";
import { api, auth } from "../api";

export type RunStatus = ChatStatus;

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
  // attachOnly listens to an ALREADY-running conversation instead of starting a
  // new turn. Used after approving a paused danger tool: the backend resumes the
  // checkpointed run, so there is a live stream to join but no message to send.
  attachOnly?: boolean;
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
  const runVersions = useRef<Record<string, number>>({});

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

  // Late history fills missing turns even after a newer stream has finished.
  const hydrateMessages = useCallback((cid: string, messages: Message[]) => {
    patch(cid, c => hydrateConversation(c, messages, cid, !!runVersions.current[cid]));
  }, [patch]);

  const run = useCallback(
    async (p: RunParams) => {
      const cid = p.conversationID;
      abortRefs.current[cid]?.abort(); // only abort THIS conversation's prior run
      const ctrl = new AbortController();
      abortRefs.current[cid] = ctrl;
      const version = (runVersions.current[cid] || 0) + 1;
      runVersions.current[cid] = version;
      const current = () => runVersions.current[cid] === version && !ctrl.signal.aborted;
      const updateMessages = (_cid: string, fn: (messages: Message[]) => Message[]) => {
        patch(cid, c => current() ? { ...c, messages: fn(c.messages) } : c);
      };
      const updateStatus = (status: RunStatus) => patch(cid, c => current() ? { ...c, status } : c);
      updateStatus("streaming");

      // optimistic echo of the user's message
      if (p.message && !p.resumeKey) {
        updateMessages(cid, (m) => [
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
      // Per-run token buffer, flushed on the next animation frame (see below).
      const pending: { buf: Record<string, string>; proto: Record<string, Message>; raf: number } = { buf: {}, proto: {}, raf: 0 };

      // flushDeltas applies the buffered tokens in one state update.
      const flushDeltas = () => {
        if (pending.raf) { cancelAnimationFrame(pending.raf); pending.raf = 0; }
        const buf = pending.buf, proto = pending.proto;
        if (!Object.keys(buf).length) return;
        pending.buf = {}; pending.proto = {};
        updateMessages(cid, (m) => {
          let copy = buf[STREAM_ID] ? m.filter((x) => x.id !== REASON_ID) : m;
          copy = [...copy];
          for (const id of Object.keys(buf)) {
            const i = copy.findIndex((x) => x.id === id);
            if (i >= 0) copy[i] = { ...copy[i], content: (copy[i].content || "") + buf[id] };
            else copy.push({ ...proto[id], id, content: buf[id] });
          }
          return copy;
        });
      };

      const handleFrame = (frame: string) => {
        if (!current()) return;
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
          if (msg.meta?.conversation_id && msg.meta.conversation_id !== cid) return;
          if (msg.type === "heartbeat") return;
          if (msg.type === "stream" && msg.action === "reset") {
            // The model call was retried/failed over mid-stream: drop the partial
            // text from the failed attempt so it isn't concatenated with the new one.
            if (pending.raf) { cancelAnimationFrame(pending.raf); pending.raf = 0; }
            pending.buf = {}; pending.proto = {}; // drop the failed attempt's tokens
            updateMessages(cid, (m) => m.filter((x) => x.id !== STREAM_ID && x.id !== REASON_ID));
            return;
          }
          if (msg.type === "stream") {
            // Reasoning ("thinking") deltas accumulate in their own transient
            // bubble so they render as a collapsible indicator, not the answer.
            const bucket = msg.action === "reasoning" ? REASON_ID : STREAM_ID;
            // Deltas arrive far faster than the screen refreshes. Rendering each
            // one separately costs a full React pass per token and makes the text
            // twitch; buffer them and flush once per animation frame instead, so
            // the answer streams at the display's own rhythm.
            pending.buf[bucket] = (pending.buf[bucket] || "") + (msg.content || "");
            pending.proto[bucket] = pending.proto[bucket] || msg;
            if (pending.raf === 0) pending.raf = requestAnimationFrame(flushDeltas);
            return;
          }
          // A buffered flush must never land AFTER the authoritative message, or
          // it would resurrect the transient bubble the final chat just removed.
          flushDeltas();
          updateMessages(cid, (m) => {
            // A real assistant message or a tool step ends the thinking round.
            const dropReason = msg.type === "tool" || (msg.type === "chat" && msg.role === "assistant");
            let base = dropReason ? m.filter((x) => x.id !== REASON_ID) : m;
            base = msg.type === "chat" && msg.role === "assistant" ? base.filter((x) => x.id !== STREAM_ID) : base;
            return [...base, msg];
          });
          const terminal = terminalStatus(msg);
          if (terminal) {
            state.terminal = terminal;
            updateStatus(terminal);
            updateMessages(cid, (m) => m.filter((x) => x.id !== REASON_ID));
          }
        } catch {
          /* skip malformed frame */
        }
      };

      const consume = async (res: Response) => {
        if (!res.ok || !res.body) throw new Error("stream unavailable: " + res.status);
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        let buf = "";
        for (;;) {
          const { done, value } = await reader.read();
          if (done || !current()) break;
          buf += dec.decode(value, { stream: true });
          let idx: number;
          while ((idx = buf.indexOf("\n\n")) >= 0) {
            handleFrame(buf.slice(0, idx));
            buf = buf.slice(idx + 2);
          }
        }
      };

      // attachLoop (re)joins the conversation's SSE, replaying anything missed.
      // maxAttempts is higher when attaching to a resuming run, because the
      // backend needs a moment to rebuild the agent from its checkpoint.
      const attachLoop = async (maxAttempts: number) => {
        let attempts = 0;
        while (state.terminal === "streaming" && !ctrl.signal.aborted && attempts < maxAttempts) {
          attempts++;
          await new Promise((r) => setTimeout(r, 500 * attempts));
          if (!current()) break;
          try {
            const url =
              `/api/v1/controller/chat/attach?conversation_id=${encodeURIComponent(cid)}` +
              `&last_event_id=${state.lastSeq}`;
            const ar = await fetch(url, {
              headers: { ...(auth.token() ? { Authorization: "Bearer " + auth.token() } : {}) },
              signal: ctrl.signal,
            });
            if (ar.status === 404) {
              if (p.attachOnly) continue; // the resumed run may not be registered yet
              break;
            }
            const previousSeq = state.lastSeq;
            await consume(ar);
            if (state.lastSeq > previousSeq) attempts = 0;
          } catch {
            /* retry */
          }
        }
      };

      try {
        if (p.attachOnly) {
          // Join the resumed run's stream from the beginning; the reconnect loop
          // below keeps retrying while the backend spins the run back up.
          await attachLoop(8);
          const finalStatus = state.terminal === "streaming" ? "error" : state.terminal;
          updateStatus(finalStatus);
          return finalStatus;
        }
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
        await attachLoop(5);
        const finalStatus = state.terminal === "streaming" ? "error" : state.terminal;
        updateStatus(finalStatus);
        return finalStatus;
      } catch (e) {
        if ((e as Error).name !== "AbortError") { updateStatus("error"); return "error" as const; }
      } finally {
        if (current()) flushDeltas();
        if (pending.raf) cancelAnimationFrame(pending.raf);
        if (abortRefs.current[cid] === ctrl) delete abortRefs.current[cid];
      }
    },
    [patch],
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

  return { run, kill, setConvMessages, hydrateMessages, messagesOf, statusOf, runningIds };
}
