import { useState } from 'react'
import { Alert, Anchor, Button, Center, Paper, PasswordInput, Stack, Text, TextInput, Title } from '@mantine/core'
import { api, setToken } from './api'

export default function Login({ onAuthed }) {
  const [mode, setMode] = useState('login')
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const setup = mode === 'setup'

  const submit = async (e) => {
    e.preventDefault()
    setBusy(true); setError('')
    try {
      const r = await api(setup ? '/auth/setup' : '/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username: user, password: pass }),
      })
      if (r?.token) { setToken(r.token); onAuthed(r.token) }
    } catch (err) {
      setError(err.message === 'unauthorized' ? 'Wrong username or password.' : err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Center h="100%" p="md">
      <Paper w={360} maw="100%" p="xl" radius="lg" withBorder shadow="sm">
        <form onSubmit={submit}>
          <Stack gap="md">
            <div>
              <Title order={3}>{setup ? 'Create your account' : 'Sign in to focacal'}</Title>
              {setup && <Text size="sm" c="dimmed" mt={4}>This is the first run. Pick the admin login for this server.</Text>}
            </div>
            <TextInput label="Username" value={user} onChange={e => setUser(e.target.value)} autoComplete="username" data-autofocus required />
            <PasswordInput label="Password" value={pass} onChange={e => setPass(e.target.value)} autoComplete={setup ? 'new-password' : 'current-password'} required />
            {error && <Alert color="red" variant="light" p="xs">{error}</Alert>}
            <Button type="submit" loading={busy}>{setup ? 'Create account' : 'Sign in'}</Button>
            <Anchor component="button" type="button" size="sm" ta="center" onClick={() => { setMode(setup ? 'login' : 'setup'); setError('') }}>
              {setup ? 'Already have an account? Sign in' : 'First time here? Set up an account'}
            </Anchor>
          </Stack>
        </form>
      </Paper>
    </Center>
  )
}
