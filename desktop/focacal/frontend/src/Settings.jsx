import { useState, useEffect } from 'react'
import { Stack, Select, Switch, Button, Text, Group, Title, Divider, Modal, Alert, useMantineColorScheme } from '@mantine/core'
import { loadCalendars, deleteConfigItem } from './api'

const KIND_NAMES = { calendar: 'Calendar', destination: 'Destination', destination_input: 'Destination input' }
const COLLECTION_NAMES = { accounts: 'account', calendars: 'calendar', destinations: 'destination' }

// Read-only list of one collection: the name, with the id shown read-only and a
// delete action. Adding and editing are intentionally not offered here.
function ItemList({ collection, items, onDelete }) {
  if (!items.length) return <Text size="sm" c="dimmed">None configured</Text>
  return (
    <Stack gap={4}>
      {items.map(it => (
        <Group key={it.id} justify="space-between" wrap="nowrap">
          <div>
            <Text size="sm">{it.name || it.id}</Text>
            <Text size="xs" c="dimmed">id: {it.id}</Text>
          </div>
          <Button size="xs" variant="subtle" color="red" onClick={() => onDelete(collection, it)}>Delete</Button>
        </Group>
      ))}
    </Stack>
  )
}

export default function Settings({ prefs, onPrefs, onSaved }) {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const [accounts, setAccounts] = useState([])
  const [calendars, setCalendars] = useState([])
  const [dests, setDests] = useState([])
  const [pendingRestart, setPendingRestart] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState(null) // {collection, item, pruned} while a delete awaits confirmation
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const c = await loadCalendars()
      setAccounts(c.accounts); setCalendars(c.calendars); setDests(c.destinations)
      setPendingRestart(c.pendingRestart); setLoadError(c.loadError)
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  // Step 1: ask the server what the delete would remove (dry run, nothing is written).
  const askDelete = async (collection, item) => {
    setError('')
    try {
      const pruned = await deleteConfigItem(collection, item.id, true)
      setConfirm({ collection, item, pruned })
    } catch (e) { setError(e.message) }
  }
  // Step 2: after confirmation, delete for real, then refresh the lists and the sidebar.
  const doDelete = async () => {
    const { collection, item } = confirm
    setBusy(true)
    try {
      await deleteConfigItem(collection, item.id, false)
      setConfirm(null); setError(''); await load(); if (onSaved) onSaved()
    } catch (e) { setError(e.message); setConfirm(null) }
    setBusy(false)
  }
  const nameOf = (list, id) => (list.find(x => x.id === id)?.name) || id
  const describe = (p) => {
    if (p.kind === 'calendar') return `${KIND_NAMES.calendar} ${nameOf(calendars, p.id)} (${p.id})`
    if (p.kind === 'destination') return `${KIND_NAMES.destination} ${nameOf(dests, p.id)} (${p.id})`
    const [dest, cal] = p.id.split('/')
    return `Input ${nameOf(calendars, cal)} (${cal}) of destination ${nameOf(dests, dest)} (${dest})`
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
      {loadError && <Alert color="yellow" title="A config file could not be loaded; the previous settings are still running">{loadError}</Alert>}
      {pendingRestart && <Text size="sm" c="orange">Restart to apply server changes</Text>}
      {error && <Text size="sm" c="red">{error}</Text>}
      <Title order={4}>Accounts, calendars and destinations</Title>
      <Text size="sm" c="dimmed">
        These lists are read-only for now. Adding and editing accounts, calendars and destinations
        is not available in the app yet (a Settings update will add it); until then, edit
        config/calendars.yaml. You can delete an entry here; deleting an account or calendar also
        removes what depends on it, and you will see exactly what before you confirm.
      </Text>

      <Title order={5}>Accounts</Title>
      <ItemList collection="accounts" items={accounts} onDelete={askDelete} />
      <Title order={5}>Calendars</Title>
      <ItemList collection="calendars" items={calendars} onDelete={askDelete} />
      <Title order={5}>Destinations</Title>
      <ItemList collection="destinations" items={dests} onDelete={askDelete} />
      <Button size="xs" variant="light" onClick={() => window.open('/calendar.ics', '_blank')}>Export ICS</Button>

      <Divider />
      <Title order={4}>Notifications</Title>
      <Switch label="Conflicts" defaultChecked />
      <Switch label="New meetings" defaultChecked />
      <Switch label="Generated events" defaultChecked />
      <Switch label="Calendar shifts" defaultChecked />

      <Modal opened={!!confirm} onClose={() => setConfirm(null)} title={confirm ? `Delete ${COLLECTION_NAMES[confirm.collection]} ${confirm.item.name || confirm.item.id}?` : ''}>
        {confirm && (
          <Stack>
            <Text size="sm">id: {confirm.item.id}</Text>
            {confirm.pruned.length > 0 ? (
              <>
                <Text size="sm">This will also remove:</Text>
                <ul>{confirm.pruned.map(p => <li key={p.kind + ':' + p.id}>{describe(p)}</li>)}</ul>
              </>
            ) : <Text size="sm">Nothing else depends on it.</Text>}
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setConfirm(null)}>Cancel</Button>
              <Button color="red" loading={busy} onClick={doDelete}>Delete</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  )
}
