import { useCallback, useState } from 'react'

const LS_KEY = 'fc_display_tz'

export const detectTimezone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'

export const listTimezones = () =>
  typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [detectTimezone()]

export function isValidTimezone(tz) {
  try { new Intl.DateTimeFormat(undefined, { timeZone: tz }); return true } catch { return false }
}

// Splits "Tuesday, November 3, 2026 at 12:05 PM EST" into the text and the zone
// abbreviation so the zone can be rendered as an editable control.
export function calloutParts(date, tz) {
  const parts = new Intl.DateTimeFormat([], {
    weekday: 'long', year: 'numeric', month: 'long', day: 'numeric',
    hour: 'numeric', minute: '2-digit', timeZone: tz, timeZoneName: 'short',
  }).formatToParts(date)
  const zone = parts.find(p => p.type === 'timeZoneName')?.value ?? tz
  const text = parts.filter(p => p.type !== 'timeZoneName').map(p => p.value).join('').trim()
  return { text, zone }
}

// The server owns event times; this only chooses which zone the UI renders them
// in. An empty override means "follow the detected zone". The override is a
// per-browser preference kept in localStorage only; the server never sees it.
export function useDisplayTimezone() {
  const [override, setOverrideState] = useState(() => {
    try { return localStorage.getItem(LS_KEY) || '' } catch { return '' }
  })
  const detected = detectTimezone()

  const setOverride = useCallback((tz) => {
    setOverrideState(tz)
    try { tz ? localStorage.setItem(LS_KEY, tz) : localStorage.removeItem(LS_KEY) } catch {}
  }, [])

  const effective = override && isValidTimezone(override) ? override : detected
  return { tz: effective, detected, overridden: !!override, setOverride, reset: () => setOverride('') }
}
