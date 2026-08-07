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

  # The same three public names as an article's, with the expressions that
  # find them on a planning item. A rule naming any of them works across
  # both types, which is what makes a name a public identifier rather than
  # a path into one document shape.
  delivery_field "section" {
    kind        = "keyword"
    expression  = ".links(rel='section')@{uuid}"
    description = "The section the content was published in."
  }

  delivery_field "newsvalue" {
    kind        = "number"
    expression  = ".meta(type='core/newsvalue')@{value}"
    description = "Editorial newsvalue, 1-6, higher is more important."
  }

  delivery_field "headline" {
    kind        = "text"
    expression  = "@{title}"
    description = "The document headline."
  }

  # Planning items only: an article is published from somewhere, a
  # planning item is about somewhere, and only the planning item carries
  # the link. A rule naming "place" is therefore a no-match on every
  # article - silently, because an absent key is a no-match by definition
  # - which is why GetDeliveryFields reports which types declare a field
  # and why an editor has to warn on a rule that mixes them.
  delivery_field "place" {
    kind        = "keyword"
    expression  = ".links(rel='place')@{uuid}"
    description = "The place the planned coverage is about."
  }
}
