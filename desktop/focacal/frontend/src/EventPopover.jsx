import { Anchor, Group, Popover, Stack, Text } from '@mantine/core'
import { IconAlertTriangleFilled, IconLock } from '@tabler/icons-react'

function range(item, tz) {
  const s = new Date(item.start), e = new Date(item.end)
  const day = new Intl.DateTimeFormat([], { weekday: 'long', month: 'long', day: 'numeric', timeZone: tz })
  if (item.all_day) return day.format(s) + ' · all day'
  const t = new Intl.DateTimeFormat([], { hour: 'numeric', minute: '2-digit', timeZone: tz })
  const z = new Intl.DateTimeFormat([], { timeZoneName: 'short', timeZone: tz }).formatToParts(s).find(p => p.type === 'timeZoneName')?.value
  return `${day.format(s)} · ${t.format(s)} – ${t.format(e)} ${z ?? ''}`.trim()
}

// Anchored to the clicked event's rectangle, so it works for any FullCalendar view.
export default function EventPopover({ sel, tz, sourceLabel, onClose }) {
  if (!sel) return null
  const { item, rect } = sel
  return (
    <Popover opened onClose={onClose} position="right-start" withArrow shadow="md" width={300} offset={8}>
      <Popover.Target>
        <div style={{ position: 'fixed', left: rect.left, top: rect.top, width: rect.width, height: rect.height, pointerEvents: 'none' }} />
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap={6}>
          <Group gap={8} wrap="nowrap" align="flex-start">
            <span style={{ width: 4, alignSelf: 'stretch', borderRadius: 2, background: item.color, flex: 'none' }} />
            <div style={{ minWidth: 0 }}>
              <Text fw={600} lh={1.3}>{item.title}</Text>
              <Text size="sm" c="dimmed">{range(item, tz)}</Text>
            </div>
          </Group>
          <Text size="xs" c="dimmed">{sourceLabel}</Text>
          {item.locked && <Group gap={4}><IconLock size={14} /><Text size="xs">Locked: replanning won't move it</Text></Group>}
          {item.conflict && <Group gap={4}><IconAlertTriangleFilled size={14} color="#ff9f0a" /><Text size="xs">Overlaps another event</Text></Group>}
          {item.conference_url && <Anchor href={item.conference_url} target="_blank" rel="noreferrer" size="sm">Join meeting</Anchor>}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}
