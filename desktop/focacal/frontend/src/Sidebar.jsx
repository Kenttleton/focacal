import { Badge, Button, Checkbox, Group, ScrollArea, Text } from '@mantine/core'
import { IconCalendar, IconSettings, IconSparkles } from '@tabler/icons-react'
import { FOCACAL_SOURCE, KIND_COLORS } from './colors'

const NAV = [
  { id: 'calendar', label: 'Calendar', icon: IconCalendar },
  { id: 'planner', label: 'Planner', icon: IconSparkles },
  { id: 'settings', label: 'Settings', icon: IconSettings },
]

function fmt(iso, tz) {
  return new Intl.DateTimeFormat([], { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZone: tz }).format(new Date(iso))
}

export default function Sidebar({ page, onNav, sources, colors, hidden, onToggle, conflicts, tz, onOpenConflict, onDismiss }) {
  const cals = [...sources.map(s => ({ id: s.id, label: s.label || s.id, color: colors[s.id] })),
    { id: FOCACAL_SOURCE, label: 'Focacal', color: KIND_COLORS.focus }]

  return (
    <nav className="sb" aria-label="Main" style={{ width: '100%', height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="sb-section" style={{ paddingTop: 14 }}>
        <Text fw={700} size="lg" px={8} pb={8}>focacal</Text>
        {NAV.map(({ id, label, icon: Icon }) => (
          <button key={id} type="button" className="sb-item" aria-current={page === id ? 'page' : undefined} onClick={() => onNav(id)}>
            <Icon size={18} stroke={1.75} />
            {label}
          </button>
        ))}
      </div>

      <ScrollArea style={{ flex: 1 }} type="auto">
        <div className="sb-section">
          <div className="sb-label">Calendars</div>
          {cals.map(c => (
            <div key={c.id} className="sb-item" style={{ cursor: 'default' }}>
              <Checkbox
                size="xs"
                color={c.color}
                checked={!hidden.has(c.id)}
                onChange={() => onToggle(c.id)}
                label={c.label}
                styles={{ root: { width: '100%' }, label: { cursor: 'pointer', paddingInlineStart: 8, fontSize: 13 } }}
              />
            </div>
          ))}
        </div>

        <div className="sb-section">
          <Group justify="space-between" className="sb-label" pr={8}>
            <span>Conflicts</span>
            {conflicts.length > 0 && <Badge size="xs" color="orange" variant="filled" circle>{conflicts.length}</Badge>}
          </Group>
          {conflicts.length === 0 && <Text size="xs" c="dimmed" px={8}>Nothing overlaps in this range.</Text>}
          {conflicts.map(c => (
            <div key={c.key} className={`cf-card ${c.tentative ? 'cf-tentative' : ''}`}>
              <button type="button" onClick={() => onOpenConflict(c)} className="sb-item" style={{ padding: 0, display: 'block' }}>
                <Text size="sm" fw={600} lineClamp={1}>{c.a.title}</Text>
                <Text size="sm" lineClamp={1}>overlaps {c.b.title}</Text>
                <Text size="xs" c="dimmed">{fmt(c.a.start, tz)}{c.tentative ? ' · tentative' : ''}</Text>
              </button>
              <Button size="compact-xs" variant="subtle" color="gray" mt={4} onClick={() => onDismiss(c.key)}>Dismiss</Button>
            </div>
          ))}
        </div>
      </ScrollArea>
    </nav>
  )
}
