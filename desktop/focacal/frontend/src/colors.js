// Calendar colors. Sources are assigned from this palette by position in
// the calendars config order; Focacal-generated blocks use fixed per-kind colors.
export const SOURCE_PALETTE = ['#0a84ff', '#bf5af2', '#ff9f0a', '#30d158', '#ff453a', '#64d2ff', '#ac8e68']

export const KIND_COLORS = {
  focus: '#30b050',
  lunch: '#ff9500',
  break: '#8e8e93',
}

export const FOCACAL_SOURCE = 'focacal'

export function sourceColors(sources) {
  const map = {}
  ;(sources || []).forEach((s, i) => { map[s.id] = SOURCE_PALETTE[i % SOURCE_PALETTE.length] })
  return map
}

export const colorFor = (item, byId) =>
  item.type && item.type !== 'real' ? (KIND_COLORS[item.type] || KIND_COLORS.focus) : (byId[item.source] || SOURCE_PALETTE[0])
