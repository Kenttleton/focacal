import {
  Action,
  ActionPanel,
  Color,
  Icon,
  List,
  Toast,
  showToast,
  open,
} from "@raycast/api";
import { useEffect, useState } from "react";
import { fetchToday, triggerReplan, formatTime, formatUntil, typeLabel, FocalEvent } from "./api";

export default function Today() {
  const [events, setEvents] = useState<FocalEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await fetchToday();
      setEvents(data);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      showToast({ style: Toast.Style.Failure, title: "Could not reach focacal", message: msg });
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  return (
    <List
      isLoading={loading}
      navigationTitle="Today's Agenda"
      searchBarPlaceholder="Filter events..."
    >
      {error && (
        <List.EmptyView
          title="focacal unreachable"
          description={error + "\n\nMake sure 'docker compose up -d' is running."}
          icon={Icon.ExclamationMark}
        />
      )}

      {!error && events.length === 0 && !loading && (
        <List.EmptyView title="Nothing scheduled today" icon={Icon.Calendar} />
      )}

      {events.map((event) => (
        <EventItem key={event.id} event={event} onReplan={load} />
      ))}
    </List>
  );
}

function EventItem({ event, onReplan }: { event: FocalEvent; onReplan: () => void }) {
  const { label, color } = typeLabel(event.type);
  const timeRange = `${formatTime(event.start)} – ${formatTime(event.end)}`;
  const until = formatUntil(event.minutes_until, event.is_now);

  const accessories: List.Item.Accessory[] = [
    { text: until, tooltip: timeRange },
  ];

  if (event.is_now) {
    accessories.unshift({ tag: { value: "Now", color: Color.Green } });
  }

  return (
    <List.Item
      title={event.title}
      subtitle={timeRange}
      icon={eventIcon(event)}
      accessories={accessories}
      actions={
        <ActionPanel>
          <ActionPanel.Section title={event.title}>
            {event.conference_url && (
              <Action
                title="Join Meeting"
                icon={Icon.Video}
                onAction={() => open(event.conference_url!)}
              />
            )}
            <Action
              title="Open in Calendar"
              icon={Icon.Calendar}
              onAction={() => open("calshow://")}
            />
          </ActionPanel.Section>

          <ActionPanel.Section title="focacal">
            <Action
              title="Replan Schedule"
              icon={Icon.ArrowClockwise}
              shortcut={{ modifiers: ["cmd"], key: "r" }}
              onAction={async () => {
                await showToast({ style: Toast.Style.Animated, title: "Replanning…" });
                await triggerReplan();
                await showToast({ style: Toast.Style.Success, title: "Replan triggered" });
                setTimeout(onReplan, 3000);
              }}
            />
            <Action
              title="Refresh"
              icon={Icon.RotateClockwise}
              shortcut={{ modifiers: ["cmd"], key: "." }}
              onAction={onReplan}
            />
          </ActionPanel.Section>
        </ActionPanel>
      }
    />
  );
}

function eventIcon(event: FocalEvent): List.Item.Props["icon"] {
  const { color } = typeLabel(event.type);
  switch (event.type) {
    case "focus":  return { source: Icon.Pencil,   tintColor: color };
    case "lunch":  return { source: Icon.Leaf,     tintColor: color };
    case "break":  return { source: Icon.Pause,    tintColor: color };
    case "real":   return { source: Icon.Calendar, tintColor: color };
    default:       return Icon.Circle;
  }
}
