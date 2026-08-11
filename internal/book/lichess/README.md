# lichess chess-openings (vendored)

The five TSV files (`a.tsv` … `e.tsv`) are the lichess opening-name dataset,
vendored verbatim from github.com/lichess-org/chess-openings
(commit 4b8622759e7ae6f93f011cc6c83a3823401ab45e, 2026-08-04). License: CC0 1.0
(public-domain dedication) — see `COPYING.txt`, copied from the same repository.

Columns are `eco⟨tab⟩name⟨tab⟩pgn` (SAN with move numbers), 3,810 openings.

This is the BIG BOOK's breadth source: `book.BuildBig` imports every line as a
weightless breadth entry for BOTH colors, subordinate to the hand-curated
`internal/book/openings.txt` (which alone defines the engine's repertoire and
weights, and always wins where the two overlap). `internal/book/eco.go` embeds
and parses these files; row order within a file, files in `a`..`e` order, is the
deterministic priority tiebreak between lines offering different moves at the
same position — the dataset is roughly canonical-line-first, so earlier rows
win. Nothing here reaches the SMALL resident book (`bookblob.bin`).
