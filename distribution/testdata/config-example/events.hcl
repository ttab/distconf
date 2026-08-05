# Calendar events. Like a planning item an event is about a date rather
# than published on one, so it is forward-anchored and gets the
# upcoming-index layout: everything from the current quarter onwards shares
# one index, and past quarters are carved out of it as they pass.
document "core/event" {
  anchor = "time_expressions"

  time_expression {
    # No layout and no timezone: start is a full RFC 3339 instant for both
    # date and datetime granularity - a date-granularity event carries the
    # midnight boundary of its own timezone (a Swedish all-day event on
    # 2026-07-08 has start 2026-07-07T22:00:00Z), so the value is already
    # resolved and reading it as an instant is what keeps the event on its
    # own day. The consequence to know about: an all-day event on the first
    # day of a quarter is routed to the previous quarter, because that is
    # where its instant falls in UTC.
    expression = ".meta(type='core/event').data{start}"
  }

  embeddings = true
}
