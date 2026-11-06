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

// GET /config/calendars returns {values, order, pending_restart, load_error}.
// values holds accounts, calendars and destinations as objects keyed by id; JSON
// object order is not defined, so each list is rebuilt from order[name].
// `sources` is derived for the sidebar and event labels, which still think in
// sources: one per calendar, with label = the calendar name and type = the
// account type ("ics" for a url calendar). Event.source is the calendar id.
export async function loadCalendars() {
  const view = await api('/config/calendars')
  const list = name => ((view.order || {})[name] || []).map(k => (view.values?.[name] || {})[k]).filter(Boolean)
  const accounts = list('accounts')
  const calendars = list('calendars')
  const type = {}
  for (const a of accounts) type[a.id] = a.type
  const sources = calendars.map(c => ({
    id: c.id,
    label: c.name || c.id,
    type: c.account ? (type[c.account] || '') : 'ics',
    account: c.account || '',
    plan: c.plan !== false,
  }))
  return {
    accounts, calendars, destinations: list('destinations'), sources,
    pruned: view.pruned || [],
    pendingRestart: !!view.pending_restart, loadError: view.load_error || '',
  }
}

// Deleting an account, calendar or destination cascades (an account takes its
// calendars, a calendar takes the destinations that target it). With dryRun the
// server validates and answers with what would be removed, writing nothing.
// Returns the pruned list: [{kind, id}].
export async function deleteConfigItem(collection, id, dryRun) {
  const view = await api('/config/calendars' + (dryRun ? '?dry_run=true' : ''), {
    method: 'PATCH', body: JSON.stringify({ [collection]: { [id]: null } }),
  })
  return view.pruned || []
}
