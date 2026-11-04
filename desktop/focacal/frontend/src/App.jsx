import { useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Button, Group, Select, Text, Tooltip } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconChevronLeft, IconChevronRight, IconLayoutSidebar } from '@tabler/icons-react'
import { api, clearToken, getToken, loadCalendars, ymd } from './api'
import { colorFor, FOCACAL_SOURCE, sourceColors } from './colors'
import { calloutParts, useDisplayTimezone } from './tz'
import CalendarView from './CalendarView'
import EventPopover from './EventPopover'
import Login from './Login'
import Settings from './Settings'
import Sidebar from './Sidebar'
import TimezoneField from './TimezoneField'

const SIDEBAR_W = 264
const read = (k, d) => { try { return localStorage.getItem(k) ?? d } catch { return d } }
const write = (k, v) => { try { localStorage.setItem(k, v) } catch {} }

function useNow(ms = 15000) {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => { const id = setInterval(() => setNow(new Date()), ms); return () => clearInterval(id) }, [ms])
  return now
}

export default function App() {
  const [authed, setAuthed] = useState(!!getToken())
  const narrow = useMediaQuery('(max-width: 800px)')
  const [navOpen, setNavOpen] = useState(() => read('fc_nav', narrow ? '0' : '1') === '1')
  const [page, setPage] = useState('calendar')
  const [prefs, setPrefs] = useState(() => ({ view: read('fc_view', 'month'), firstDay: Number(read('fc_firstday', '0')) }))
  const [view, setView] = useState(prefs.view)
  const [range, setRange] = useState(null)
  const [events, setEvents] = useState([])
  const [blocks, setBlocks] = useState([])
  const [conflicts, setConflicts] = useState([])
  const [sources, setSources] = useState([])
  const [cfg, setCfg] = useState(null)
  const [hidden, setHidden] = useState(() => new Set(JSON.parse(read('fc_hidden', '[]'))))
  const [sel, setSel] = useState(null)
  const calRef = useRef(null)
  const now = useNow()
  const zone = useDisplayTimezone()

  const refreshSources = () => loadCalendars().then(c => setSources(c.sources)).catch(e => { if (e.message === 'unauthorized') setAuthed(false) })

  useEffect(() => {
    if (!authed) return
    api('/health').catch(() => { clearToken(); setAuthed(false) })
    // 401 clears the token in api(); 503 (config service unavailable) and other
    // failures leave the defaults: no sources listed, 09:00 working-hours start.
    refreshSources()
    api('/config/planner').then(v => setCfg(v?.values || null)).catch(e => { if (e.message === 'unauthorized') setAuthed(false) })
  }, [authed])

  // /events and /blocks take YYYY-MM-DD; /conflicts takes RFC 3339.
  useEffect(() => {
    if (!authed || !range) return
    const pad = d => new Date(d.getTime() + 86400000)
    const q = `from=${ymd(new Date(range.start.getTime() - 86400000))}&to=${ymd(pad(range.end))}`
    api(`/events?${q}`).then(e => setEvents(e || [])).catch(() => {})
    api(`/blocks?${q}`).then(b => setBlocks(b || [])).catch(() => {})
    api(`/conflicts?from=${range.start.toISOString()}&to=${range.end.toISOString()}`).then(c => setConflicts(c || [])).catch(() => {})
  }, [authed, range])

  const colors = useMemo(() => sourceColors(sources), [sources])
  const openConflicts = useMemo(() => conflicts.filter(c => !c.dismissed), [conflicts])
  const conflictIds = useMemo(() => new Set(openConflicts.flatMap(c => [c.a.id, c.b.id])), [openConflicts])

  const items = useMemo(() => {
    const seen = new Set()
    return [...events, ...blocks].filter(it => {
      if (seen.has(it.id)) return false
      seen.add(it.id)
      return !hidden.has(it.type === 'real' ? it.source : FOCACAL_SOURCE)
    }).map(it => ({ ...it, color: colorFor(it, colors), conflict: conflictIds.has(it.id) }))
  }, [events, blocks, hidden, colors, conflictIds])

  if (!authed) return <Login onAuthed={() => setAuthed(true)} />

  const toggleNav = () => { setNavOpen(o => { write('fc_nav', o ? '0' : '1'); return !o }) }
  const go = (p) => { setPage(p); if (narrow) setNavOpen(false) }
  const changeView = (v) => { if (!v) return; setView(v); write('fc_view', v) }
  const toggleCal = (id) => setHidden(h => {
    const n = new Set(h); n.has(id) ? n.delete(id) : n.add(id)
    write('fc_hidden', JSON.stringify([...n]))
    return n
  })
  const dismiss = async (key) => {
    await api(`/conflicts/${key}/dismiss`, { method: 'POST' }).catch(() => {})
    setConflicts(prev => prev.map(c => c.key === key ? { ...c, dismissed: true } : c))
  }
  const openConflict = (c) => { setPage('calendar'); setTimeout(() => calRef.current?.goto(c.a.start), 0) }
  const savePrefs = (p) => {
    setPrefs(p); write('fc_firstday', String(p.firstDay))
    if (p.view !== prefs.view) { write('fc_view', p.view); setView(p.view) }
  }

  const callout = calloutParts(now, zone.tz)
  // Open one hour before working hours begin so the first label isn't clipped.
  const startHour = parseInt((cfg?.working_hours?.start || '09:00').split(':')[0], 10) || 9
  const scrollTime = `${String(Math.max(0, startHour - 1)).padStart(2, '0')}:00:00`
  const sourceLabel = (it) => it.type !== 'real' ? 'Focacal planned time' : (sources.find(s => s.id === it.source)?.label || it.source)

  const sidebarToggle = (
    <Tooltip label={navOpen ? 'Hide sidebar' : 'Show sidebar'} withArrow>
      <ActionIcon variant="subtle" color="gray" onClick={toggleNav} aria-label="Toggle sidebar" aria-expanded={navOpen}>
        <IconLayoutSidebar size={20} stroke={1.75} />
      </ActionIcon>
    </Tooltip>
  )

  const sidebar = (
    <aside style={narrow
      ? { position: 'fixed', inset: '0 auto 0 0', width: SIDEBAR_W, zIndex: 300, boxShadow: '4px 0 24px rgba(0,0,0,.25)' }
      : { width: SIDEBAR_W, flex: 'none' }}>
      <Sidebar page={page} onNav={go} sources={sources} colors={colors} hidden={hidden} onToggle={toggleCal}
        conflicts={openConflicts} tz={zone.tz} onOpenConflict={openConflict} onDismiss={dismiss} />
    </aside>
  )

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {navOpen && sidebar}
      {navOpen && narrow && <div onClick={toggleNav} style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.3)', zIndex: 250 }} />}

      <main style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: 'var(--cal-bg)' }}>
        {page === 'calendar' ? (
          <>
            <div className="tb">
              {sidebarToggle}
              <Group gap={4}>
                <ActionIcon variant="default" onClick={() => calRef.current?.prev()} aria-label="Previous"><IconChevronLeft size={16} /></ActionIcon>
                <Button variant="default" size="compact-md" onClick={() => calRef.current?.today()}>Today</Button>
                <ActionIcon variant="default" onClick={() => calRef.current?.next()} aria-label="Next"><IconChevronRight size={16} /></ActionIcon>
              </Group>
              <div className="tb-callout">
                <span>{callout.text}</span>
                <TimezoneField zone={callout.zone} tz={zone.tz} detected={zone.detected} overridden={zone.overridden}
                  onChange={zone.setOverride} onReset={zone.reset} />
              </div>
              <Select value={view} onChange={changeView} allowDeselect={false} w={110} aria-label="Calendar view"
                data={[{ value: 'day', label: 'Day' }, { value: 'week', label: 'Week' }, { value: 'month', label: 'Month' }]} />
            </div>
            <CalendarView ref={calRef} items={items} view={view} tz={zone.tz} firstDay={prefs.firstDay}
              scrollTime={scrollTime} onRange={setRange}
              onSelect={(item, rect) => setSel({ item, rect })} />
            <EventPopover sel={sel} tz={zone.tz} sourceLabel={sel ? sourceLabel(sel.item) : ''} onClose={() => setSel(null)} />
          </>
        ) : (
          <>
            <div className="tb">{sidebarToggle}<Text fw={600}>{page === 'planner' ? 'Planner' : 'Settings'}</Text></div>
            <div style={{ flex: 1, overflow: 'auto', padding: 24 }}>
              {page === 'planner' && <Text c="dimmed">Planner is coming soon.</Text>}
              {page === 'settings' && <Settings prefs={prefs} onPrefs={savePrefs} onSaved={refreshSources} />}
            </div>
          </>
        )}
      </main>
    </div>
  )
}
