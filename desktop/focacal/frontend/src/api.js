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
