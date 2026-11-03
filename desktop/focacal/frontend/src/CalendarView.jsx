import { forwardRef, useImperativeHandle, useRef, useEffect, useState } from 'react'
import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import interactionPlugin from '@fullcalendar/interaction'

const CalendarView = forwardRef(function CalendarView({ events, view }, ref) {
  const calRef = useRef(null)
  const fcView = view === 'day' ? 'timeGridDay' : view === 'week' ? 'timeGridWeek' : 'dayGridMonth'
  const [nowLabel, setNowLabel] = useState('')

  useEffect(() => {
    const api = calRef.current?.getApi()
    if (api) api.changeView(fcView)
  }, [view])

  useEffect(() => {
    const update = () => {
      const d = new Date()
      setNowLabel(d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }))
    }
    update()
    const id = setInterval(update, 30000)
    return () => clearInterval(id)
  }, [view])

  useImperativeHandle(ref, () => ({
    prev: () => calRef.current?.getApi().prev(),
    next: () => calRef.current?.getApi().next(),
    today: () => calRef.current?.getApi().today(),
  }))

  const fcEvents = events.map(e => ({
    id: e.id,
    title: e.title,
    start: e.start,
    end: e.end,
    color: e.color || undefined,
    classNames: (e.conflict_keys && e.conflict_keys.length) ? ['conflict'] : [],
  }))

  const isTimeGrid = view === 'day' || view === 'week'

  return (
    <div className={isTimeGrid ? 'no-today-highlight' : ''} style={{ position: 'relative' }}>
      {isTimeGrid && (
        <div style={{
          position: 'absolute',
          left: 4,
          top: 4,
          background: '#e00',
          color: '#fff',
          fontSize: 11,
          padding: '2px 6px',
          borderRadius: 3,
          zIndex: 5,
          pointerEvents: 'none',
          fontWeight: 'bold',
        }}>{nowLabel}</div>
      )}
      <FullCalendar
        ref={calRef}
        plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin]}
        initialView={fcView}
        headerToolbar={false}
        dayHeaderFormat={{ weekday: 'long' }}
        nowIndicator={true}
        events={fcEvents}
        height="auto"
        dayHeaderClassNames="fc-dayheader-custom"
      />
    </div>
  )
})

export default CalendarView
