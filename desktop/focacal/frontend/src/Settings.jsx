import { useState, useEffect } from 'react'
import { Stack, TextInput, Select, Switch, Button, Text, Group, Title, Divider, Drawer } from '@mantine/core'
import { api } from './api'

export default function Settings({ onSaved }) {
  const [sources, setSources] = useState([])
  const [dests, setDests] = useState([])
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerType, setDrawerType] = useState('source')
  const [form, setForm] = useState({})

  const load = async () => {
    try { setSources(await api('/sources')) } catch {}
    try { setDests(await api('/destinations')) } catch {}
  }
  useEffect(() => { load() }, [])

  const genId = () => (typeof crypto !== 'undefined' && crypto.randomUUID) ? crypto.randomUUID() : Math.random().toString(36).slice(2)
  const openAdd = (type) => { setDrawerType(type); setForm({ id: genId() }); setDrawerOpen(true) }
  const openEdit = (type, item) => { setDrawerType(type); setForm({ ...item }); setDrawerOpen(true) }

  const saveSource = async () => {
    const f = { ...form }
    if (!f.id) f.id = genId()
    const exists = sources.find(s => s.id === f.id)
    const list = exists ? sources.map(s => s.id === f.id ? f : s) : [...sources, f]
    await api('/config/sources', { method: 'PUT', body: JSON.stringify(list) })
    setDrawerOpen(false); load()
  }
  const saveDest = async () => {
    const f = { ...form }
    if (!f.id) f.id = genId()
    if (!f.calendar_id) f.calendar_id = 'primary'
    const key = d => d.id + '|' + d.calendar_id
    const exists = dests.find(d => key(d) === key(f))
    const list = exists ? dests.map(d => key(d) === key(f) ? f : d) : [...dests, f]
    await api('/config/destinations', { method: 'PUT', body: JSON.stringify(list) })
    setDrawerOpen(false); load()
  }

  return (
    <Stack style={{ maxWidth: 640, margin: '0 auto' }}>
      <Title order={3}>Settings</Title>

      <Title order={4}>General</Title>
      <Select label="Default view" data={['day','week','month']} defaultValue="month" />
      <Select label="Start of week" data={['Sunday','Monday']} defaultValue="Sunday" />
      <Select label="Polling interval (min)" data={['1','5','10','15','30','60']} defaultValue="10" />
      <Select label="Theme" data={['light','dark','system']} defaultValue="system" />

      <Divider />
      <Title order={4}>Sources</Title>
      <Text size="sm" c="dimmed">{(sources || []).length} configured</Text>
      <Button size="xs" onClick={() => openAdd('source')}>Add source</Button>
      <ul>{(sources || []).map(s => <li key={s.id}>{s.label || s.id} ({s.type}) <Button size="xs" variant="subtle" onClick={() => openEdit('source', s)}>Edit</Button></li>)}</ul>
      <Button size="xs" variant="light" onClick={() => window.open('/calendar.ics', '_blank')}>Export ICS</Button>

      <Divider />
      <Title order={4}>Destinations</Title>
      <Text size="sm" c="dimmed">{(dests || []).length} configured</Text>
      <Button size="xs" onClick={() => openAdd('destination')}>Add destination</Button>
      <ul>{(dests || []).map(d => <li key={d.id + d.calendar_id}>{d.id}/{d.calendar_id} <Button size="xs" variant="subtle" onClick={() => openEdit('destination', d)}>Edit</Button></li>)}</ul>

      <Divider />
      <Title order={4}>Notifications</Title>
      <Switch label="Conflicts" defaultChecked />
      <Switch label="New meetings" defaultChecked />
      <Switch label="Generated events" defaultChecked />
      <Switch label="Calendar shifts" defaultChecked />

      {/* Right drawer for complex add flows */}
      <Drawer opened={drawerOpen} onClose={() => setDrawerOpen(false)} title={`Add ${drawerType}`} position="right" size="md">
        <Stack>
          {drawerType === 'source' && (
            <>
              <TextInput label="ID (auto)" value={form.id || ''} disabled />
              <TextInput label="Label" value={form.label || ''} onChange={e => setForm({ ...form, label: e.target.value })} />
              <Select label="Type" data={['google','outlook','caldav','ics']} value={form.type} onChange={v => setForm({ ...form, type: v })} />
              <Button onClick={saveSource}>Save</Button>
            </>
          )}
          {drawerType === 'destination' && (
            <>
              <TextInput label="ID" value={form.id || ''} onChange={e => setForm({ ...form, id: e.target.value })} />
              <TextInput label="Calendar ID" value={form.calendar_id || ''} onChange={e => setForm({ ...form, calendar_id: e.target.value })} />
              <Button onClick={saveDest}>Save</Button>
            </>
          )}
        </Stack>
      </Drawer>
    </Stack>
  )
}
