import { Icon, type IconName } from "./Icon";

// The first-run guide: three steps that take a new user from an empty account
// to a finished task and show where recurring work lives. Model and task steps
// are read from the account itself; the last step and skipping are recorded
// server-side, so the guide does not come back on another device.
export interface OnboardingState {
  hasModel: boolean;
  hasConversation: boolean;
  toured: boolean;
}

export const TRY_PROMPT = "搜索今天 AI 领域的重要新闻，挑出最值得关注的 3 条，每条用一句话说明要点并附上原文链接。";

interface Step { key: string; icon: IconName; title: string; detail: string; done: boolean; action: string; run: () => void }

export function onboardingVisible(s: OnboardingState, dismissed: boolean): boolean {
  return !dismissed && !(s.hasModel && s.hasConversation && s.toured);
}

export function Onboarding({ state, onConfigureModel, onTry, onTour, onDismiss }: {
  state: OnboardingState;
  onConfigureModel: () => void;
  onTry: () => void;
  onTour: () => void;
  onDismiss: () => void;
}) {
  const steps: Step[] = [
    { key: "model", icon: "gear", title: "配置模型", detail: "支持 OpenAI 兼容接口，例如火山方舟、DeepSeek、通义千问。", done: state.hasModel, action: "去配置", run: onConfigureModel },
    { key: "try", icon: "play", title: "试一个任务", detail: "让 Orka 联网搜索并整理一份简报，看它怎样拆步骤、交成果。", done: state.hasConversation, action: "填入示例", run: onTry },
    { key: "tour", icon: "clock", title: "认识定时任务", detail: "按时间、网页变化或外部事件自动运行；成果在右上角的工作台。", done: state.toured, action: "去看看", run: onTour },
  ];
  const finished = steps.filter(s => s.done).length;
  const next = steps.find(s => !s.done);
  return <section aria-label="开始使用" className="mx-auto mb-3 w-full max-w-3xl px-4 sm:px-5">
    <div className="rounded-2xl border border-border bg-surface px-4 py-3.5 shadow-[0_2px_18px_rgba(40,38,32,0.05)]">
      <div className="flex items-center gap-2">
        <h2 className="text-[14px] font-medium text-ink">开始使用 Orka</h2>
        <span className="text-[12px] text-faint">{finished}/{steps.length}</span>
        <span className="flex-1" />
        <button onClick={onDismiss} className="text-[12px] text-faint hover:text-ink">跳过引导</button>
      </div>
      <ol className="mt-2.5 grid gap-2 sm:grid-cols-3">
        {steps.map((s, i) => {
          const current = s === next;
          return <li key={s.key} className={"flex flex-col gap-1 rounded-xl border px-3 py-2.5 " + (current ? "border-accent/40 bg-accentsoft/40" : "border-border")}>
            <div className="flex items-center gap-1.5 text-[13px] text-ink">
              <span className={"grid h-5 w-5 shrink-0 place-items-center rounded-full text-[11px] " + (s.done ? "bg-ok/15 text-ok" : "bg-surface2 text-muted")}>
                {s.done ? <Icon name="check" size={12} /> : i + 1}
              </span>
              <span className={s.done ? "text-muted line-through decoration-faint" : ""}>{s.title}</span>
            </div>
            {!s.done && <p className="text-[12px] leading-snug text-faint">{s.detail}</p>}
            {!s.done && <button onClick={s.run} className={"mt-0.5 inline-flex w-fit items-center gap-1 rounded-lg px-2 py-1 text-[12px] " + (current ? "bg-accent text-white hover:opacity-90" : "border border-border text-muted hover:text-ink")}>
              <Icon name={s.icon} size={12} />{s.action}
            </button>}
          </li>;
        })}
      </ol>
    </div>
  </section>;
}
