function ensure_html_caption(b: Block): Block {
	if (nd.get_data(b, "html_caption", "") !== "") {
		return b
	}

	const caption = nd.get_data(b, "caption", "");
	if (caption === "") {
		return b
	}

	b.data = nd.upsert_data(b.data, {
		html_caption: html.encode(caption),
	})

	return b
}

// visual_to_image converts a tt/visual block to a core/image block. The
// visual's picture/graphic self-link becomes the image link, and the
// link's credit/height/width move into the block data.
function visual_to_image(b: Block): Block {
	const self = nd.first_block(b.links, (l: Block) =>
		l.rel === "self" &&
			(l.type === "tt/picture" || l.type === "tt/graphic"))
	if (self == null) {
		return b
	}

	const data: Record<string, string> = {
		text: nd.get_data(b, "caption", ""),
		credit: nd.get_data(self, "credit", ""),
		height: nd.get_data(self, "height", ""),
		width: nd.get_data(self, "width", ""),
	}

	const htmlCaption = nd.get_data(b, "html_caption", "")
	if (htmlCaption !== "") {
		data.html_caption = htmlCaption
	}

	b.type = "core/image"
	b.data = data

	b.links = nd.drop_blocks(b.links, { rel: "self" })
	b.links.push({
		rel: "image",
		type: self.type,
		uri: self.uri,
		url: self.url,
	})

	return b
}

// Should only be present in stage.
const oldTextBlocks : Record<string, string> = {
	"core/heading-1": "heading-1",
	"core/heading-2": "heading-2",
	"tt/dateline": "vignette",
	"core/paragraph": "",
}

function transform(doc: Document): Document {
	// Ensure a newsvalue block exists.
	const default_nv: Block = { type: "core/newsvalue", value: "1" };
	doc.meta = nd.upsert_block(
		doc.meta,
		{ type: "core/newsvalue" },
		default_nv,
		(b: Block) => b,
	)

	if (doc.language === "sv") {
		doc.language = "sv-se"
	}

	// Ensure tt/visual and core/image blocks have html_caption.
	doc.content = nd.alter_blocks(
		doc.content,
		(b: Block) => b.type === "tt/visual" || b.type === "core/image",
		(b: Block) => ensure_html_caption(b),
	)

	// Convert tt/visual blocks to core/image.
	doc.content = nd.alter_blocks(
		doc.content,
		{ type: "tt/visual" },
		(b: Block) => visual_to_image(b),
	)

	// Upgrade text blocks
	doc.content = nd.alter_blocks(
		doc.content,
		(b: Block) => (b.type != undefined) && oldTextBlocks[b.type] != undefined,
		(b: Block) => {
			const role = oldTextBlocks[b.type]
			
			b.type = "core/text"
			b.role = role

			return b
		},
	)

	const heading = nd.first_block(doc.content,
		(b: Block) => b.type === "core/text" && b.role === "heading-1"
	)

	if (heading != null) {
		doc.title = html.decode(html.strip(nd.get_data(heading, "text")))
	}

	return doc
}
