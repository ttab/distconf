document "core/planning-item" {
  # A planning item is about a date rather than published on one, so it is
  # partitioned by the date it covers: everything from the current quarter
  # onwards shares one index, and past quarters are carved out of it as
  # they pass.
  anchor = "time_expressions"

  time_expression {
    # No timezone: start_date is a bare calendar date, and partitions are
    # cut in UTC, so reading it as UTC is what keeps an item on the day it
    # says. Reading it in a zone east of UTC moves it back a day — and on
    # a quarter boundary, back a whole quarter.
    expression = ".meta(type='core/planning-item').data{start_date:date}"
  }

  embeddings = true

  # The section a planning item belongs to, so that the calendar can be
  # narrowed the same way the published day is. Same value as an article's:
  # the section document's UUID.
  facet "section" {
    expression = ".links(rel='section')@{uuid}"
  }
}
