document "core/article" {
  transform_file = "article.ts"

  # The age of a news item is when it broke, not when it was last touched,
  # so articles are partitioned by their first distribution event. That
  # timestamp is immutable, so a document never moves between partitions,
  # and quarters age out into the archive as they pass.
  anchor = "first_published"

  # Articles are the semantic surface: they are chunked and embedded, so
  # they can be searched and subscribed to by vector. Needs a deployment
  # with an embedding sidecar, and only applies to indexes created after
  # it is applied.
  embeddings = true

  # The section the article was published in, so that a published-day view
  # can be narrowed to it. Extracted per version and stored with it, so a
  # version that was in Sport when it was published stays in Sport's day
  # even after a later version moves out - which is what a publication log
  # should say.
  #
  # The value is the section document's UUID rather than its name: a facet
  # filter matches exactly, and the client resolves display names itself.
  facet "section" {
    expression = ".links(rel='section')@{uuid}"
  }

  # Delivery fields are the vocabulary a delivery rule may name. Unlike a
  # facet, which narrows a view the caller is already looking at, these
  # are evaluated against standing rules for content the customer has not
  # asked for yet - so they are named for the customer's world, not for
  # the projection, and they are computable without the search index.
  #
  # A name means one thing across every type that declares it: the same
  # "section" is declared on planning items with the expression that
  # finds it there.
  delivery_field "section" {
    kind        = "keyword"
    expression  = ".links(rel='section')@{uuid}"
    description = "The section the content was published in."
  }

  # The editorial newsvalue, as a number so that a rule can ask for
  # "4 and up" rather than enumerating the scores.
  delivery_field "newsvalue" {
    kind        = "number"
    expression  = ".meta(type='core/newsvalue')@{value}"
    description = "Editorial newsvalue, 1-6, higher is more important."
  }

  # The headline, as text, so a rule can match words in it. This is what
  # a text condition tests: an extracted, bounded piece of the document,
  # not the body - the delivery matcher never reads bodies.
  delivery_field "headline" {
    kind        = "text"
    expression  = "@{title}"
    description = "The document headline."
  }
}
