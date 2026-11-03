import { useState, useEffect, useRef } from 'react'
import { MantineProvider, Button, Select, Drawer, Text, Group, Stack } from '@mantine/core'
import { api, getToken, setToken, clearToken } from './api'
import CalendarView from './CalendarView'
import './App.css'

export default function App() {
  const [token, setTok] = useState(getToken())
  const [authed, setAuthed] = useState(!!getToken())
  const [view, setView] = useState(localStorage.getItem('fc_view') || 'month')
  const [leftOpen, setLeftOpen] = useState(true)
  const [rightOpen, setRightOpen] = useState(false)
  const [conflicts, setConflicts] = useState([])
  const [events, setEvents] = useState([])
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [mode, setMode] = useState('login')
  const [msg, setMsg] = useState('')
  const [page, setPage] = useState('calendar')
  const calRef = useRef(null)

  useEffect(() => {
    if (!token) { setAuthed(false); return }
    api('/health').then(() => setAuthed(true)).catch(() => { clearToken(); setTok(''); setAuthed(false) })
  }, [token])

  useEffect(() => {
    if (!authed) return
    const now = new Date()
    const from = new Date(now.getTime() - 7*86400000).toISOString()
    const to = new Date(now.getTime() + 14*86400000).toISOString()
    api(`/events?from=${from}&to=${to}`).then(setEvents).catch(() => {})
    api(`/conflicts?from=${from}&to=${to}`).then(setConflicts).catch(() => {})
  }, [authed])

  const submit = async (e) => {
    e.preventDefault()
    const path = mode === 'setup' ? '/auth/setup' : '/auth/login'
    const r = await api(path, { method: 'POST', body: JSON.stringify({ username: user, password: pass }) }).catch(err => ({ error: err.message }))
    if (r && r.error) { setMsg(r.error); return }
    if (r && r.token) { setToken(r.token); setTok(r.token); setAuthed(true) }
  }

  const changeView = (v) => { setView(v); localStorage.setItem('fc_view', v) }

  const dismiss = async (key) => {
    await api(`/conflicts/${key}/dismiss`, { method: 'POST' })
    setConflicts(prev => prev.map(c => c.key === key ? { ...c, dismissed: true } : c))
  }

  if (!authed) {
    return (
      <MantineProvider defaultColorScheme="dark">
        <div style={{ padding: 24, maxWidth: 360 }}>
          <Text size="xl" fw={700} mb="md">{mode === 'setup' ? 'First-run setup' : 'Sign in'}</Text>
          <form onSubmit={submit}>
            <Stack>
              <input placeholder="username" value={user} onChange={e => setUser(e.target.value)} />
              <input type="password" placeholder="password" value={pass} onChange={e => setPass(e.target.value)} />
              <Button type="submit">{mode === 'setup' ? 'Create' : 'Login'}</Button>
            </Stack>
          </form>
          <Button variant="subtle" onClick={() => setMode(mode === 'setup' ? 'login' : 'setup')} mt="sm">
            {mode === 'setup' ? 'Have an account? Login' : 'First run? Setup'}
          </Button>
          {msg && <Text c="red" mt="sm">{msg}</Text>}
        </div>
      </MantineProvider>
    )
  }

  const openConflicts = conflicts.filter(c => !c.dismissed)

  return (
    <MantineProvider defaultColorScheme="dark">
      <div style={{ display: 'flex', height: '100vh' }}>
        {leftOpen ? (
          <aside style={{ width: 200, borderRight: '1px solid #eee', padding: 12 }}>
            <Button variant="subtle" size="xs" onClick={() => setLeftOpen(false)}>‹</Button>
            <Stack mt="sm">
              <Button variant="subtle" onClick={() => setPage('calendar')}>Calendar</Button>
              <Button variant="subtle" onClick={() => setPage('planner')}>Planner</Button>
              <Button variant="subtle" onClick={() => setPage('settings')}>Settings</Button>
            </Stack>
          </aside>
        ) : (
          <Button variant="subtle" size="xs" onClick={() => setLeftOpen(true)} style={{ width: 24 }}>›</Button>
        )}

        <main style={{ flex: 1, padding: 12, overflow: 'auto' }}>
          {page === 'calendar' && (
            <>
              <Group mb="sm" justify="space-between">
                <Group>
                  <Button size="xs" onClick={() => calRef.current?.prev()}>‹</Button>
                  <Button size="xs" onClick={() => calRef.current?.today()}>Today</Button>
                  <Button size="xs" onClick={() => calRef.current?.next()}>›</Button>
                </Group>
                <Text fw={700} ta="center">
                  {new Date().toLocaleString([], { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' })}
                </Text>
                <Select
                  value={view}
                  onChange={changeView}
                  data={[{ value: 'day', label: 'Day' }, { value: 'week', label: 'Week' }, { value: 'month', label: 'Month' }]}
                  w={120}
                />
              </Group>
              <CalendarView ref={calRef} events={events} view={view} />
            </>
          )}
          {page === 'planner' && <Text>Planner (coming soon)</Text>}
          {page === 'settings' && <Text>Settings (forms TBD)</Text>}
        </main>

        {!rightOpen && (
          <Button variant="subtle" size="xs" onClick={() => setRightOpen(true)} style={{ width: 24, position: 'relative' }}>
            ‹{openConflicts.length > 0 && <span style={{ color: 'red', marginLeft: 4 }}>●{openConflicts.length}</span>}
          </Button>
        )}
        <Drawer opened={rightOpen} onClose={() => setRightOpen(false)} title="Conflicts" position="right" size="sm">
          {openConflicts.length === 0 && <Text c="dimmed">No conflicts</Text>}
          {openConflicts.map(c => (
            <div key={c.key} style={{ marginBottom: 8, borderBottom: '1px solid #eee', paddingBottom: 8 }}>
              <Text size="sm">{new Date(c.a.start).toLocaleString()} – {new Date(c.b.start).toLocaleString()}</Text>
              <Text size="xs" c="dimmed">{c.a.title} vs {c.b.title}</Text>
              <Button size="xs" mt={4} onClick={() => dismiss(c.key)}>Dismiss</Button>
            </div>
          ))}
        </Drawer>
      </div>
    </MantineProvider>
  )
}
