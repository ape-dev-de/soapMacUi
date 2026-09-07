package soap

import (
	"fmt"
	"strings"
)

// InsertXOPReference setzt einen xop:Include an die Cursorposition im Envelope
// und stellt sicher, dass das xop-Präfix deklariert ist.
//
// Das ist der Handgriff, der in SoapUI aus vier Schritten besteht: anhängen,
// Content-ID erzeugen, kopieren, an der richtigen Stelle einfügen.
//
// Die Regeln, in dieser Reihenfolge:
//  1. Ist etwas markiert, wird die Markierung ersetzt.
//  2. Steht der Cursor in einer Zeile der Form <e>?</e> oder <e></e>,
//     wird dieser Inhalt ersetzt — der Normalfall bei einem erzeugten
//     Beispiel-Request.
//  3. Sonst wird an der Cursorposition eingefügt.
//
// Zurück kommen der neue Body und die Markierung des eingefügten Verweises,
// damit der Editor ihn hervorheben kann.
func InsertXOPReference(body string, selStart, selEnd int, cid string) (string, int, int, error) {
	if cid == "" {
		return "", 0, 0, fmt.Errorf("keine Content-ID angegeben")
	}

	// Die Namespace-Deklaration verlängert den Envelope-Starttag. Alles, was
	// dahinter liegt, verschiebt sich — auch der Cursor.
	body, nsAt, nsLen := ensureXOPNamespace(body)
	if nsLen > 0 {
		if selStart >= nsAt {
			selStart += nsLen
		}
		if selEnd >= nsAt {
			selEnd += nsLen
		}
	}

	n := len(body)
	selStart, selEnd = clamp(selStart, 0, n), clamp(selEnd, 0, n)
	if selEnd < selStart {
		selStart, selEnd = selEnd, selStart
	}

	ref := fmt.Sprintf(`<xop:Include href="cid:%s"/>`, cid)

	// 1. Markierung ersetzen
	if selEnd > selStart {
		return body[:selStart] + ref + body[selEnd:], selStart, selStart + len(ref), nil
	}

	// 2. Platzhalter am Cursor ersetzen
	if a, b, ok := placeholderAt(body, selStart); ok {
		return body[:a] + ref + body[b:], a, a + len(ref), nil
	}

	// 3. An der Cursorposition einfügen
	return body[:selStart] + ref + body[selStart:], selStart, selStart + len(ref), nil
}

// placeholderAt findet den Textknoten am Cursor, sofern er ein Platzhalter ist.
//
// Entscheidend ist die Prüfung auf ein *schliessendes* Folgetag: nur dann steht
// der Textknoten wirklich im Inhalt eines Elements. Zwischen <Envelope> und
// <Body> liegt ebenfalls ein leerer Textknoten — dort darf nichts hin.
func placeholderAt(body string, pos int) (int, int, bool) {
	pos = clamp(pos, 0, len(body))

	// Steht der Cursor innerhalb eines Tags, hinter dessen Ende springen.
	if lt, gt := strings.LastIndexByte(body[:pos], '<'), strings.LastIndexByte(body[:pos], '>'); lt > gt {
		rel := strings.IndexByte(body[pos:], '>')
		if rel < 0 {
			return 0, 0, false
		}
		pos += rel + 1
	}

	start := strings.LastIndexByte(body[:pos], '>') + 1
	rel := strings.IndexByte(body[pos:], '<')
	if rel < 0 {
		return 0, 0, false
	}
	end := pos + rel
	if start > end {
		return 0, 0, false
	}
	if !strings.HasPrefix(body[end:], "</") {
		return 0, 0, false
	}
	switch strings.TrimSpace(body[start:end]) {
	case "?", "":
		return start, end, true
	default:
		return 0, 0, false
	}
}

// ensureXOPNamespace ergänzt xmlns:xop am Envelope, falls es fehlt.
// Zurück kommen der neue Body, die Einfügeposition und die eingefügte Länge.
func ensureXOPNamespace(body string) (string, int, int) {
	if strings.Contains(body, XOPNamespace) {
		return body, 0, 0
	}
	m := envelopeStart.FindStringIndex(body)
	if m == nil {
		return body, 0, 0
	}
	// Direkt vor dem abschliessenden ">" bzw. "/>" des Starttags einsetzen.
	at := m[1] - 1
	if at > m[0] && body[at-1] == '/' {
		at--
	}
	decl := fmt.Sprintf(` xmlns:xop=%q`, XOPNamespace)
	return body[:at] + decl + body[at:], at, len(decl)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
