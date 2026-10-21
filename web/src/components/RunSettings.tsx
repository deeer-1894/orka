import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Icon, type IconName } from "./Icon";

// How the next run behaves, set beside the input it applies to: the header only
// says what you are looking at and where to go. Values that differ from the
// defaults also show as chips above the input, so a non-default run is never
// a surprise.
export interface RunSettingsValue {
  deepThinking: boolean;
  confirmRisky: boolean;
  toggleThinking: () => void;
  toggleConfirm: () => void;
}

interface Setting { key: string; icon: IconName; label: string; detail: string; on: boolean; toggle: () => void; defaultOn: boolean; chip: string }

function settingsOf(v: RunSettingsValue): Setting[] {
  return [
    { key: "thinking", icon: "sparkle", label: "深度思考", detail: "先充分推理再动手，适合难题，但更慢、更费用量", on: v.deepThinking, toggle: v.toggleThinking, defaultOn: false, chip: "深度思考" },
    { key: "confirm", icon: "shield", label: "高危操作需确认", detail: "执行终端命令、浏览器操作、网络请求、运行代码前先征求你确认", on: v.confirmRisky, toggle: v.toggleConfirm, defaultOn: true, chip: "高危操作不确认" },
  ];
}

export function RunSettingsMenu({ value }: { value: RunSettingsValue }) {
  const settings = settingsOf(value);
  const changed = settings.some(s => s.on !== s.defaultOn);
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button className="composer-icon relative" title="运行设置" aria-label="运行设置">
        <Icon name="sliders" size={17} />
        {changed && <span aria-hidden className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-accent" />}
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent side="top" align="start" className="w-[300px] p-1.5">
      <DropdownMenuLabel>运行设置</DropdownMenuLabel>
      {settings.map(s => (
        <DropdownMenuPrimitive.CheckboxItem key={s.key} checked={s.on} onSelect={e => { e.preventDefault(); s.toggle(); }}
          className="flex cursor-pointer select-none items-start gap-2.5 rounded-lg px-2.5 py-2 outline-none data-[highlighted]:bg-surface2">
          <Icon name={s.icon} size={15} className="mt-0.5 shrink-0 text-muted" />
          <span className="min-w-0 flex-1">
            <span className="block text-[13px] text-ink">{s.label}</span>
            <span className="block text-[11.5px] leading-snug text-faint">{s.detail}</span>
          </span>
          <span aria-hidden className={"relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition " + (s.on ? "bg-accent" : "bg-border")}>
            <span className={"inline-block h-4 w-4 rounded-full bg-white shadow transition " + (s.on ? "translate-x-[18px]" : "translate-x-0.5")} />
          </span>
        </DropdownMenuPrimitive.CheckboxItem>
      ))}
      <p className="px-2.5 pb-1 pt-1.5 text-[11px] text-faint">对之后发送的消息生效</p>
    </DropdownMenuContent>
  </DropdownMenu>;
}

// RunSettingsChips lists the settings that differ from their defaults; each
// chip puts its setting back.
export function RunSettingsChips({ value }: { value: RunSettingsValue }) {
  const changed = settingsOf(value).filter(s => s.on !== s.defaultOn);
  return <>{changed.map(s => (
    <span key={s.key} className="inline-flex items-center gap-1 rounded-full border border-accent/30 bg-accentsoft px-2 py-1 text-[12px] text-accent">
      <Icon name={s.icon} size={13} />{s.chip}
      <button onClick={s.toggle} aria-label={"恢复默认：" + s.label} title="恢复默认" className="hover:text-ink"><Icon name="close" size={12} /></button>
    </span>
  ))}</>;
}
