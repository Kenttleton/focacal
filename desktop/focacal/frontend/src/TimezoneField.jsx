import { useMemo, useState } from 'react'
import { ActionIcon, Autocomplete, Tooltip } from '@mantine/core'
import { IconCurrentLocation } from '@tabler/icons-react'
import { isValidTimezone, listTimezones } from './tz'

// Renders the zone abbreviation as text. Click it to edit in place as a
// searchable IANA zone input; Enter or picking an option saves, Escape cancels.
// While overridden, a button resets to the detected zone.
export default function TimezoneField({ zone, tz, detected, overridden, onChange, onReset }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(tz)
  const zones = useMemo(listTimezones, [])

  const start = () => { setDraft(tz); setEditing(true) }
  const commit = (value) => {
    setEditing(false)
    if (value && value !== tz && isValidTimezone(value)) onChange(value)
  }

  if (editing) {
    return (
      <Autocomplete
        size="xs"
        w={210}
        data={zones}
        value={draft}
        limit={8}
        autoFocus
        aria-label="Timezone"
        placeholder="e.g. America/Chicago"
        error={draft !== '' && !isValidTimezone(draft)}
        onChange={setDraft}
        onOptionSubmit={commit}
        onBlur={() => setEditing(false)}
        onKeyDown={e => {
          if (e.key === 'Enter') commit(draft)
          if (e.key === 'Escape') setEditing(false)
        }}
      />
    )
  }

  return (
    <>
      <Tooltip label={`${tz}${overridden ? ' (custom)' : ' (detected)'} — click to change`} withArrow openDelay={300}>
        <button type="button" className="tz-link" onClick={start} aria-label={`Timezone ${tz}, change`}>
          {zone}
        </button>
      </Tooltip>
      {overridden && (
        <Tooltip label={`Use detected timezone (${detected})`} withArrow>
          <ActionIcon variant="subtle" color="gray" size="sm" onClick={onReset} aria-label="Reset to detected timezone">
            <IconCurrentLocation size={15} />
          </ActionIcon>
        </Tooltip>
      )}
    </>
  )
}
