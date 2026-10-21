// The main area shows a conversation, the tasks page or one project's page.
// The latter two are addressable (#tasks, #project/<id>) so they survive a
// reload and can be linked.
export type View = { kind: "chat" } | { kind: "tasks" } | { kind: "project"; id: string };

export const CHAT: View = { kind: "chat" };
export const TASKS: View = { kind: "tasks" };

export function viewFromHash(hash: string): View {
  if (hash === "#tasks") return TASKS;
  const m = /^#project\/([A-Za-z0-9_-]{1,128})$/.exec(hash);
  return m ? { kind: "project", id: m[1] } : CHAT;
}

export function hashOf(view: View): string {
  return view.kind === "tasks" ? "#tasks" : view.kind === "project" ? "#project/" + view.id : "";
}
