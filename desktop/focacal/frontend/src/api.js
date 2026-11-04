const API = ''

export function getToken() { return localStorage.getItem('fc_token') || '' }
export function setToken(t) { localStorage.setItem('fc_token', t) }
export function clearToken() { localStorage.removeItem('fc_token') }

export async function api(path, opts = {}) {
  const token = getToken()
  const res = await fetch(API + path, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: 'Bearer ' + token } : {}),
      ...(opts.headers || {}),
    },
  })
  if (res.status === 401) { clearToken(); throw new Error('unauthorized') }
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || res.statusText)
  }
  if (res.status === 204) return null
  return res.json()
}

// /events and /blocks only accept YYYY-MM-DD; anything else silently falls
// back to a -1/+8 day window on the server.
const pad = n => String(n).padStart(2, '0')
export const ymd = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`

// GET /config/calendars returns {values, order, pending_restart}; values are
// keyed by id (destinations by "id|calendar_id") and JSON object order is not
// defined, so lists are rebuilt from order.
export async function loadCalendars() {
  const view = await api('/config/calendars')
  const list = name => ((view.order || {})[name] || []).map(k => (view.values?.[name] || {})[k]).filter(Boolean)
  return { sources: list('sources'), destinations: list('destinations'), pendingRestart: !!view.pending_restart }
}
