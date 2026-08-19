// An HTML renderer for core/factbox blocks. It is called once per document,
// with the whole document, and answers for the top-level blocks it wants to
// render - here the factboxes, addressed by the id each top-level block
// carries. Everything it leaves alone is rendered by the built-ins.
//
// Only top-level blocks are addressable: a renderer that wants to change
// something inside a factbox renders the factbox, which is what this does.
//
// The output is sanitized against the renderer's policy afterwards, but the
// escaping is still this script's job: the policy decides which markup
// survives, not what a text value means.
export function render(req) {
  const blocks = []

  for (const block of req.document.content || []) {
    if (block.type !== "core/factbox") {
      continue
    }

    blocks.push({
      id: block.id,
      html: factbox(block),
    })
  }

  return { blocks: blocks }
}

function factbox(block) {
  const parts = ['<aside class="factbox">']

  if (block.title) {
    parts.push("<h4>" + escapeText(block.title) + "</h4>")
  }

  for (const child of block.content || []) {
    if (child.type === "core/text" && child.data && child.data.text) {
      parts.push("<p>" + escapeText(child.data.text) + "</p>")
    }
  }

  parts.push("</aside>")

  return parts.join("")
}

function escapeText(text) {
  return String(text)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
}
