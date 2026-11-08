package knowledge

import "embed"

// ArticlesFS embeds all markdown knowledge base articles (story NW-15).
//
//go:embed articles/*.md
var ArticlesFS embed.FS
