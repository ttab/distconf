# Delivery-time rendition generation for image assets. Rendition links
# are a delivery projection: the pipeline stores documents without
# rendition links, and the content API derives them from this
# configuration when serving.
renditions "image" {
  default_variants  = ["thumbnail", "preview", "hires"]
  default_extension = "jpg"

  # Media archive images. SDL media ids carry no prefix in the archive,
  # so the sdl prefix of the document URIs is stripped by the pattern.
  source "tt-archive" {
    namespace   = "mm"
    link_types  = ["tt/picture", "tt/graphic"]
    uri_pattern = "^https?://tt\\.se/media/image/sdl([A-Za-z0-9._-]+)$"
  }
}
