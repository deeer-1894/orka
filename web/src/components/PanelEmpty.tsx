import { Icon, type IconName } from "./Icon";
export function PanelEmpty({
  icon, title, children, action,
}: {
  icon?: IconName;
  title?: string;
  children: React.ReactNode;
  action?: { label: string; onClick: () => void };
}) {
  // Legacy single-string usage still renders as a plain hint.
  if (!title) return <div className="p-6 text-center text-[13px] text-faint">{children}</div>;
  return (
    <div className="flex flex-col items-center px-6 py-10 text-center">
      {icon && (
        <div className="mb-3 grid h-11 w-11 place-items-center rounded-full bg-surface2 text-muted">
          <Icon name={icon} size={20} />
        </div>
      )}
      <div className="text-[14px] font-medium text-ink">{title}</div>
      <p className="mt-1.5 max-w-[34ch] text-[12.5px] leading-relaxed text-muted">{children}</p>
      {action && (
        <button
          onClick={action.onClick}
          className="mt-4 rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-ink hover:border-accent/40 hover:bg-surface2"
        >
          {action.label}
        </button>
      )}
    </div>
  );
}
