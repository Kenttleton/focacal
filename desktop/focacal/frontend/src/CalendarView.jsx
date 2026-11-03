import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import interactionPlugin from '@fullcalendar/interaction'

export default function CalendarView({ events, view }) {
  const initialView = view === 'day' ? 'timeGridDay' : view === 'week' ? 'timeGridWeek' : 'dayGridMonth'
  const fcEvents = events.map(e => ({
    id: e.id,
    title: e.title,
    start: e.start,
    end: e.end,
    color: e.color || undefined,
    classNames: (e.conflict_keys && e.conflict_keys.length) ? ['conflict'] : [],
  }))
  return (
    <FullCalendar
      plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin]}
      initialView={initialView}
      headerToolbar={false}
      events={fcEvents}
      height="auto"
    />
  )
}
