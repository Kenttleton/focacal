import { showHUD, showToast, Toast } from "@raycast/api";
import { fetchNow, formatTime, formatUntil } from "./api";

export default async function Now() {
  try {
    const event = await fetchNow();
    if (!event) {
      await showHUD("Nothing on your calendar right now");
      return;
    }
    const until = formatUntil(event.minutes_until, event.is_now);
    const time = `${formatTime(event.start)} – ${formatTime(event.end)}`;
    const status = event.is_now ? `Now: ${event.title}` : `${until} — ${event.title}`;
    await showHUD(`${status} (${time})`);
  } catch (err) {
    await showToast({
      style: Toast.Style.Failure,
      title: "focacal unreachable",
      message: err instanceof Error ? err.message : String(err),
    });
  }
}
