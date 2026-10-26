import { useState } from "react";
import { api, auth } from "../../api";
import { toast } from "../../lib/toast";

const field = "w-full rounded-lg border border-border bg-bg px-3 py-2 text-base text-ink outline-none transition focus:border-accent/60";

// AccountSettings: change the password. Doing so signs out every other device;
// this one continues with the fresh session the server returns.
export function AccountSettings() {
  const [old, setOld] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const mismatch = again !== "" && again !== next;
  const submit = async () => {
    setBusy(true);
    try {
      const r = await api.changePassword(old, next);
      auth.set(r.token);
      setOld(""); setNext(""); setAgain("");
      toast("密码已修改，其他设备需要重新登录", "success");
    } catch (e) { toast((e as Error).message || "修改失败", "error"); } finally { setBusy(false); }
  };
  return <section aria-label="账号" className="space-y-4">
    <div>
      <h2 className="text-lg font-medium text-ink">账号</h2>
      <p className="mt-1 text-sm text-muted">修改密码后，所有其他设备上的登录都会失效。</p>
    </div>
    <form className="max-w-sm space-y-3" onSubmit={e => { e.preventDefault(); void submit(); }}>
      <label className="block space-y-1"><span className="text-sm font-medium text-ink">当前密码</span>
        <input type="password" autoComplete="current-password" value={old} onChange={e => setOld(e.target.value)} className={field} /></label>
      <label className="block space-y-1"><span className="text-sm font-medium text-ink">新密码</span>
        <input type="password" autoComplete="new-password" value={next} onChange={e => setNext(e.target.value)} className={field} />
        <span className="block text-xs text-faint">至少 8 位，同时包含字母和数字</span></label>
      <label className="block space-y-1"><span className="text-sm font-medium text-ink">再次输入新密码</span>
        <input type="password" autoComplete="new-password" value={again} onChange={e => setAgain(e.target.value)} className={field} />
        {mismatch && <span role="alert" className="block text-xs text-accent">两次输入不一致</span>}</label>
      <button type="submit" disabled={busy || !old || !next || mismatch || again === ""} className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-40">{busy ? "修改中…" : "修改密码"}</button>
    </form>
  </section>;
}
