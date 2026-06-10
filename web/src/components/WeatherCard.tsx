import type { WeatherCardData } from "../types";

// weatherIcon maps a condition description (EN or ZH from wttr.in) to an emoji.
function weatherIcon(desc: string): string {
  const d = desc.toLowerCase();
  if (/(thunder|雷)/.test(d)) return "⛈️";
  if (/(snow|雪|sleet|冰)/.test(d)) return "❄️";
  if (/(rain|shower|雨|drizzle)/.test(d)) return "🌧️";
  if (/(fog|mist|haze|雾|霾)/.test(d)) return "🌫️";
  if (/(partly|partial|多云间|少云)/.test(d)) return "⛅";
  if (/(cloud|overcast|阴|多云)/.test(d)) return "☁️";
  if (/(clear|sunny|晴)/.test(d)) return "☀️";
  return "🌤️";
}

// weekday turns an ISO date (YYYY-MM-DD) into a short localized weekday.
function weekday(date: string, idx: number): string {
  if (idx === 0) return "今天";
  const d = new Date(date + "T00:00:00");
  if (isNaN(d.getTime())) return date.slice(5);
  return ["周日", "周一", "周二", "周三", "周四", "周五", "周六"][d.getDay()];
}

export function WeatherCard({ data }: { data: WeatherCardData }) {
  const { location, current, forecast } = data;
  return (
    <div className="mb-7 ml-[42px] max-w-md overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-[#5b8def] to-[#7aa7f0] text-white shadow-sm">
      <div className="flex items-center justify-between px-5 pt-4">
        <div>
          <div className="text-[13px] opacity-90">{location}</div>
          <div className="mt-1 flex items-end gap-2">
            <span className="text-[40px] font-light leading-none">{current.temp_c}°</span>
            <span className="pb-1 text-[14px] opacity-90">{current.desc}</span>
          </div>
          <div className="mt-1 text-[12px] opacity-85">
            体感 {current.feels_c}° · 湿度 {current.humidity}% · 风 {current.wind_kmph} km/h
          </div>
        </div>
        <div className="text-[52px] leading-none">{weatherIcon(current.desc)}</div>
      </div>

      {forecast && forecast.length > 0 && (
        <div className="mt-3 flex gap-1 bg-black/10 px-3 py-3">
          {forecast.map((f, i) => (
            <div key={f.date} className="flex flex-1 flex-col items-center gap-1">
              <span className="text-[12px] opacity-90">{weekday(f.date, i)}</span>
              <span className="text-[22px] leading-none">{weatherIcon(f.desc)}</span>
              <span className="text-[12px]">
                <span className="font-medium">{f.max_c}°</span>
                <span className="opacity-70"> / {f.min_c}°</span>
              </span>
              {f.rain && f.rain !== "0" && (
                <span className="text-[10px] opacity-80">💧{f.rain}%</span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// parseWeatherCard extracts the <weather-card>{json}</weather-card> block that
// the weather tool embeds in its result. Returns null if absent/invalid.
export function parseWeatherCard(result?: string): WeatherCardData | null {
  if (!result) return null;
  const m = result.match(/<weather-card>([\s\S]*?)<\/weather-card>/);
  if (!m) return null;
  try {
    return JSON.parse(m[1]) as WeatherCardData;
  } catch {
    return null;
  }
}
