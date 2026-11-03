import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef } from 'react'
import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import interactionPlugin from '@fullcalendar/interaction'
import luxon3Plugin from '@fullcalendar/luxon3'
import { IconAlertTriangleFilled } from '@tabler/icons-react'

const VIEWS = { day: 'timeGridDay', week: 'timeGridWeek', month: 'dayGridMonth' }
const TIME_FORMAT = { hour: 'numeric', minute: '2-digit', meridiem: 'short', omitZeroMinute: true }

// FullCalendar's short meridiem is lowercase and unspaced ("9:30am"); show "9:30 AM".
const ampm = (text) => text.replace(/\s*(am|pm)\b/gi, (_, m) => ' ' + m.toUpperCase())

function EventContent({ arg }) {
  const { conflict } = arg.event.extendedProps
  const monthView = arg.view.type === 'dayGridMonth'
  const time = arg.event.allDay ? '' : ampm(arg.timeText)
  return (
    <div className="ev">
      <div className="ev-row">
        {conflict && <span className="ev-flag" title="Overlaps another event"><IconAlertTriangleFilled size={11} /></span>}
        {monthView && time && <span className="ev-time">{time}</span>}
        <span className="ev-title">{arg.event.title}</span>
      </div>
      {!monthView && time && <div className="ev-time">{time}</div>}
    </div>
  )
}

// Weekday over a large day number in time-grid views (Apple), weekday only in month.
// For named time zones FullCalendar passes header dates as UTC markers (the wall
// date in the display zone, encoded as UTC), so they are read as UTC here.
function DayHeader({ arg }) {
  const wd = arg.date.toLocaleDateString([], { weekday: 'short', timeZone: 'UTC' })
  if (arg.view.type === 'dayGridMonth') return <div className="dh dh-month"><span className="dh-wd">{wd}</span></div>
  return (
    <div className={`dh ${arg.isToday ? 'dh-today' : ''}`}>
      <span className="dh-wd">{wd}</span>
      <span className={`dn dn-lg ${arg.isToday ? 'dn-today' : ''}`}>{arg.date.getUTCDate()}</span>
    </div>
  )
}

const CalendarView = forwardRef(function CalendarView(
  { items, view, tz, firstDay, scrollTime, onRange, onSelect }, ref,
) {
  const calRef = useRef(null)
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return }
    // Deferred: FullCalendar flushes synchronously, which React forbids mid-commit.
    const id = setTimeout(() => calRef.current?.getApi().changeView(VIEWS[view]), 0)
    return () => clearTimeout(id)
  }, [view])
  useImperativeHandle(ref, () => ({
    prev: () => calRef.current?.getApi().prev(),
    next: () => calRef.current?.getApi().next(),
    today: () => calRef.current?.getApi().today(),
    goto: (date) => calRef.current?.getApi().gotoDate(date),
  }))

  const events = useMemo(() => items.map(it => ({
    id: it.id,
    title: it.title,
    start: it.start,
    end: it.end,
    allDay: !!it.all_day,
    classNames: it.type !== 'real' ? ['ev-generated'] : [],
    extendedProps: { raw: it, color: it.color, kind: it.type, conflict: it.conflict },
  })), [items])

  return (
    <div className="cal-wrap">
      <FullCalendar
        ref={calRef}
        plugins={[dayGridPlugin, timeGridPlugin, interactionPlugin, luxon3Plugin]}
        initialView={VIEWS[view]}
        headerToolbar={false}
        timeZone={tz}
        firstDay={firstDay}
        height="100%"
        events={events}
        eventDisplay="block"
        dayMaxEvents
        nowIndicator
        allDayText=""
        slotDuration="00:30:00"
        slotLabelInterval="01:00"
        slotLabelFormat={{ hour: 'numeric', meridiem: 'short' }}
        slotLabelContent={arg => ampm(arg.text)}
        slotEventOverlap={false}
        eventTimeFormat={TIME_FORMAT}
        scrollTime={scrollTime}
        scrollTimeReset={false}
        expandRows
        dayHeaderContent={arg => <DayHeader arg={arg} />}
        dayCellContent={arg => arg.view.type !== 'dayGridMonth' ? null : (
          <span className={`dn ${arg.isToday ? 'dn-today' : ''}`}>{arg.dayNumberText}</span>
        )}
        eventContent={arg => <EventContent arg={arg} />}
        eventDidMount={info => info.el.style.setProperty('--ev', info.event.extendedProps.color)}
        eventClick={info => onSelect(info.event.extendedProps.raw, info.el.getBoundingClientRect())}
        datesSet={info => onRange({ start: info.start, end: info.end })}
      />
    </div>
  )
})

export default CalendarView
