# The other two published-on-a-date types. Both are backward-anchored for
# the same reason articles are: they are dated by when they went out, and
# the anchor is immutable so they never move between partitions.

# Flash news: the short breaking-news item that precedes the article.
document "core/flash" {
  # No transform script. A flash is title plus text blocks, and the dist
  # schema already describes the delivered shape, so there is nothing to
  # convert on the way out.
  anchor = "first_published"

  # Deliberately not embedded. A flash is a headline-length item that the
  # article supersedes within minutes, so vector hits on it would mostly
  # be duplicates of the article that follows.
  embeddings = false
}

# Editorial information: the notices editorial staff send to subscribers -
# "till redaktionerna" and the like.
document "core/editorial-info" {
  anchor = "first_published"

  embeddings = false
}
