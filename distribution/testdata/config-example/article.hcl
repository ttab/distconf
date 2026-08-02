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
}
