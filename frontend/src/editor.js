// CodeMirror-Editor für den SOAP-Body.
//
// Nach aussen sieht das Objekt aus wie ein <textarea> (value, selectionStart,
// setRangeText …), damit der Rest der Oberfläche unverändert damit arbeitet.
// Entscheidend: CodeMirror normalisiert das Dokument nicht — was im Editor
// steht, kommt bei doc.toString() byte-genau wieder heraus.

import { EditorState, Compartment, StateEffect, StateField, RangeSetBuilder } from "@codemirror/state";
import {
  EditorView, keymap, lineNumbers, highlightActiveLine,
  highlightActiveLineGutter, drawSelection, rectangularSelection,
  crosshairCursor, highlightSpecialChars, Decoration, WidgetType, ViewPlugin,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { searchKeymap, highlightSelectionMatches, search, openSearchPanel } from "@codemirror/search";
import {
  foldGutter, foldKeymap, foldAll, unfoldAll, codeFolding,
  bracketMatching, indentOnInput, indentUnit,
  syntaxHighlighting, HighlightStyle,
} from "@codemirror/language";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { xml } from "@codemirror/lang-xml";
import { tags as t } from "@lezer/highlight";

// Farben wie im übrigen Fenster: Navy-Black mit Purple-Akzent.
const palette = HighlightStyle.define([
  { tag: [t.tagName, t.angleBracket], color: "#b98cff" },
  { tag: t.attributeName, color: "#7fd6c0" },
  { tag: [t.attributeValue, t.string], color: "#e8b86d" },
  { tag: t.comment, color: "#5d6685", fontStyle: "italic" },
  { tag: t.processingInstruction, color: "#8b93b5" },
  { tag: t.content, color: "#dfe3f2" },
  { tag: t.invalid, color: "#fb7185" },
]);

const theme = EditorView.theme({
  "&": {
    height: "100%",
    fontSize: "12px",
    backgroundColor: "transparent",
    color: "#dfe3f2",
  },
  ".cm-scroller": {
    fontFamily: 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, monospace',
    lineHeight: "1.55",
    overflow: "auto",
  },
  ".cm-content": { padding: "10px 0", caretColor: "#c4b5fd" },
  ".cm-gutters": {
    backgroundColor: "transparent",
    color: "rgba(120,128,160,.42)",
    border: "none",
    borderRight: "1px solid rgba(148,130,255,.07)",
  },
  ".cm-activeLineGutter": { backgroundColor: "rgba(167,139,250,.07)", color: "#a78bfa" },
  ".cm-activeLine": { backgroundColor: "rgba(167,139,250,.045)" },
  ".cm-foldGutter span": { color: "#7c7f9e", cursor: "pointer" },
  ".cm-foldPlaceholder": {
    backgroundColor: "rgba(124,58,237,.28)",
    border: "1px solid rgba(167,139,250,.4)",
    color: "#dcd3ff",
    borderRadius: "4px",
    padding: "0 6px",
    margin: "0 2px",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "rgba(167,139,250,.32)",
  },
  ".cm-cursor": { borderLeftColor: "#c4b5fd" },
  ".cm-matchingBracket, &.cm-focused .cm-matchingBracket": {
    backgroundColor: "rgba(167,139,250,.25)",
    outline: "1px solid rgba(167,139,250,.5)",
  },
  ".cm-att": {
    display: "inline-flex",
    alignItems: "center",
    gap: "5px",
    padding: "0 7px",
    margin: "0 1px",
    borderRadius: "6px",
    border: "1px solid rgba(167,139,250,.42)",
    background: "linear-gradient(135deg, rgba(124,58,237,.34), rgba(192,132,252,.18))",
    color: "#efe9ff",
    cursor: "pointer",
    verticalAlign: "baseline",
  },
  ".cm-att:hover": { filter: "brightness(1.18)" },
  ".cm-att-act": {
    marginLeft: "1px",
    padding: "0 3px",
    borderRadius: "4px",
    opacity: ".55",
    fontSize: "11px",
  },
  ".cm-att-act:hover": { opacity: "1", background: "rgba(255,255,255,.16)" },
  ".cm-att-size": { opacity: ".65", fontSize: "10px" },
  ".cm-att-missing": {
    borderColor: "rgba(251,191,36,.5)",
    background: "rgba(251,191,36,.14)",
    color: "#fbbf24",
  },
  ".cm-searchMatch": { backgroundColor: "rgba(232,184,109,.28)" },
  ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: "rgba(232,184,109,.5)" },
  ".cm-panels": { backgroundColor: "rgba(11,15,30,.96)", color: "#e9ebf5" },
  ".cm-panels input, .cm-panels button": {
    backgroundColor: "rgba(3,5,12,.6)",
    color: "#e9ebf5",
    border: "1px solid rgba(148,130,255,.2)",
    borderRadius: "5px",
    padding: "2px 6px",
  },
}, { dark: true });


/* ---------------------------------------------------------------- Anhänge
 *
 * Ein <xop:Include href="cid:…"/> wird als Datei-Chip dargestellt. Ersetzt
 * wird nur die *Darstellung*: das Dokument enthält weiterhin genau das
 * Element, und doc.toString() liefert es byte-genau zurück.
 *
 * Steht der Cursor im Bereich, bleibt der Rohtext sichtbar — sonst käme man
 * nicht mehr an das Attribut heran.
 */

/** setAttachmentsEffect übergibt die Metadaten (cid -> {name, size}). */
export const setAttachmentsEffect = StateEffect.define();

const attachmentsField = StateField.define({
  create: () => ({}),
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setAttachmentsEffect)) return e.value;
    return value;
  },
});

const XOP_RE = /<xop:Include\b[^>]*?href\s*=\s*"cid:([^"]+)"[^>]*?\/>/g;

function humanSize(n) {
  if (!n && n !== 0) return "";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MiB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KiB";
  return n + " B";
}

class AttachmentWidget extends WidgetType {
  constructor(cid, meta, handlers, from, to) {
    super();
    this.cid = cid;
    this.meta = meta;
    this.h = handlers || {};
    this.from = from;
    this.to = to;
  }

  eq(other) {
    return other.cid === this.cid &&
      other.from === this.from && other.to === this.to &&
      other.meta?.name === this.meta?.name &&
      other.meta?.size === this.meta?.size;
  }

  toDOM() {
    const wrap = document.createElement("span");
    wrap.className = "cm-att" + (this.meta ? "" : " cm-att-missing");
    wrap.title = this.meta
      ? `${this.meta.name} · cid:${this.cid}\nKlick öffnet den Reiter Anhänge`
      : `Kein Anhang mit cid:${this.cid} an diesem Request`;

    const ico = document.createElement("span");
    ico.className = "cm-att-ico";
    ico.textContent = this.meta ? "📎" : "⚠";
    wrap.appendChild(ico);

    const name = document.createElement("span");
    name.className = "cm-att-name";
    name.textContent = this.meta ? this.meta.name : this.cid;
    wrap.appendChild(name);

    if (this.meta) {
      const size = document.createElement("span");
      size.className = "cm-att-size";
      size.textContent = humanSize(this.meta.size);
      wrap.appendChild(size);
    }

    const act = (label, title, fn) => {
      const b = document.createElement("span");
      b.className = "cm-att-act";
      b.textContent = label;
      b.title = title;
      b.addEventListener("mousedown", (e) => {
        e.preventDefault();
        e.stopPropagation();
        fn();
      });
      return b;
    };
    wrap.appendChild(act("⇄", "Datei ersetzen",
      () => this.h.onReplace?.(this.cid, this.from, this.to)));
    wrap.appendChild(act("✕", "Verweis entfernen",
      () => this.h.onRemove?.(this.cid, this.from, this.to)));

    wrap.addEventListener("mousedown", (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.h.onClick?.(this.cid, !!this.meta);
    });
    return wrap;
  }

  // false heisst: Ereignisse im Widget nicht an den Editor weiterreichen.
  ignoreEvent() { return false; }
}

function buildAttachmentDecorations(view, handlers) {
  const known = view.state.field(attachmentsField, false) || {};
  const sel = view.state.selection.main;
  const builder = new RangeSetBuilder();

  for (const { from, to } of view.visibleRanges) {
    const text = view.state.doc.sliceString(from, to);
    XOP_RE.lastIndex = 0;
    let m;
    while ((m = XOP_RE.exec(text)) !== null) {
      const start = from + m.index;
      const end = start + m[0].length;
      // Cursor im Bereich: Rohtext zeigen, damit er bearbeitbar bleibt.
      if (sel.from <= end && sel.to >= start) continue;
      builder.add(start, end, Decoration.replace({
        widget: new AttachmentWidget(m[1], known[m[1]] || null, handlers, start, end),
      }));
    }
  }
  return builder.finish();
}

function attachmentWidgets(handlers) {
  return ViewPlugin.fromClass(
    class {
      constructor(view) { this.decorations = buildAttachmentDecorations(view, handlers); }
      update(u) {
        if (u.docChanged || u.selectionSet || u.viewportChanged ||
            u.transactions.some((t) => t.effects.some((e) => e.is(setAttachmentsEffect)))) {
          this.decorations = buildAttachmentDecorations(u.view, handlers);
        }
      }
    },
    {
      decorations: (v) => v.decorations,
      // Pfeiltasten sollen über den Chip springen, nicht hineinlaufen.
      provide: (plugin) => EditorView.atomicRanges.of((view) => view.plugin(plugin)?.decorations || Decoration.none),
    },
  );
}

/** mount hängt einen Editor an parent und liefert die textarea-ähnliche Fassade. */
export function mount(parent, opts = {}) {
  const readOnlyC = new Compartment();
  let silent = false;

  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: "",
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        highlightActiveLine(),
        highlightSpecialChars(),
        history(),
        drawSelection(),
        rectangularSelection(),
        crosshairCursor(),
        codeFolding(),
        foldGutter({
          markerDOM(open) {
            const s = document.createElement("span");
            s.textContent = open ? "▾" : "▸";
            s.style.padding = "0 3px";
            return s;
          },
        }),
        indentOnInput(),
        indentUnit.of("  "),
        bracketMatching(),
        closeBrackets(),
        search({ top: true }),
        highlightSelectionMatches(),
        xml(),
        syntaxHighlighting(palette),
        attachmentsField,
        attachmentWidgets({
          onClick: opts.onAttachmentClick,
          onRemove: opts.onAttachmentRemove,
          onReplace: opts.onAttachmentReplace,
        }),
        theme,
        readOnlyC.of(EditorState.readOnly.of(false)),
        keymap.of([
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...historyKeymap,
          ...searchKeymap,
          ...foldKeymap,
          indentWithTab,
          ...(opts.keymap || []),
        ]),
        EditorView.updateListener.of((u) => {
          if (u.docChanged && !silent) opts.onChange?.();
        }),
      ],
    }),
  });

  return {
    view,

    get value() { return view.state.doc.toString(); },
    set value(v) { this.setDoc(v, false); },

    /** setDoc ersetzt den Inhalt. silent=true unterdrückt onChange —
     *  nötig beim Laden, damit nichts fälschlich als geändert gilt. */
    setDoc(v, quiet = true) {
      silent = quiet;
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v ?? "" } });
      silent = false;
    },

    get selectionStart() { return view.state.selection.main.from; },
    get selectionEnd() { return view.state.selection.main.to; },

    setSelectionRange(a, b) {
      const n = view.state.doc.length;
      const from = Math.max(0, Math.min(a ?? 0, n));
      const to = Math.max(0, Math.min(b ?? from, n));
      view.dispatch({ selection: { anchor: from, head: to }, scrollIntoView: true });
    },

    setRangeText(text, a, b) {
      view.dispatch({
        changes: { from: a, to: b, insert: text },
        selection: { anchor: a + text.length },
      });
    },

    select() {
      view.dispatch({ selection: { anchor: 0, head: view.state.doc.length } });
    },

    focus() { view.focus(); },
    foldAll() { foldAll(view); },
    unfoldAll() { unfoldAll(view); },
    openSearch() { openSearchPanel(view); },

    setReadOnly(ro) {
      view.dispatch({ effects: readOnlyC.reconfigure(EditorState.readOnly.of(ro)) });
    },

    /** setAttachments übergibt die Metadaten für die Datei-Chips. */
    setAttachments(list) {
      const map = {};
      for (const a of list || []) map[a.id] = { name: a.name, size: a.size };
      view.dispatch({ effects: setAttachmentsEffect.of(map) });
    },

    /** destroy gibt den Editor frei — für kurzlebige Instanzen wie die
     *  Anhang-Bearbeitung, damit nichts im Speicher hängen bleibt. */
    destroy() { view.destroy(); },

    get lineCount() { return view.state.doc.lines; },
  };
}
