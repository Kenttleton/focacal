import { createTheme } from '@mantine/core'

// System font stack: San Francisco on macOS, Segoe UI on Windows, and
// the platform default elsewhere. Nothing to bundle or license.
const SYSTEM_FONT = '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", system-ui, sans-serif'

export const theme = createTheme({
  fontFamily: SYSTEM_FONT,
  headings: { fontFamily: SYSTEM_FONT, fontWeight: '600' },
  primaryColor: 'blue',
  defaultRadius: 'md',
  cursorType: 'pointer',
})
