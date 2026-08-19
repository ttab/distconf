# Delivery-time HTML rendering. Every format other than raw newsdoc needs
# the document body as HTML, and "why can't we just get HTML?" is an ask in
# its own right, so a consumer opts into a rendered fragment on any surface
# that returns a document. The fragment is rendered when it is asked for and
# cached - never on the store path, where a renderer's failure profile would
# stall the pipeline.
html_rendering {
  # The rendition variant the fragment's images link to. It is
  # service-level by construction: one render serves every caller, so a
  # request cannot pick a variant of its own.
  image_variant = "preview"

  # Only articles render HTML. Left out, every configured type would -
  # including planning items, which nobody wants a body fragment of.
  document_types = ["core/article"]
}

# A renderer extension is invoked for a document its triggers match, gets
# one call carrying the whole document, and answers for whichever top-level
# blocks it likes. The built-in renderers keep everything it did not answer
# for. Where two renderers answer for one block the first in declaration
# order wins, so the order of these blocks decides the output.
#
# A renderer that fails - a timeout, an exception, a response we cannot
# verify - contributes nothing: the blocks it would have answered for fall
# back to the built-in rendering and the delivery goes out. That is what
# makes it safe to put a script in the middle of a contracted feed.
renderer "factbox" {
  kind = "js"

  # The render cache is keyed on the configuration that affects output, so
  # bump this whenever the script's output changes in a way that a cached
  # fragment must not keep serving.
  revision = 1

  # Resolved relative to this directory. The generation carries the script
  # itself, not the path.
  script_file = "factbox-render.js"

  # Why the renderer is called, not what it renders: a document with a
  # factbox in it gets a call, and the script decides from there. Left out
  # entirely, it would be called for every article.
  trigger {
    block_types = ["core/factbox"]
  }

  document_types = ["core/article"]

  # Everything a renderer returns is sanitized before it reaches a
  # consumer: the code runs outside the service and its output is embedded
  # in what a customer renders. Use policy_preset = "strict" or
  # "rich-text" to stay in step with the service's own policies instead.
  policy {
    elements    = ["aside", "h4", "p", "em", "strong", "a"]
    attributes  = { a = ["href"], aside = ["class"] }
    url_schemes = ["https"]
  }
}
