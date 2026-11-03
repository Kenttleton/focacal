import { useState, useEffect, useRef } from 'react'
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

  if (!authed) {
    return (
      <div style={{ padding: 24, maxWidth: 360 }}>
        <h2>{mode === 'setup' ? 'First-run setup' : 'Sign in'}</h2>
        <form onSubmit={submit}>
          <input placeholder="username" value={user} onChange={e => setUser(e.target.value)} /><br />
          <input type="password" placeholder="password" value={pass} onChange={e => setPass(e.target.value)} /><br />
          <button type="submit">{mode === 'setup' ? 'Create' : 'Login'}</button>
        </form>
        <button onClick={() => setMode(mode === 'setup' ? 'login' : 'setup')}>
          {mode === 'setup' ? 'Have an account? Login' : 'First run? Setup'}
        </button>
        {msg && <p style={{ color: 'red' }}>{msg}</p>}
      </div>
    )
  }

  const dismiss = async (key) => {
    await api(`/conflicts/${key}/dismiss`, { method: 'POST' })
    setConflicts(prev => prev.map(c => c.key === key ? { ...c, dismissed: true } : c))
  }

  const changeView = (v) => { setView(v); localStorage.setItem('fc_view', v) }

  return (
    <div style={{ display: 'flex', height: '100vh' }}>
      {leftOpen && (
        <aside style={{ width: 200, borderRight: '1px solid #ccc', padding: 12 }}>
          <button onClick={() => setLeftOpen(false)}>‹</button>
          <nav>
            <div onClick={() => setPage('calendar')} style={{ cursor: 'pointer', padding: 4 }}>Calendar</div>
            <div onClick={() => setPage('planner')} style={{ cursor: 'pointer', padding: 4 }}>Planner</div>
            <div onClick={() => setPage('settings')} style={{ cursor: 'pointer', padding: 4 }}>Settings</div>
          </nav>
        </aside>
      )}
      {!leftOpen && <button onClick={() => setLeftOpen(true)} style={{ width: 24 }}>›</button>}

      <main style={{ flex: 1, padding: 12, overflow: 'auto' }}>
        {!leftOpen && null}
        {page === 'calendar' && (
          <div style={{ marginBottom: 8, display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'space-between' }}>
            <div style={{ display: 'flex', gap: 8 }}>
              <button onClick={() => calRef.current?.prev()}>‹</button>
              <button onClick={() => calRef.current?.today()}>Today</button>
              <button onClick={() => calRef.current?.next()}>›</button>
            </div>
            <div style={{ fontWeight: 'bold', textAlign: 'center' }}>
              {new Date().toLocaleString([], { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' })}
            </div>
            <select value={view} onChange={e => changeView(e.target.value)}>
              <option value="day">Day</option>
              <option value="week">Week</option>
              <option value="month">Month</option>
            </select>
          </div>
        )}
        {page === 'calendar' && (
          <div>
            <CalendarView ref={calRef} events={events} view={view} />
          </div>
        )}
        {page === 'planner' && <div><h3>Planner</h3><p>Coming soon.</p></div>}
        {page === 'settings' && <div><h3>Settings</h3><p>Forms TBD.</p></div>}
      </main>

      {!rightOpen && (
        <button onClick={() => setRightOpen(true)} style={{ width: 24, position: 'relative' }}>
          ‹{conflicts.filter(c => !c.dismissed).length > 0 && <span style={{ color: 'red' }}>●{conflicts.filter(c => !c.dismissed).length}</span>}
        </button>
      )}
      {rightOpen && (
        <aside style={{ width: 280, borderLeft: '1px solid #ccc', padding: 12 }}>
          <button onClick={() => setRightOpen(false)}>›</button>
          <h4>Conflicts</h4>
          {conflicts.filter(c => !c.dismissed).length === 0 && <p>None</p>}
          {conflicts.filter(c => !c.dismissed).map(c => (
            <div key={c.key} style={{ marginBottom: 8, borderBottom: '1px solid #eee', paddingBottom: 8 }}>
              <div>{new Date(c.a.start).toLocaleString()} – {new Date(c.b.start).toLocaleString()}</div>
              <div style={{ fontSize: 12 }}>{c.a.title} vs {c.b.title}</div>
              <button onClick={() => dismiss(c.key)}>Dismiss</button>
            </div>
          ))}
        </aside>
      )}
    </div>
  )
}
