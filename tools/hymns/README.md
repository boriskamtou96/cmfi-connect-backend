# Hymn books

The hymns live in `data/hymns/<code>.json`, one file per book, imported into the
`hymn_books` and `hymns` tables (migration 000014). The JSON files are the source of truth:
to fix a typo, edit the file and import it again.

## 1. Extract a PDF

The books are Word documents exported to PDF, two columns per page. `extract_pdf.py` knows two
layouts (details at the top of the script):

- `rendons`: titles in bold ("12. Title"), verses "1." or "1-", choruses after "Refrain" or in italics;
- `victor`: the number alone in bold, no title (the first line, printed in capitals, becomes the
  title), choruses after "Chœur :" / "Chorus :" or in italics, credits in small print (`author`).

```bash
python3 -m venv /tmp/hymns-venv && /tmp/hymns-venv/bin/pip install -r tools/hymns/requirements.txt
PY=/tmp/hymns-venv/bin/python

$PY tools/hymns/extract_pdf.py "Rendons ministère à Christ.pdf" --layout rendons \
  --code rendons-ministere-1 --title "Rendons ministère au Seigneur, volume 1" --language fr --position 1 \
  > data/hymns/rendons-ministere-1.json

# one PDF, two books: French pages 5-77, English pages 83-156
$PY tools/hymns/extract_pdf.py Christ_est_Vainqueur.pdf --layout victor --pages 5-77 \
  --code christ-est-vainqueur --title "Christ est vainqueur" --language fr --position 2 \
  > data/hymns/christ-est-vainqueur.json
$PY tools/hymns/extract_pdf.py Christ_est_Vainqueur.pdf --layout victor --pages 83-156 \
  --code christ-the-victor --title "Christ the Victor" --language en --position 3 \
  > data/hymns/christ-the-victor.json
```

The report on stderr lists what to check by hand: missing or duplicate numbers, verses out
of order, very long lines (often two lines glued together), lines starting in lowercase (often
a line that Word wrapped; many are on purpose in the French books). Typos of the books
themselves are kept: fix them in the JSON files, which are the source of truth.
A book with another layout needs a new layout in `extract_pdf.py`.

## 2. Import

```bash
go run ./cmd/import-hymns -dry-run data/hymns/*.json   # check only
go run ./cmd/import-hymns data/hymns/*.json            # create or update (uses DB_DSN from .env)
go run ./cmd/import-hymns -prune data/hymns/x.json     # also delete hymns removed from the file
```

Importing an unchanged file changes nothing: the book keeps its version, so the apps don't
download it again.

## 3. API (authenticated)

- `GET /v1/hymns/books`: the books with `hymn_count` and `version`.
- `GET /v1/hymns/books/:code`: a whole book with its hymns, with an `ETag`;
  send it back in `If-None-Match` to get `304 Not Modified` when nothing changed.
- `GET /v1/hymns/books/:code/:number`: one hymn.

A hymn: `{"number", "title", "author", "first_line", "parts": [...]}` (`author` only when the book
prints one), each part being
`{"kind": "verse" | "chorus", "label": "1", "repeat": 2, "lines": [...]}`:
`label` is the printed number (verse 1, "Refrain 2"), absent for a chorus without number;
`repeat` (absent = once) comes from "Refrain : x2".
