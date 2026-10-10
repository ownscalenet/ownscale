import { useState } from 'react'

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

const formatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

// relativeTime formats a time as "2 hours ago" or "in 3 days".
function relativeTime(date: Date, now = new Date()): string {
  const seconds = Math.round((date.getTime() - now.getTime()) / 1000)
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return formatter.format(Math.trunc(seconds / size), unit)
    }
  }
  return Math.abs(seconds) < 10 ? 'just now' : formatter.format(seconds, 'second')
}

// RelativeTime shows a readable duration; hover or click reveals the timestamp.
export function RelativeTime({ value }: { value: string | Date }) {
  const [open, setOpen] = useState(false)
  const date = new Date(value)
  if (isNaN(date.getTime())) return <>—</>
  return (
    <time
      dateTime={date.toISOString()}
      className={`tooltip tooltip-top cursor-pointer ${open ? 'tooltip-open' : ''}`}
      data-tip={date.toLocaleString()}
      onClick={() => setOpen((o) => !o)}
      onMouseLeave={() => setOpen(false)}
    >
      {relativeTime(date)}
    </time>
  )
}
