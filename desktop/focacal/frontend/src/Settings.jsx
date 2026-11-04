import { useState, useEffect } from 'react'
import { Stack, TextInput, Select, Switch, Button, Text, Group, Title, Divider, Drawer, useMantineColorScheme } from '@mantine/core'
import { api, loadCalendars } from './api'

export default function Settings({ prefs, onPrefs }) {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const [sources, setSources] = useState([])
  const [dests, setDests] = useState([])
  const [pendingRestart, setPendingRestart] = useState(false)
  const [error, setError] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerType, setDrawerType] = useState('source')
  const [editing, setEditing] = useState(null) // the saved entry being edited, or null when adding
  const [form, setForm] = useState({})

  const load = async () => {
    try {
      const c = await loadCalendars()
      setSources(c.sources); setDests(c.destinations); setPendingRestart(c.pendingRestart)
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  const genId = () => (typeof crypto !== 'undefined' && crypto.randomUUID) ? crypto.randomUUID() : Math.random().toString(36).slice(2)
  const openAdd = (type) => { setDrawerType(type); setEditing(null); setError(''); setForm({ id: genId() }); setDrawerOpen(true) }
  const openEdit = (type, item) => { setDrawerType(type); setEditing(item); setError(''); setForm({ ...item }); setDrawerOpen(true) }

  // Sends one merge patch to /config/calendars. The server validates and answers
  // 400/422 with {error}; secrets are never part of a patch.
  const patchCalendars = async (patch) => {
    try {
      await api('/config/calendars', { method: 'PATCH', body: JSON.stringify(patch) })
      setError(''); setDrawerOpen(false); await load()
    } catch (e) { setError(e.message) }
  }

  // Only the fields the form edits, and for an existing entry only those that changed.
  const changed = (fields) => {
    const out = {}
    for (const k of fields) if (!editing || form[k] !== editing[k]) out[k] = form[k] ?? ''
    return out
  }
  const saveSource = async () => {
    const id = form.id || genId()
    const fields = changed(['label', 'type'])
    if (editing && !Object.keys(fields).length) { setDrawerOpen(false); return }
    await patchCalendars({ sources: { [id]: editing ? fields : { id, ...fields } } })
  }
  const saveDest = async () => {
    const id = form.id || genId()
    const calendar_id = form.calendar_id || 'primary'
    await patchCalendars({ destinations: { [id + '|' + calendar_id]: { id, calendar_id } } })
  }
  const remove = async () => {
    const key = drawerType === 'source' ? editing.id : editing.id + '|' + editing.calendar_id
    await patchCalendars({ [drawerType === 'source' ? 'sources' : 'destinations']: { [key]: null } })
  }

  return (
    <Stack style={{ maxWidth: 640, margin: '0 auto' }}>
      <Title order={3}>Settings</Title>

      <Title order={4}>General</Title>
      <Select label="Default view" data={[{ value: 'day', label: 'Day' }, { value: 'week', label: 'Week' }, { value: 'month', label: 'Month' }]}
        value={prefs.view} onChange={v => v && onPrefs({ ...prefs, view: v })} allowDeselect={false} />
      <Select label="Start of week" data={[{ value: '0', label: 'Sunday' }, { value: '1', label: 'Monday' }]}
        value={String(prefs.firstDay)} onChange={v => v && onPrefs({ ...prefs, firstDay: Number(v) })} allowDeselect={false} />
      <Select label="Polling interval (min)" data={['1','5','10','15','30','60']} defaultValue="10" />
      <Select label="Theme" data={[{ value: 'auto', label: 'System' }, { value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }]}
        value={colorScheme} onChange={v => v && setColorScheme(v)} allowDeselect={false} />

      <Divider />
      {pendingRestart && <Text size="sm" c="orange">Changes apply after restart.</Text>}
      {error && !drawerOpen && <Text size="sm" c="red">{error}</Text>}
      <Title order={4}>Sources</Title>
      <Text size="sm" c="dimmed">{(sources || []).length} configured</Text>
      <Button size="xs" onClick={() => openAdd('source')}>Add source</Button>
      <ul>{(sources || []).map(s => <li key={s.id}>{s.label || s.id} ({s.type}, {s.has_secret ? 'credential set' : 'no credential'}) <Button size="xs" variant="subtle" onClick={() => openEdit('source', s)}>Edit</Button></li>)}</ul>
      <Button size="xs" variant="light" onClick={() => window.open('/calendar.ics', '_blank')}>Export ICS</Button>

      <Divider />
      <Title order={4}>Destinations</Title>
      <Text size="sm" c="dimmed">{(dests || []).length} configured</Text>
      <Button size="xs" onClick={() => openAdd('destination')}>Add destination</Button>
      <ul>{(dests || []).map(d => <li key={d.id + '|' + d.calendar_id}>{d.id}/{d.calendar_id} <Button size="xs" variant="subtle" onClick={() => openEdit('destination', d)}>Edit</Button></li>)}</ul>

      <Divider />
      <Title order={4}>Notifications</Title>
      <Switch label="Conflicts" defaultChecked />
      <Switch label="New meetings" defaultChecked />
      <Switch label="Generated events" defaultChecked />
      <Switch label="Calendar shifts" defaultChecked />

      {/* Right drawer for complex add flows */}
      <Drawer opened={drawerOpen} onClose={() => setDrawerOpen(false)} title={`${editing ? 'Edit' : 'Add'} ${drawerType}`} position="right" size="md">
        <Stack>
          {error && <Text size="sm" c="red">{error}</Text>}
          {drawerType === 'source' && (
            <>
              <TextInput label="ID (auto)" value={form.id || ''} disabled />
              <TextInput label="Label" value={form.label || ''} onChange={e => setForm({ ...form, label: e.target.value })} />
              <Select label="Type" data={['google','outlook','caldav','ics']} value={form.type} onChange={v => setForm({ ...form, type: v })} />
              {editing && <Text size="sm" c="dimmed">Credential: {editing.has_secret ? 'set (not shown)' : 'not set'}. Credentials are configured outside the UI.</Text>}
              <Button onClick={saveSource}>Save</Button>
              {editing && <Button color="red" variant="light" onClick={remove}>Delete</Button>}
            </>
          )}
          {drawerType === 'destination' && (
            <>
              <TextInput label="ID" value={form.id || ''} disabled={!!editing} onChange={e => setForm({ ...form, id: e.target.value })} />
              <TextInput label="Calendar ID" value={form.calendar_id || ''} disabled={!!editing} onChange={e => setForm({ ...form, calendar_id: e.target.value })} />
              {!editing && <Button onClick={saveDest}>Save</Button>}
              {editing && <Button color="red" variant="light" onClick={remove}>Delete</Button>}
            </>
          )}
        </Stack>
      </Drawer>
    </Stack>
  )
}
