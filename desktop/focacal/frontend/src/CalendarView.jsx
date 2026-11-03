import { useRef, useEffect } from 'react'
import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import interactionPlugin from '@fullcalendar/interaction'

export default function CalendarView({ events, view }) {
  const calRef = useRef(null)
  const fcView = view === 'day' ? 'timeGridDay' : view === 'week' ? 'timeGridWeek' : 'dayGridMonth'

  useEffect(() => {
    const api = calRef.current?.getApi()
    if (api) api.changeView(fcView)
  }, [view])

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
      ref={calRef}
      plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin]}
      initialView={fcView}
      headerToolbar={false}
      dayHeaderFormat={{ weekday: 'long' }}
      events={fcEvents}
      height="auto"
      dayHeaderClassNames="fc-dayheader-custom"
    />
  )
}
