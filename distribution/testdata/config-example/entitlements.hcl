# Entitlements are product tiers: content an organisation only sees once it
# has been granted the entitlement by name, with
#
#   distconf distribution entitlement grant <org> <entitlement>
#
# The configuration says what an entitlement hides; who holds it is the
# grants, which take effect immediately and outlive any one generation. A
# grant of an entitlement no active generation defines is kept, and does
# nothing until one does.
#
# Gates are evaluated when content is read or delivered, never stamped on
# what is stored, so changing one is an activation and not a rewrite. A
# document that the gates of several entitlements match requires all of
# them.

# A field gate: articles published in the premium section. The condition
# names a delivery field, and the type has to declare it twice - as a
# keyword delivery field and as a facet of the same name with the same
# expression - because the field is read from both: the delivery-field row
# on the hot paths, the facet in the daily views, which reach back past the
# point where the rows are pruned. core/article declares "section" both ways
# in article.hcl.
#
# The values are what the field extracts, matched exactly: a section is the
# UUID of its core/section document, never its name.
entitlement "preem" {
  description = "Premium tier: articles in the premium section."

  gate "core/article" {
    delivery_field = "section"
    values         = ["5f9b0e83-6a1e-4d5c-9b8a-3e2f1c0d7a64"]
  }
}

# A type gate: no condition, so every document of the type is hidden from
# an organisation without the grant. A search that names the type
# explicitly is refused rather than answered with nothing - type names are
# not secret, the delivery-field vocabulary lists them.
entitlement "event-calendar" {
  description = "The events calendar."

  gate "core/event" {}
}
