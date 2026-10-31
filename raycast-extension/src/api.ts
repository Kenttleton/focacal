import { getPreferenceValues } from "@raycast/api";

interface Preferences {
  apiBaseUrl: string;
  timezone: string;
}

export interface FocalEvent {
  id: string;
  type: "real" | "focus" | "lunch" | "break";
  source: string;
  title: string;
  start: string;
  end: string;
  all_day?: boolean;
  conference_url?: string;
  is_now: boolean;
  minutes_until: number;
}

function prefs(): Preferences {
  return getPreferenceValues<Preferences>();
}

function baseURL(): string {
  return (prefs().apiBaseUrl || "http://localhost:5233").replace(/\/$/, "");
}

async function get<T>(path: string): Promise<T> {
  const tz = encodeURIComponent(prefs().timezone || "UTC");
  const url = `${baseURL()}${path}${path.includes("?") ? "&" : "?"}tz=${tz}`;
  const resp = await fetch(url, { signal: AbortSignal.timeout(5000) });
  if (!resp.ok) throw new Error(`focacal API ${resp.status}: ${resp.statusText}`);
  return resp.json() as Promise<T>;
}

async function post(path: string): Promise<void> {
  const url = `${baseURL()}${path}`;
  await fetch(url, { method: "POST", signal: AbortSignal.timeout(5000) });
}

/** Fetch today's full agenda (events + blocks), sorted by start time. */
export async function fetchToday(): Promise<FocalEvent[]> {
  const events = await get<FocalEvent[]>("/today");
  return events ?? [];
}

/** Fetch the current or next event. Returns null if nothing is on today. */
export async function fetchNow(): Promise<FocalEvent | null> {
  try {
    return await get<FocalEvent | null>("/now");
  } catch {
    return null;
  }
}

/** Trigger an immediate AI replan. */
export async function triggerReplan(): Promise<void> {
  await post("/replan");
}

/** Format a time string for display (e.g. "2:30 PM"). */
export function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

/** Format a duration in minutes as a human string (e.g. "in 25 min", "now", "10 min ago"). */
export function formatUntil(minutesUntil: number, isNow: boolean): string {
  if (isNow) return "Now";
  if (minutesUntil <= 0) return `${Math.abs(minutesUntil)} min ago`;
  if (minutesUntil < 60) return `in ${minutesUntil} min`;
  const h = Math.floor(minutesUntil / 60);
  const m = minutesUntil % 60;
  return m > 0 ? `in ${h}h ${m}m` : `in ${h}h`;
}

/** Return a short type label and color hint for display. */
export function typeLabel(type: FocalEvent["type"]): { label: string; color: string } {
  switch (type) {
    case "focus":  return { label: "Focus",   color: "#4A90D9" };
    case "lunch":  return { label: "Lunch",   color: "#7ED321" };
    case "break":  return { label: "Break",   color: "#F5A623" };
    case "real":   return { label: "Meeting", color: "#9B59B6" };
    default:       return { label: type,      color: "#888888" };
  }
}
