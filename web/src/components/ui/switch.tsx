// Switch is the on/off control used wherever a setting toggles (task enable,
// run settings). A plain button with role="switch", so it needs no extra
// primitive and reads correctly to screen readers.
export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
    className={"relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition disabled:opacity-40 " + (checked ? "bg-accent" : "bg-border")}>
    <span className={"inline-block h-4 w-4 rounded-full bg-white shadow transition " + (checked ? "translate-x-[18px]" : "translate-x-0.5")} />
  </button>;
}
