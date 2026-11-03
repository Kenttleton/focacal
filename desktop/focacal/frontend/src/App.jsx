import { useState, useEffect } from 'react'
import './App.css'

const API = '' // same origin in web; desktop can override via config

export default function App() {
  const [token, setToken] = useState(localStorage.getItem('fc_token') || '')
  const [status, setStatus] = useState('checking')
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [mode, setMode] = useState('login') // login | setup
  const [msg, setMsg] = useState('')

  useEffect(() => {
    if (!token) { setStatus('auth'); return }
    fetch(API + '/health', { headers: { Authorization: 'Bearer ' + token } })
      .then(r => r.ok ? setStatus('ok') : setStatus('auth'))
      .catch(() => setStatus('auth'))
  }, [token])

  const submit = async (e) => {
    e.preventDefault()
    const path = mode === 'setup' ? '/auth/setup' : '/auth/login'
    const body = mode === 'setup'
      ? { username: user, password: pass }
      : { username: user, password: pass }
    const r = await fetch(API + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    const data = await r.json()
    if (!r.ok) { setMsg(data.error || 'failed'); return }
    if (data.token) {
      localStorage.setItem('fc_token', data.token)
      setToken(data.token)
      setStatus('ok')
    }
  }

  if (status === 'checking') return <div>Loading…</div>
  if (status === 'ok') return <div>Authenticated. Token stored.</div>

  return (
    <div style={{ padding: 24, maxWidth: 360 }}>
      <h2>{mode === 'setup' ? 'First-run setup' : 'Sign in'}</h2>
      <form onSubmit={submit}>
        <input placeholder="username" value={user} onChange={e => setUser(e.target.value)} /><br/>
        <input type="password" placeholder="password" value={pass} onChange={e => setPass(e.target.value)} /><br/>
        <button type="submit">{mode === 'setup' ? 'Create' : 'Login'}</button>
      </form>
      <button onClick={() => setMode(mode === 'setup' ? 'login' : 'setup')}>
        {mode === 'setup' ? 'Have an account? Login' : 'First run? Setup'}
      </button>
      {msg && <p style={{ color: 'red' }}>{msg}</p>}
    </div>
  )
}
EOF