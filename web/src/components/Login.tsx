import { useState } from "react";
import { accounts, auth, type Session } from "../api";

export function Login({ onAuthed }: { onAuthed: (s: Session) => void }) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setErr("");
    if (!email || password.length < 6) {
      setErr("请输入邮箱,密码至少 6 位");
      return;
    }
    setBusy(true);
    try {
      const s =
        mode === "login"
          ? await accounts.login(email, password)
          : await accounts.register(email, password, name);
      auth.set(s.token);
      onAuthed(s);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid h-screen place-items-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-2xl bg-accent text-white font-serif text-[22px]">
            O
          </div>
          <h1 className="font-serif text-[26px] text-ink">Welcome to Orka</h1>
          <p className="mt-1 text-[14px] text-muted">
            {mode === "login" ? "登录你的工作区" : "创建一个新账号"}
          </p>
        </div>

        <div className="rounded-2xl border border-border bg-surface p-5 shadow-[0_2px_18px_rgba(40,38,32,0.06)]">
          {mode === "register" && (
            <Field label="名称(可选)" value={name} onChange={setName} placeholder="你的名字" />
          )}
          <Field label="邮箱" value={email} onChange={setEmail} placeholder="you@example.com" type="email" />
          <Field
            label="密码"
            value={password}
            onChange={setPassword}
            placeholder="至少 6 位"
            type="password"
            onEnter={submit}
          />
          {err && <p className="mb-2 text-[13px] text-accent">{err}</p>}
          <button
            onClick={submit}
            disabled={busy}
            className="mt-1 w-full rounded-xl bg-accent py-2.5 font-medium text-white hover:brightness-105 disabled:opacity-50 transition"
          >
            {busy ? "…" : mode === "login" ? "登录" : "注册并登录"}
          </button>
        </div>

        <p className="mt-4 text-center text-[13px] text-muted">
          {mode === "login" ? "还没有账号?" : "已有账号?"}{" "}
          <button
            onClick={() => {
              setMode(mode === "login" ? "register" : "login");
              setErr("");
            }}
            className="text-accent hover:underline"
          >
            {mode === "login" ? "去注册" : "去登录"}
          </button>
        </p>
      </div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  onEnter,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  onEnter?: () => void;
}) {
  return (
    <label className="mb-3 block">
      <span className="mb-1 block text-[12px] text-faint">{label}</span>
      <input
        type={type}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && onEnter?.()}
        className="w-full rounded-lg border border-border bg-bg px-3 py-2 text-[14px] outline-none focus:border-accent/50"
      />
    </label>
  );
}
