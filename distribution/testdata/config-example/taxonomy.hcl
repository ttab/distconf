# The taxonomy: small, slowly changing registries that are referenced by
# content of every era. They are bounded collections, so a consumer can
# fetch one whole by type and language instead of following the log for
# it, and they take no anchor - they are never partitioned and always stay
# in the recent set.
document "core/story" {
  bounded_collection = true
}

document "core/category" {
  bounded_collection = true
}

document "core/section" {
  bounded_collection = true
}

document "core/person" {
  bounded_collection = true
}

document "core/organisation" {
  bounded_collection = true
}

document "core/place" {
  bounded_collection = true
}
