import { forwardRef, useImperativeHandle, useRef, useEffect } from 'react'
import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import interactionPlugin from '@fullcalendar/interaction'

const CalendarView = forwardRef(function CalendarView({ events, view }, ref) {
  const calRef = useRef(null)
  const fcView = view === 'day' ? 'timeGridDay' : view === 'week' ? 'timeGridWeek' : 'dayGridMonth'

  useEffect(() => {
    const api = calRef.current?.getApi()
    if (api) api.changeView(fcView)
  }, [view])

  useImperativeHandle(ref, () => ({
    prev: () => calRef.current?.getApi().prev(),
    next: () => calRef.current?.getApi().next(),
    today: () => calRef.current?.getApi().today(),
    getTitle: () => calRef.current?.getApi().view.title,
  }))

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
})

export default CalendarView
