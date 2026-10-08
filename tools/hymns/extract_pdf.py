"""Extracts a hymn book from a Word-exported PDF (two columns per page) to JSON.

Usage (see README.md):
    python extract_pdf.py BOOK.pdf --layout rendons --code CODE --title TITLE --language fr \
        --position 1 [--pages 5-77] > ../../data/hymns/CODE.json
The report of suspicious spots goes to stderr: read it before importing.

Two layouts are known:

rendons (Rendons ministère au Seigneur):
- hymn headers in bold, larger than the text: "12. Title" (a long title wraps on the next bold line);
- verses start with "1." or "1-", a chorus starts with a "Refrain" line (or is written in italics),
  possibly numbered and repeated: "Refrain 2 : x2" gives {"kind": "chorus", "label": "2", "repeat": 2}.

victor (Christ est vainqueur / Christ the Victor, both in the same PDF: use --pages):
- the hymn number alone on a bold line, no title (the title is the first line, as in the book's index);
- the first word of a hymn is in capitals ("1GRAND Dieu"), verses start with "2 " or "2.";
- a chorus follows a "Chœur :" / "Chorus :" line; "Ch." at the end of a line means "sing the chorus";
- the author (and the tune) in small print under the hymn.

In both, Word wraps a line that is too long for the column: its last word goes on the next line.
"""
import argparse
import json
import re
import statistics
import sys
import unicodedata

import pymupdf

SPACE_WIDTH = 3.0  # a space in the 11 pt text, in points

HEADER = re.compile(r"^(\d+)\s*[.\-)]\s*(.+)$")
VERSE = re.compile(r"^(\d+)\s*[.\-)]\s*(.*)$")
# victor: "1GRAND Dieu", "2 Puisse", "1. Quel", "2  Behold", "2- Nous"
VICTOR_VERSE = re.compile(r"^(\d{1,2})(?:\s*[.)\-]\s*|\s+|(?=[A-ZÀ-ÖØ-Þ]))(\S.*)$")
# "Refrain", "Refrain 2 : x2", "Chœur :"... but not "Reflète": the keyword must end there
CHORUS = re.compile(r"^(refrain|r[ée]f\.|chœur|choeur|chorus|r/)(?![^\W\d_])\s*(\d+)?\s*[:.]?\s*(.*)$", re.IGNORECASE)
# victor also writes "Ref :", "Réf.:", "Ref 2 :"
VICTOR_CHORUS = re.compile(r"^(refrain|r[ée]f|chœur|choeur|chorus|r/)(?![^\W\d_])\.?\s*(\d+)?\s*[:.]?\s*(.*)$", re.IGNORECASE)
VERSE_NUMBER_ALONE = re.compile(r"^(\d{1,2})\s*[.)]?$")
REPEAT = re.compile(r"^\(?\s*(x\s*\d+|\d\s*x|bis|ter)\s*\)?$", re.IGNORECASE)
TRAILING_REPEAT = re.compile(r"\s*\(?\s*\b(bis|ter|\d\s*x|x\s*\d)\s*\)?\s*$", re.IGNORECASE)
CHORUS_CUE = re.compile(r"\s*\b(Ch|R[ée]f)\.?\s*\d?\s*:?\s*$")
MISSPELT_CHORUS = re.compile(r"^c(?:oe|œ)ur(?=\s*\d*\s*:)", re.IGNORECASE)  # "Coeur:" for "Chœur :"  # victor: "... moi.  Ch." = sing the chorus after this verse


def clean(text):
    text = unicodedata.normalize("NFC", text)
    text = text.replace(" ", " ").replace("’", "'").replace("‘", "'")
    text = text.replace("‟", "'").replace("\t", " ")
    return re.sub(r"\s{2,}", " ", text).strip()


def repeat_count(text):
    """ "x2", "(x3)", "2x", "bis", "ter" -> how many times the part is sung."""
    marker = REPEAT.match(text.strip()).group(1).lower().replace(" ", "")
    return {"bis": 2, "ter": 3}.get(marker) or int(marker.replace("x", ""))


def line_style(spans, layout):
    main = max(spans, key=lambda s: len(s["text"].strip()))
    text = "".join(s["text"] for s in spans).strip()
    bold = bool(main["flags"] & 16) or "Bold" in main["font"]
    italic = bool(main["flags"] & 2) or "Italic" in main["font"]
    if text.isdigit() and "Calibri" in main["font"]:
        return "page"  # page number in the footer
    if layout == "victor":
        if text.isdigit() and all("Bold" in s["font"] for s in spans):
            return "number"
        if main["size"] < 9.5:
            return "credit"
        return "italic" if italic else "text"
    if bold and 15 <= main["size"] < 17.5:
        return "header"
    if bold and main["size"] >= 17.5:
        return "book"
    return "italic" if italic else "text"


def read_lines(path, layout, first_page=1, last_page=None):
    """Every line in reading order: page, then left column, then right column.
    Pieces of a justified line (same column, same height) are put back together."""
    doc = pymupdf.open(path)
    last_page = last_page or doc.page_count
    lines = []
    for page_number in range(first_page, last_page + 1):
        page = doc[page_number - 1]
        middle = page.rect.width / 2
        pieces = []
        for block in page.get_text("dict")["blocks"]:
            for line in block.get("lines", []):
                x0, y0, x1, _ = line["bbox"]
                pieces.append(dict(column=0 if x0 < middle else 1, y=y0, y1=line["bbox"][3], x0=x0, x1=x1,
                                   spans=line["spans"]))
        if layout == "victor":
            place_floating_boxes(pieces, middle)
        pieces.sort(key=lambda p: (p["column"], round(p["y"], 1), p["x0"]))

        merged = []
        for piece in pieces:
            last = merged[-1] if merged else None
            joinable = last and not is_number(piece) and not is_number(last) and any(
                s["text"].strip() for s in piece["spans"]) and any(s["text"].strip() for s in last["spans"])
            if joinable and last["column"] == piece["column"] and abs(last["y"] - piece["y"]) < 2.5:
                last["spans"] = last["spans"] + [{**piece["spans"][0], "text": " "}] + piece["spans"]
                last["x1"] = max(last["x1"], piece["x1"])
                last["y1"] = max(last["y1"], piece["y1"])
            else:
                merged.append(dict(piece))

        for piece in merged:
            spans = [s for s in piece["spans"] if s["text"].strip()]
            raw = "".join(s["text"] for s in piece["spans"])
            style = line_style(spans, layout) if spans else "blank"
            lines.append(dict(page=page_number, column=piece["column"], y=piece["y"], y1=piece["y1"], x0=piece["x0"],
                              x1=piece["x1"], text=clean(raw) if spans else "", style=style,
                              floating=piece.get("floating", False), box_continued=piece.get("box_continued", False),
                              # victor: a line of the song starts with two spaces, the end of a wrapped one doesn't
                              spaces=len(raw) - len(raw.lstrip(" "))))
    return lines


def is_number(piece):
    """A hymn number: a bold line holding only digits. Never glued to a line at its height."""
    spans = [s for s in piece["spans"] if s["text"].strip()]
    return bool(spans) and "".join(s["text"] for s in spans).strip().isdigit() and all(
        "Bold" in s["font"] for s in spans)


def place_floating_boxes(pieces, middle):
    """Word text boxes float across the columns: "               WE BLESS Thee, we praise Thee"
    starts in the left column but runs into the right one. Such a line belongs to the column
    where its words start, after its leading spaces; the next lines of the same box (just
    below) stay in that column."""
    box = None
    for piece in sorted(pieces, key=lambda p: p["y"]):
        text = "".join(s["text"] for s in piece["spans"])
        if not text.strip() or not (piece["x0"] < middle < piece["x1"] - 20):
            continue
        piece["floating"] = True
        if box is not None and 0 < piece["y"] - box["y"] < 16:
            piece["column"] = box["column"]
            piece["box_continued"] = True
        else:
            words_start = piece["x0"] + (len(text) - len(text.lstrip(" "))) * SPACE_WIDTH
            piece["column"] = 0 if words_start < middle else 1
        box = piece


def column_edges(lines):
    """Right edge of the text in each column: almost every line ends before it."""
    edges = {}
    for column in (0, 1):
        ends = [l["x1"] for l in lines if l["column"] == column and l["style"] in ("text", "italic")]
        edges[column] = statistics.quantiles(ends, n=100)[98] + 4 if len(ends) > 20 else max(ends, default=0)
    return edges


def first_word_crosses_edge(previous, line, edges):
    """Word wrapped [line] onto a new line when its first word did not fit after [previous]."""
    text = line["text"]
    first_word = text.split(" ")[0]
    width = (line["x1"] - line["x0"]) * len(first_word) / max(len(text), 1)
    return previous["x1"] + width + 3 >= edges[previous["column"]] - 12


# --- rendons ---------------------------------------------------------------------------

def wrapped_into(previous, line, edges):
    """True when [line] is the end of [previous], cut by Word.

    Lyrics often start a line in lowercase on purpose, and in uppercase almost always,
    so an uppercase start is only joined when it is clearly a leftover: one or two words,
    after a line that ends in the middle of a sentence ("... ils meurent pour" / "Toi.").
    """
    text, before = line["text"], previous["text"]
    if not text or not first_word_crosses_edge(previous, line, edges):
        return False
    if before.endswith("-"):
        return True  # "Saint-" / "Esprit"
    if text[0].islower() or text[0] in ",;:!?.»)":
        return True
    return len(text.split()) <= 2 and before[-1:].isalpha()


def parse_rendons(lines):
    edges = column_edges(lines)
    hymns, warnings = [], []
    hymn = part = None
    previous = None

    def warn(message, line):
        number = hymn["number"] if hymn else "-"
        warnings.append(f"p.{line['page']} hymn {number}: {message}: {line['text']!r}")

    def new_part(kind, label):
        nonlocal part
        part = {"kind": kind, "label": label, "lines": []}
        hymn["parts"].append(part)

    for line in lines:
        text, style = line["text"], line["style"]
        if style in ("blank", "book", "page"):
            previous = line if style == "blank" else None
            if style == "blank" and part is not None:
                part["closed"] = True
            continue

        if style == "header":
            match = HEADER.match(text)
            if match:
                hymn = {"number": int(match.group(1)), "title": match.group(2).strip(), "parts": [], "page": line["page"]}
                hymns.append(hymn)
                part = None
            elif hymn is not None and previous is not None and previous["style"] == "header":
                hymn["title"] = f"{hymn['title']} {text}".strip()  # title on two lines
            else:
                warn("bold line outside a title", line)
            previous = line
            continue

        if hymn is None:
            warn("text before the first hymn", line)
            continue

        # rest of a line that Word wrapped: glue it back
        if (previous is not None and previous["style"] in ("text", "italic") and previous["column"] == line["column"]
                and part is not None and part["lines"] and wrapped_into(previous, line, edges)):
            last = part["lines"][-1]
            part["lines"][-1] = f"{last}{text}" if last.endswith("-") else f"{last} {text}"
            previous = line
            continue

        # "x2", "(x3)", "bis" alone on a line: right after "Refrain", the whole chorus
        # is sung again; elsewhere it repeats the line above
        if REPEAT.match(text) and part is not None and not part["lines"]:
            part["repeat"] = repeat_count(text)
            previous = line
            continue
        if REPEAT.match(text) and part is not None and part["lines"]:
            part["lines"][-1] = f"{part['lines'][-1]} {text}"
            previous = line
            continue

        verse = VERSE.match(text)
        chorus = CHORUS.match(text)
        if chorus:
            new_part("chorus", chorus.group(2))
            rest = chorus.group(3).strip()
            if rest and REPEAT.match(rest):
                part["repeat"] = repeat_count(rest)  # "Refrain : x2"
            elif rest:
                part["lines"].append(rest)
        elif verse and (verse.group(2) or "").strip() != "" and not (
                part and part["kind"] == "verse" and part["label"] is None and not part.get("closed")):
            new_part("verse", verse.group(1))
            part["lines"].append(verse.group(2).strip())
        elif style == "italic" and (part is None or part.get("closed") or part["kind"] != "chorus"):
            # a chorus written in italics, without "Refrain"
            new_part("chorus", None)
            part["lines"].append(text)
        else:
            if part is None or (part.get("closed") and part["kind"] == "chorus" and style != "italic"):
                new_part("verse", None)
            part["lines"].append(text)
        previous = line

    return hymns, warnings


# --- victor ----------------------------------------------------------------------------

# words that keep their capital when the capitals of a first line are removed
NAMES = {"dieu", "jésus", "jesus", "christ", "seigneur", "esprit", "père", "eternel", "éternel", "agneau",
         "emmanuel", "sion", "roi", "lord", "god", "spirit", "father", "saviour", "savior", "king", "lamb", "zion"}


def without_capitals(line):
    """The first words of a hymn are printed in capitals: "GRAND Dieu" / "O NOM divin" /
    "THERE's a Man" / "L'HEURE approche" / "C'EST TOI JÉSUS". Back to normal case:
    "Grand Dieu", "O nom divin", "There's a Man", "L'heure approche", "C'est toi Jésus"."""
    tokens = line.split(" ")
    first = True
    for i, token in enumerate(tokens):
        if token in ("I", "O", "Ô", "A", "À") or not any(c.isalpha() for c in token):
            first = first and token not in ("I", "A", "À")
            continue  # "O ! DIEU merci"
        match = re.match(r"^([\"«“(]*)([A-ZÀ-ÖØ-Þ]['’])?([A-ZÀ-ÖØ-Þ]{2,})(.*)$", token)
        if not match:
            break
        quote, elision, word, rest = match.groups()
        word = word.lower()
        rest = re.sub(r"^(['’])([A-ZÀ-ÖØ-Þ]+)", lambda m: m.group(1) + m.group(2).lower(), rest)  # THERE'S
        if elision:
            word = (elision if first else elision.lower()) + word  # "L'heure", "c'est"
        elif first or word in NAMES:
            word = word[0].upper() + word[1:]
        tokens[i] = quote + word + rest
        first = False
    return " ".join(tokens)


STARTS_IN_CAPITALS = re.compile(r"^[\"«“(]*(?:[IOÔAÀ!,]\s+)*(?:[A-ZÀ-ÖØ-Þ]['’])?[A-ZÀ-ÖØ-Þ]{2,}")


def normalize_repeat(text):
    """ "Roi  bis" / "Roi (bis)" / "clouds. 2x" -> "Roi (bis)" / "clouds. (x2)" """
    match = TRAILING_REPEAT.search(text)
    if not match or match.start() == 0:
        return text
    marker = match.group(1).lower().replace(" ", "")
    marker = marker if marker in ("bis", "ter") else "x" + marker.replace("x", "")
    return f"{text[:match.start()].rstrip()} ({marker})"


def column_lefts(lines):
    """Left edge of the text in each column."""
    lefts = {}
    for column in (0, 1):
        x0s = [l["x0"] for l in lines if l["column"] == column and l["style"] in ("text", "italic")]
        lefts[column] = statistics.quantiles(x0s, n=20)[0] if len(x0s) > 20 else min(x0s, default=0)
    return lefts


def text_start(line, lefts):
    """Where the words of a line begin, from the left of its column, after its leading spaces."""
    return line["x0"] - lefts[line["column"]] + line["spaces"] * SPACE_WIDTH


def victor_wrapped(line, body_start, previous, edges, lefts):
    """True when [line] is the end of [previous], cut by Word.

    Within a hymn, the lines of the song all start at the same place [body_start]: after a few
    spaces ("  Nous prosternant devant"), or at an indent ("Sois adoré"), depending on the hymn;
    a chorus may be indented more than the verses, its lines then line up with each other.
    The end of a wrapped line starts further right than both ("Ton trône ô Dieu", "remplir.").
    On pages without any indent, it can only be told by its first word not fitting after [previous]."""
    text = line["text"]
    reference = body_start if VICTOR_VERSE.match(previous["text"]) else max(body_start, text_start(previous, lefts))
    offset = text_start(line, lefts) - reference
    if offset > 6:
        return True
    crosses = first_word_crosses_edge(previous, line, edges)
    if line["spaces"] >= 2:
        # a line of the song, unless a word or two pushed down by hand ("  neige.")
        return crosses and text[0].islower() and len(text.split()) <= 2
    if abs(offset) <= 4:
        return crosses and (text[0].islower() or text[0] in ",;:!?.»)")
    return crosses and (text[0].islower() or text[0] in ",;:!?.»)" or len(text.split()) <= 3)


def song_start(lines, lefts):
    """The usual start of the song lines of a hymn: the most common one."""
    starts = [round(text_start(l, lefts) / 2) * 2 for l in lines
              if l["style"] in ("text", "italic") and not VICTOR_VERSE.match(l["text"])
              and not VICTOR_CHORUS.match(l["text"])]
    return statistics.mode(starts) if starts else 0


def parse_victor(lines):
    edges = column_edges(lines)
    lefts = column_lefts(lines)
    warnings = []

    # 1. the lines of each hymn, from its bold number to the next one
    chunks = []
    for line in lines:
        if line["style"] == "number":
            chunks.append(({"number": int(line["text"]), "parts": [], "credits": [], "page": line["page"]}, []))
        elif chunks and line["style"] not in ("page", "blank"):
            current = chunks[-1][1]
            # the end of the previous hymn, in a text box that floats down next to this number
            if line["box_continued"] and len(chunks) > 1 and all(l["box_continued"] for l in current):
                chunks[-2][1].append(line)
            else:
                current.append(line)

    # a stanza ends where the white space above a line is wider than between the lines of a
    # stanza (measured bottom to top: a first line in big capitals is taller, not further away)
    text_lines = [l for l in lines if l["style"] not in ("blank", "page")]
    gaps = [b["y"] - a["y1"] for a, b in zip(text_lines, text_lines[1:])
            if a["page"] == b["page"] and a["column"] == b["column"] and -10 < b["y"] - a["y1"] < 30]
    stanza_gap = statistics.median(gaps) + 5 if gaps else 5

    hymns = []
    for hymn, hymn_lines in chunks:
        hymns.append(hymn)
        body_start = song_start(hymn_lines, lefts)
        part = previous = None
        above = None  # the line above, for the gap between stanzas
        title_at = None

        def new_part(kind, label):
            nonlocal part
            part = {"kind": kind, "label": label, "lines": []}
            hymn["parts"].append(part)

        def add_line(text):
            nonlocal title_at
            # the hymn's first words are in capitals, even when it starts with its chorus:
            # that line is the title, as in the book's index
            if title_at is None and STARTS_IN_CAPITALS.match(text):
                text = without_capitals(text)
                title_at = (part, len(part["lines"]))
            part["lines"].append(normalize_repeat(text))

        for line in hymn_lines:
            text, style = line["text"], line["style"]
            if style == "credit":
                hymn["credits"].append(text)
                continue
            if (above is not None and part is not None and above["page"] == line["page"]
                    and above["column"] == line["column"] and line["y"] - above["y1"] > stanza_gap):
                part["closed"] = True
            above = line

            text = MISSPELT_CHORUS.sub("Chœur", CHORUS_CUE.sub("", text))
            if not text:
                continue  # a lone "Ch." or "Ref."
            line = {**line, "text": text}

            verse = VICTOR_VERSE.match(text)
            alone = VERSE_NUMBER_ALONE.match(text)
            chorus = VICTOR_CHORUS.match(text)

            # rest of a line that Word wrapped: glue it back
            if (previous is not None and previous["column"] == line["column"] and part is not None and part["lines"]
                    and not verse and not alone and not chorus and not line["floating"]
                    and victor_wrapped(line, body_start, previous, edges, lefts)):
                last = part["lines"][-1]
                part["lines"][-1] = normalize_repeat(f"{last}{text}" if last.endswith("-") else f"{last} {text}")
                previous = line
                continue

            if REPEAT.match(text) and part is not None:
                if part["lines"]:
                    part["lines"][-1] = normalize_repeat(f"{part['lines'][-1]} {text}")
                else:
                    part["repeat"] = repeat_count(text)
            elif chorus:
                new_part("chorus", chorus.group(2))
                rest = chorus.group(3).strip()
                if rest and REPEAT.match(rest):
                    part["repeat"] = repeat_count(rest)
                elif rest:
                    add_line(rest)
            elif alone:
                new_part("verse", alone.group(1))  # "2." on its own line, the verse below
            elif verse:
                new_part("verse", verse.group(1))
                add_line(verse.group(2).strip())
            else:
                # a blank line then more lines: the next stanza, printed without its number,
                # or a chorus printed in italics without "Chorus"
                if part is None or part.get("closed"):
                    if style == "italic" and part is not None and part["kind"] == "chorus" and not part["lines"]:
                        pass  # the lines of a "Chœur :" after a blank line
                    else:
                        new_part("chorus" if style == "italic" else "verse", None)
                add_line(text)
            previous = line

        # a first verse printed without its number, before "2."
        verses = [p for p in hymn["parts"] if p["kind"] == "verse" and p["lines"]]
        if len(verses) > 1 and verses[0]["label"] is None and verses[1]["label"] == "2":
            verses[0]["label"] = "1"

        first = (title_at[0]["lines"][title_at[1]] if title_at else
                 next((p["lines"][0] for p in hymn["parts"] if p["lines"]), ""))
        hymn["title"] = TRAILING_REPEAT.sub("", first).rstrip(" ,;:.") or f"{hymn['number']}"
        credits = " ".join(hymn.pop("credits"))
        if credits:
            hymn["author"] = credits
    return hymns, warnings


# --- common ----------------------------------------------------------------------------

def finish(hymns):
    """Drops the parsing state and empty parts, and orders the keys of each hymn."""
    result = []
    for hymn in hymns:
        parts = []
        for part in hymn["parts"]:
            if not part["lines"]:
                continue
            clean_part = {"kind": part["kind"]}
            if part.get("label") is not None:
                clean_part["label"] = part["label"]
            if part.get("repeat"):
                clean_part["repeat"] = part["repeat"]
            clean_part["lines"] = part["lines"]
            parts.append(clean_part)
        ordered = {"number": hymn["number"], "title": hymn["title"]}
        if hymn.get("author"):
            ordered["author"] = hymn["author"]
        ordered["parts"] = parts
        ordered["page"] = hymn["page"]
        result.append(ordered)
    return result


def check(hymns, warnings):
    numbers = [h["number"] for h in hymns]
    expected = set(range(1, max(numbers) + 1)) if numbers else set()
    missing = sorted(expected - set(numbers))
    duplicates = sorted({n for n in numbers if numbers.count(n) > 1})
    if missing:
        warnings.append(f"missing numbers: {missing}")
    if duplicates:
        warnings.append(f"duplicate numbers: {duplicates}")
    for h in hymns:
        where = f"p.{h['page']} hymn {h['number']}"
        if not h["parts"]:
            warnings.append(f"{where}: no text")
        labels = [int(p["label"]) for p in h["parts"] if p["kind"] == "verse" and p.get("label", "").isdigit()]
        if labels and labels != list(range(1, len(labels) + 1)):
            warnings.append(f"{where}: verses numbered {labels}")
        for p in h["parts"]:
            for l in p["lines"]:
                if len(l) > 70:
                    warnings.append(f"{where}: long line {l!r}")
                if l[:1].islower():
                    warnings.append(f"{where}: line starts in lowercase {l!r}")
    return warnings


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("pdf")
    parser.add_argument("--layout", choices=("rendons", "victor"), default="rendons")
    parser.add_argument("--code", required=True)
    parser.add_argument("--title", required=True)
    parser.add_argument("--language", default="fr")
    parser.add_argument("--position", type=int, default=0, help="order of the book in the app")
    parser.add_argument("--pages", help="only these pages, e.g. 5-77")
    args = parser.parse_args()

    first, last = (int(p) for p in args.pages.split("-")) if args.pages else (1, None)
    lines = read_lines(args.pdf, args.layout, first, last)
    hymns, warnings = (parse_victor if args.layout == "victor" else parse_rendons)(lines)
    hymns = finish(hymns)
    warnings = check(hymns, warnings)
    for h in hymns:
        h.pop("page")
    json.dump(
        {"book": {"code": args.code, "title": args.title, "language": args.language, "position": args.position},
         "hymns": hymns},
        sys.stdout, ensure_ascii=False, indent=2,
    )
    sys.stdout.write("\n")
    print(f"{len(hymns)} hymns, {sum(len(h['parts']) for h in hymns)} parts, {len(warnings)} warnings", file=sys.stderr)
    for w in warnings:
        print("WARN", w, file=sys.stderr)


if __name__ == "__main__":
    main()
