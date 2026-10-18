import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { ArtifactDrawer } from "../src/components/ArtifactDrawer";
import { Composer } from "../src/components/Composer";
import { Thread } from "../src/components/Thread";
import { FilePreview } from "../src/components/FilePreview";
import App from "../src/App";
import "../src/index.css";

function Fixture() {
  const [state, setState] = useState<any>({ mode: "drawer", conversationID: "", tab: "overview", name: "sample.csv" });
  const [creates, setCreates] = useState(0);
  useEffect(() => { (window as any).fileTest = { set: (next: any) => setState((s: any) => ({ ...s, ...next })) }; }, []);
  const noop = () => {};
  const messages: any[] = state.messages ? [...state.messages] : [{ id: "write", ts: 1, role: "assistant", type: "tool", meta: { conversation_id: state.conversationID }, payload: { tool: "file_write", args: { path: "a/b/sample.csv" }, result: "saved successfully" } }];
  if (state.markdown) messages.push({ id: "answer", ts: 2, role: "assistant", type: "chat", meta: { conversation_id: state.conversationID }, content: state.markdown });
  return <div className="h-screen bg-bg text-ink">
    <output data-testid="creates">{creates}</output>
    {state.mode === "drawer" && <ArtifactDrawer open onClose={noop} tab={state.tab} setTab={tab => setState((s: any) => ({ ...s, tab }))} liveTab={null} email="me@example.com" conversationID={state.conversationID} onJumpToConversation={noop} focusArtifact={null} onClearArtifact={noop} />}
    {state.mode === "composer" && <Composer status="idle" onSend={noop} onKill={noop} enabledTools={new Set()} onSetTools={noop} activeSkill={null} onPickSkill={noop} conversationID={state.conversationID} ensureConversation={async () => {
      setCreates(n => n + 1); await new Promise(r => setTimeout(r, 100));
      setState((s: any) => s.conversationID ? s : ({ ...s, conversationID: "new" })); return "new";
    }} />}
    {state.mode === "thread" && <Thread messages={messages as any} status={state.status || "idle"} conversationID={state.conversationID} ownerEmail="me@example.com" fileConv={state.shared ? state.conversationID : undefined} recovery={{ run: null, busy: false } as any} onContinue={noop} canRetry={false} onResume={noop} onPick={noop} onRetry={noop} onSchedule={noop} />}
    {state.mode === "preview" && <FilePreview conv={state.conversationID} name={state.name} onClose={() => setState((s: any) => ({ ...s, mode: "none" }))} />}
    {state.mode === "app" && <App />}
  </div>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
