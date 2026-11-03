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

  const openAdd = (type) => { setDrawerType(type); setForm({}); setDrawerOpen(true) }

  const saveSource = async () => {
    const list = [...sources, form]
    await api('/config/sources', { method: 'PUT', body: JSON.stringify(list) })
    setDrawerOpen(false); load()
  }
  const saveDest = async () => {
    const list = [...dests, form]
    await api('/config/destinations', { method: 'PUT', body: JSON.stringify(list) })
    setDrawerOpen(false); load()
  }

  return (
    <Stack>
      <Title order={3}>Settings</Title>

      <Title order={4}>General</Title>
      <Select label="Default view" data={['day','week','month']} defaultValue="month" />
      <Select label="Start of week" data={['Sunday','Monday']} defaultValue="Sunday" />
      <Select label="Polling interval (min)" data={['1','5','10','15','30','60']} defaultValue="10" />
      <Select label="Theme" data={['light','dark','system']} defaultValue="system" />

      <Divider />
      <Title order={4}>Sources</Title>
      <Text size="sm" c="dimmed">{sources.length} configured</Text>
      <Button size="xs" onClick={() => openAdd('source')}>Add source</Button>
      <ul>{sources.map(s => <li key={s.id}>{s.id} ({s.type})</li>)}</ul>

      <Divider />
      <Title order={4}>Destinations</Title>
      <Text size="sm" c="dimmed">{dests.length} configured</Text>
      <Button size="xs" onClick={() => openAdd('destination')}>Add destination</Button>
      <ul>{dests.map(d => <li key={d.id + d.calendar_id}>{d.id}/{d.calendar_id}</li>)}</ul>

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
              <TextInput label="ID (auto)" placeholder="generated" disabled />
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
