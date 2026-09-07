/* SoapMacUi — Oberfläche.
 *
 * Speicherregel aus dem Entwurf: Bodies und Anhänge leben in Go. Hier liegt
 * immer nur der gerade sichtbare Request im Editor; alles andere sind IDs.
 */

const go = () => window.go?.app?.App;

const S = {
  projects: [],      // alle bekannten Projekte (ProjectRef)
  projectId: null,   // aktives Projekt
  project: null,     // ProjectView des aktiven Projekts
  endpointId: null,
  requestId: null,
  request: null,
  dirty: false,
  tabs: [],          // offene Request-Tabs (nur IDs, keine Bodies)
  results: new Map(), // tabId+endpointId -> SendResult (je Tab eigene Antwort)
};

const $ = (id) => document.getElementById(id);
const el = (tag, cls, txt) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (txt != null) n.textContent = txt;
  return n;
};

function status(msg, kind) {
  const n = $("statusMsg");
  n.textContent = msg;
  n.style.color = kind === "err" ? "var(--err)" : kind === "ok" ? "var(--ok)" : "";
}

/* ------------------------------------------------------------------ Dialog */

function confirmAction({ title, sub, okLabel = "OK" }) {
  return ask({ title, sub, noInput: true, okLabel }).then((r) => !!r);
}

function ask({ title, sub, value = "", placeholder = "", check = null, noInput = false, okLabel = "OK" }) {
  return new Promise((resolve) => {
    $("modalTitle").textContent = title;
    $("modalSub").textContent = sub || "";
    const input = $("modalInput");
    input.value = value;
    input.placeholder = placeholder;
    input.hidden = noInput;
    $("modalOk").textContent = okLabel;
    $("modalCheckWrap").hidden = !check;
    if (check) { $("modalCheck").checked = false; $("modalCheckWrap").querySelector("span").textContent = check; }
    $("modal").hidden = false;
    requestAnimationFrame(() => {
      if (noInput) { $("modalOk").focus(); return; }
      input.focus();
      input.select();
    });

    const done = (ok) => {
      $("modal").hidden = true;
      $("modalOk").onclick = $("modalCancel").onclick = null;
      input.onkeydown = null;
      resolve(ok ? { value: input.value.trim(), check: $("modalCheck").checked } : null);
    };
    $("modalOk").onclick = () => done(true);
    $("modalCancel").onclick = () => done(false);
    input.onkeydown = (e) => {
      if (e.key === "Enter") done(true);
      if (e.key === "Escape") done(false);
    };
  });
}

/* ------------------------------------------------------------------ Baum */

const expanded = new Set();

function renderTree() {
  const root = $("tree");
  root.innerHTML = "";
  if (!S.projects.length) {
    root.appendChild(emptyState());
    return;
  }

  // Alle bekannten Projekte zeigen, nicht nur das aktive. Ein Projekt, das
  // aus der Ansicht verschwindet, fühlt sich an wie ein verlorenes Projekt.
  for (const ref of S.projects) {
    const key = "p:" + ref.dir;
    const isActive = !!ref.id && ref.id === S.projectId;
    const pv = isActive ? S.project : null;

    const pn = el("div", "tree-node tree-project");
    const projRow = row({
      id: key,
      label: ref.name,
      twisty: true,
      active: isActive,
      title: ref.dir,
      onClick: () => selectProject(ref),
      onDblClick: () => toggleExpanded(key),
    });
    projRow.dataset.projectDir = ref.dir;
    projRow.dataset.projectName = ref.name;
    pn.appendChild(projRow);

    const kids = el("div", "tree-children");
    if (!pv) {
      const hint = el("div", "note");
      hint.style.padding = "6px 10px";
      hint.textContent = "Zum Öffnen anklicken.";
      kids.appendChild(hint);
    } else if (!pv.interfaces?.length) {
      const hint = el("div", "note");
      hint.style.padding = "6px 10px";
      hint.textContent = "Noch kein WSDL geladen.";
      kids.appendChild(hint);
    } else {
      for (const itf of pv.interfaces) {
        const inode = el("div", "tree-node");
        inode.appendChild(row({
          id: "i:" + itf.id, label: itf.name || "Interface", twisty: true,
          tag: String(itf.operations?.length || 0), title: itf.wsdlUrl,
        }));
        const ikids = el("div", "tree-children");

        for (const op of itf.operations || []) {
          const onode = el("div", "tree-node");
          const many = (op.requests?.length || 0) > 1;
          const opRowEl = row({
            id: "o:" + op.id,
            label: op.name,
            twisty: many,
            tag: op.retired ? "entfallen" : (op.suggestMtom ? "MTOM" : null),
            onClick: many ? null : () => openRequest(op.requests?.[0]?.id, op),
            active: !many && op.requests?.[0]?.id === S.requestId,
            title: op.documentation || `${op.service} / ${op.port}`,
          });
          if (op.requests?.[0]) opRowEl.dataset.requestId = op.requests[0].id;
          onode.appendChild(opRowEl);
          if (many) {
            const rkids = el("div", "tree-children");
            for (const r of op.requests) {
              rkids.appendChild(row({
                id: "r:" + r.id, label: r.name, twisty: false,
                onClick: () => openRequest(r.id, op), active: r.id === S.requestId,
              }));
            }
            onode.appendChild(wrapChildren("o:" + op.id, rkids));
          }
          ikids.appendChild(onode);
        }
        inode.appendChild(wrapChildren("i:" + itf.id, ikids));
        kids.appendChild(inode);
      }
    }
    pn.appendChild(wrapChildren(key, kids));
    root.appendChild(pn);
  }
}

// selectProject öffnet ein Projekt oder klappt das aktive auf und zu.
// Ungespeicherte Änderungen werden vorher gesichert.
async function selectProject(ref) {
  const key = "p:" + ref.dir;
  if (ref.id && ref.id === S.projectId) {
    // Bereits aktiv: nur sicherstellen, dass es offen ist. Ein Einfachklick
    // darf nie zuklappen — das ist der Sprung, der stört. Zuklappen macht
    // der Doppelklick oder das Dreieck.
    if (!expanded.has(key)) { expanded.add(key); renderTree(); }
    return;
  }
  if (S.dirty) await saveBody();
  try {
    const pv = await go().OpenProject(ref.dir);
    S.projectId = pv.id;
    S.project = pv;
    S.requestId = null;
    S.request = null;
    S.endpointId = null;
    editor.setDoc("");
    S.dirty = false;
    updateStats();
    markDirty();
    clearResult();
    setChip("reqAction", "");
    setChip("reqVersion", "");
    $("reqTitle").textContent = "Request";
    expanded.add(key);
    await refreshProject();
    status("Projekt geöffnet: " + pv.name);
  } catch (e) {
    status("Projekt öffnen: " + e, "err");
  }
}

function wrapChildren(key, node) {
  node.style.display = expanded.has(key) ? "" : "none";
  node.dataset.for = key;
  return node;
}

function toggleExpanded(key) {
  expanded.has(key) ? expanded.delete(key) : expanded.add(key);
  renderTree();
}

function row({ id, label, twisty, tag, onClick, onDblClick, active, title }) {
  const r = el("div", "tree-row" + (active ? " active" : ""));
  if (title) r.title = title;
  const tw = el("span", "tw" + (expanded.has(id) ? " open" : ""), twisty ? "▶" : "");
  r.appendChild(tw);
  r.appendChild(el("span", "lbl", label));
  if (tag) r.appendChild(el("span", "tag", tag));

  let timer = null;
  r.onclick = (e) => {
    // Das Dreieck klappt nur auf und zu, ohne die Auswahl zu verändern.
    if (twisty && (e.target === tw || !onClick)) {
      toggleExpanded(id);
      return;
    }
    if (!onClick) return;
    if (!onDblClick) { onClick(); return; }
    // Einfach- und Doppelklick auseinanderhalten: der Einfachklick wartet
    // kurz ab, damit ein folgender Doppelklick ihn noch abfangen kann.
    if (timer) return;
    timer = setTimeout(() => { timer = null; onClick(); }, 200);
  };
  if (onDblClick) {
    r.ondblclick = (e) => {
      if (e.target === tw) return;
      if (timer) { clearTimeout(timer); timer = null; }
      onDblClick();
    };
  }
  return r;
}

function emptyState() {
  const w = el("div", "empty");
  w.appendChild(el("p", "empty-title", "Noch kein Projekt"));
  w.appendChild(el("p", "empty-sub", "Lege ein Projekt an und lade ein WSDL, um loszulegen."));
  const b = el("button", "btn primary", "Projekt anlegen");
  b.onclick = newProject;
  w.appendChild(b);
  return w;
}

/* ------------------------------------------------------------------ Endpoints */

function renderEndpoints() {
  const bar = $("epTabs");
  bar.innerHTML = "";
  for (const ep of S.project?.endpoints || []) {
    const t = el("div", "ep-tab" + (ep.id === S.endpointId ? " active" : ""));
    t.appendChild(el("span", "dot"));
    t.appendChild(el("span", null, ep.name || "Endpoint"));
    t.appendChild(el("span", "url", shortUrl(ep.url)));
    t.title = ep.url;
    // Tabwechsel richtet den *aktuellen* Request um. Body, Header und
    // Anhänge bleiben stehen — kein Neuaufsetzen, kein Copy-Paste.
    t.onclick = () => {
      S.endpointId = ep.id;
      const at = activeTab();
      if (at) at.endpointId = ep.id;   // der Tab behält sein Ziel
      renderEndpoints();
      renderReqTabs();
      fillInspector();
      showResultFor();
    };
    bar.appendChild(t);
  }
}

function shortUrl(u) {
  try {
    const p = new URL(u);
    return p.host;
  } catch { return u || ""; }
}

function currentEndpoint() {
  return (S.project?.endpoints || []).find((e) => e.id === S.endpointId) || null;
}

/* ------------------------------------------------------------------ Editor */

// CodeMirror statt <textarea>: Farben, Klappen, Suchen und richtiges Undo.
// Die Fassade in editor.js verhält sich nach aussen wie ein Textfeld, deshalb
// bleibt der Rest dieser Datei unverändert. Byte-Genauigkeit gilt weiter —
// CodeMirror normalisiert das Dokument nicht.
const editor = SoapEditor.mount($("editorHost"), {
  onChange: () => { S.dirty = true; updateStats(); markDirty(); },
  // Klick auf einen Datei-Chip im Editor führt zur Anhang-Liste.
  onAttachmentRemove: (cid, from, to) => removeReferenceAt(cid, from, to),
  onAttachmentReplace: (cid, from, to) => replaceAttachmentAt(cid, from, to),
  onAttachmentClick: (cid, known) => {
    showReqView("attach");
    const a = (S.request?.attachments || []).find((x) => x.id === cid);
    status(known && a
      ? `${a.name} · ${fmtBytes(a.size)} · cid:${cid}`
      : `Kein Anhang mit cid:${cid} — Verweis zeigt ins Leere.`, known ? "" : "warn");
  },
});

function updateStats() {
  $("editorStats").textContent =
    `${editor.lineCount} Zeilen · ${new Blob([editor.value]).size} Bytes`;
}

function markDirty() {
  $("editorHint").textContent = S.dirty
    ? "ungespeicherte Änderung — ⌘S"
    : "byte-genau — was hier steht, geht so raus";
  $("editorHint").style.color = S.dirty ? "var(--warn)" : "";
  renderReqTabs();
}

async function openRequest(requestId, op, tabId = null) {
  if (!requestId) return;
  if (S.dirty) await saveBody();
  try {
    const rv = await go().GetRequest(S.projectId, requestId);
    S.requestId = requestId;
    S.request = rv;
    S.dirty = false;
    editor.setDoc(rv.body || "");
    updateStats();
    markDirty();
    $("reqTitle").textContent = rv.operation || rv.name;
    setChip("reqAction", rv.soapAction ? `SOAPAction: ${rv.soapAction || "\"\""}` : "");
    setChip("reqVersion", rv.soapVersion ? "SOAP " + rv.soapVersion : "");
    $("btnSend").disabled = !S.endpointId;
    if (tabId) { S.activeTab = tabId; renderReqTabs(); } else { addTab(rv, op); }
    if (rv.suggestMtom && $("mtomMode").value === "none") {
      status("Das WSDL deutet auf MTOM hin — Modus im Inspektor prüfen.");
    }
    renderAttachments();
    renderHeaders();
    renderTree();
    showResultFor();
  } catch (e) {
    status("Request laden: " + e, "err");
  }
}

function setChip(id, text) {
  const n = $(id);
  n.textContent = text;
  n.hidden = !text;
}

async function saveBody() {
  if (!S.requestId || !S.dirty) return;
  try {
    await go().SaveRequestBody(S.projectId, S.requestId, editor.value);
    S.dirty = false;
    markDirty();
    status("Gespeichert", "ok");
  } catch (e) {
    status("Speichern: " + e, "err");
  }
}

/* ------------------------------------------------------------------ Senden */

async function send() {
  if (!S.requestId || !S.endpointId) return;
  await saveBody();
  const btn = $("btnSend");
  btn.disabled = true;
  btn.textContent = "Sende …";
  status("Sende an " + (currentEndpoint()?.url || ""));
  const t0 = performance.now();
  try {
    const res = await go().Send(S.projectId, S.requestId, S.endpointId);
    S.results.set(resultKey(), res);
    showResult(res);
    const ms = (performance.now() - t0).toFixed(0);
    status(res.error ? "Fehlgeschlagen: " + res.error : `HTTP ${res.status} in ${ms} ms`, res.error ? "err" : "ok");
  } catch (e) {
    status("Senden: " + e, "err");
  } finally {
    btn.disabled = false;
    btn.textContent = "Senden ⌘↵";
  }
}

/* Der Schlüssel enthält den Tab, nicht den Request: zwei Tabs desselben
 * Requests sollen getrennte Antworten behalten. */
function resultKey() { return (S.activeTab || S.requestId) + "|" + S.endpointId; }

function showResultFor() {
  const r = S.results.get(resultKey());
  if (r) showResult(r); else clearResult();
}

function clearResult() {
  $("respPretty").innerHTML = "";
  $("respRawReq").textContent = "";
  $("respRawRes").textContent = "";
  $("respHeaders").innerHTML = "";
  $("respAttach").innerHTML = "";
  $("statusPill").hidden = true;
  $("timingHint").textContent = "";
  $("tlsHint").textContent = "";
  $("attBadge").hidden = true;
  renderFault(null);
}

// renderFault füllt einen eigenen Container. Vorher wurde die Box neben die
// Antwort gehängt und nie geräumt — sie stapelte sich bei jedem Senden und
// überlebte jeden Wechsel.
function renderFault(f) {
  const box = $("faultBox");
  box.innerHTML = "";
  box.hidden = !f;
  if (!f) return;
  const w = el("div", "fault");
  w.appendChild(el("div", "fhead", "SOAP-Fault" + (f.code ? " · " + f.code : "")));
  if (f.reason) w.appendChild(el("div", null, f.reason));
  if (f.actor) w.appendChild(el("div", "fdetail", "actor: " + f.actor));
  if (f.detail) w.appendChild(el("div", "fdetail", f.detail));
  box.appendChild(w);
}

function showResult(res) {
  const pill = $("statusPill");
  pill.hidden = false;
  if (res.error) {
    pill.textContent = "Fehler";
    pill.className = "status-pill err";
  } else {
    pill.textContent = String(res.status);
    pill.className = "status-pill " + (res.fault ? "warn" : res.ok ? "ok" : "err");
  }

  renderFault(res.fault);

  const pretty = $("respPretty");
  pretty.innerHTML = "";
  if (res.error) {
    const e = el("div", null, res.error);
    e.style.color = "var(--err)";
    e.style.fontFamily = "var(--mono)";
    pretty.appendChild(e);
  } else {
    renderXmlTree(pretty, res.envelope || "");
  }

  $("respRawReq").textContent = res.rawRequest || "(kein Mitschnitt)";
  $("respRawRes").textContent = (res.rawResponse || "(kein Mitschnitt)") +
    (res.rawTruncated ? "\n\n[Mitschnitt gekürzt — Grenze erreicht]" : "");

  const hs = $("respHeaders");
  hs.innerHTML = "";
  for (const [k, vs] of Object.entries(res.headers || {})) {
    for (const v of vs) {
      const kv = el("div", "kv");
      kv.appendChild(el("span", "k", k));
      kv.appendChild(el("span", "v", v));
      hs.appendChild(kv);
    }
  }

  const at = $("respAttach");
  at.innerHTML = "";
  const atts = res.attachments || [];
  $("attBadge").hidden = atts.length === 0;
  $("attBadge").textContent = String(atts.length);
  for (const a of atts) {
    at.appendChild(attachmentCard({
      title: a.name || a.id,
      meta: `${a.contentType || "?"} · ${fmtBytes(a.size)} · cid:${a.id}`,
      path: a.path,
      editable: false,          // empfangene Anhänge sind ein Beleg, kein Entwurf
      leadIcon: a.isXml ? "📄" : "📎",
    }));
  }
  if (!atts.length) at.appendChild(el("div", "att-empty", "Keine Anhänge in der Antwort."));

  const t = res.timing || {};
  $("timingHint").textContent =
    `DNS ${(t.dnsMs || 0).toFixed(1)} ms · Connect ${(t.connectMs || 0).toFixed(1)} ms · ` +
    `TLS ${(t.tlsMs || 0).toFixed(1)} ms · TTFB ${(t.ttfbMs || 0).toFixed(1)} ms · ` +
    `gesamt ${(t.totalMs || 0).toFixed(1)} ms · ${fmtBytes(res.size)}`;
  $("tlsHint").textContent = res.tls
    ? `${res.tls.version} · ${res.tls.cipherSuite} · ${res.tls.verified ? "geprüft" : "UNGEPRÜFT"}`
    : "";
  $("tlsHint").style.color = res.tls && !res.tls.verified ? "var(--warn)" : "";

  for (const w of res.warnings || []) status(w, "warn");
}

function fmtBytes(n) {
  if (!n) return "0 B";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MiB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KiB";
  return n + " B";
}

/* XML-Hervorhebung für die schreibgeschützte Antwortansicht.
 *
 * Ein Durchlauf mit Platzhaltern statt verketteter Ersetzungen: sonst greift
 * die Attribut-Regex in das class="..." der Spans, die der Tag-Schritt gerade
 * erzeugt hat, und zerlegt die eigene Ausgabe.
 */
function highlightXML(src) {
  const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const T0 = "\x01", T1 = "\x02", A0 = "\x03", A1 = "\x04",
        V0 = "\x05", V1 = "\x06", C0 = "\x0e", C1 = "\x0f";

  const token = /<!--[\s\S]*?-->|<[?!/]?[^<>]*>/g;
  let out = "", last = 0, m;
  while ((m = token.exec(src)) !== null) {
    out += esc(src.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("<!--")) {
      out += C0 + esc(tok) + C1;
    } else {
      out += esc(tok)
        .replace(/^(&lt;[?!/]*)([\w.:-]+)/, (_, b, n) => b + T0 + n + T1)
        .replace(/([\w.:-]+)=("[^"]*")/g, (_, a, v) => A0 + a + A1 + "=" + V0 + v + V1);
    }
    last = token.lastIndex;
  }
  out += esc(src.slice(last));

  return out
    .split(T0).join('<span class="t">').split(T1).join("</span>")
    .split(A0).join('<span class="a">').split(A1).join("</span>")
    .split(V0).join('<span class="v">').split(V1).join("</span>")
    .split(C0).join('<span class="c">').split(C1).join("</span>");
}

/* ------------------------------------------------------------------ Inspektor */

function fillInspector() {
  const ep = currentEndpoint();
  if (!ep) return;
  $("epName").value = ep.name || "";
  $("epUrl").value = ep.url || "";

  const a = ep.auth || {};
  $("authKind").value = a.kind || "none";
  $("authUser").value = a.username || "";
  $("authPwType").value = a.passwordType || "PasswordText";
  $("authNonce").checked = !!a.addNonce;
  $("authCreated").checked = !!a.addCreated;
  $("authTimestamp").checked = !!a.addTimestamp;
  $("authMU").checked = !!a.mustUnderstand;
  $("secretNote").textContent = a.secretRef
    ? "Passwort liegt im Schlüsselbund."
    : "Noch kein Passwort hinterlegt.";
  toggleAuth();

  const w = ep.wire || {};
  $("wireBody").value = w.body || "exact";
  $("wireAction").value = w.soapAction || "quoted";
  $("wireEncoding").value = w.encoding || "UTF-8";
  $("wireLine").value = w.lineEnding || "keep";
  $("wireDecl").checked = !!w.xmlDeclaration;
  $("wireBom").checked = !!w.bom;
  $("wireCharset").checked = !!w.charsetInContentType;
  $("wireGzip").checked = !!w.acceptGzip;
  $("wireChunked").checked = !!w.chunked;
  $("wireExpect").checked = !!w.expect100Continue;

  const m = ep.mtom || {};
  $("mtomMode").value = m.mode || "none";
  $("mtomTE").value = m.transferEncoding || "binary";
  $("mtomStartInfo").checked = !!m.startInfo;

  const h = ep.http || {};
  $("httpInsecure").checked = !!h.insecureSkipVerify;
  $("httpRedirects").checked = !!h.followRedirects;
  $("httpTimeout").value = Math.round((h.timeoutNanos || 60000000000) / 1e9) || 60;
  $("httpProxy").value = h.proxy || "";
}

function toggleAuth() {
  const k = $("authKind").value;
  $("authDetails").hidden = k === "none";
  $("wssOnly").hidden = k !== "wss";
}

async function applyInspector() {
  const ep = currentEndpoint();
  if (!ep) return;
  const updated = structuredClone(ep);
  updated.name = $("epName").value;
  updated.url = $("epUrl").value;

  updated.auth = {
    ...updated.auth,
    kind: $("authKind").value,
    username: $("authUser").value,
    passwordType: $("authPwType").value,
    addNonce: $("authNonce").checked,
    addCreated: $("authCreated").checked,
    addTimestamp: $("authTimestamp").checked,
    mustUnderstand: $("authMU").checked,
  };
  updated.wire = {
    ...updated.wire,
    body: $("wireBody").value,
    soapAction: $("wireAction").value,
    encoding: $("wireEncoding").value,
    lineEnding: $("wireLine").value,
    xmlDeclaration: $("wireDecl").checked,
    bom: $("wireBom").checked,
    charsetInContentType: $("wireCharset").checked,
    acceptGzip: $("wireGzip").checked,
    chunked: $("wireChunked").checked,
    expect100Continue: $("wireExpect").checked,
  };
  updated.mtom = {
    ...updated.mtom,
    mode: $("mtomMode").value,
    transferEncoding: $("mtomTE").value,
    startInfo: $("mtomStartInfo").checked,
  };
  updated.http = {
    ...updated.http,
    insecureSkipVerify: $("httpInsecure").checked,
    followRedirects: $("httpRedirects").checked,
    timeoutNanos: Math.max(1, parseInt($("httpTimeout").value || "60", 10)) * 1e9,
    proxy: $("httpProxy").value,
  };

  try {
    await go().UpdateEndpoint(S.projectId, updated);
    await refreshProject();
    const pw = $("authPass").value;
    if (pw) {
      await go().SetSecret(S.projectId, updated.id, pw);
      $("authPass").value = "";
      await refreshProject();
      status("Passwort im Schlüsselbund abgelegt", "ok");
    }
  } catch (e) {
    status("Endpoint speichern: " + e, "err");
  }
}

function renderAttachments() {
  const box = $("reqAttList");
  box.innerHTML = "";
  const list = S.request?.attachments || [];
  const badge = $("reqAttBadge");
  badge.hidden = list.length === 0;
  badge.textContent = String(list.length);

  editor.setAttachments(list);
  if (!list.length) {
    box.appendChild(el("div", "att-empty", "Keine Anhänge an diesem Request."));
    return;
  }
  for (const a of list) {
    // Ein Anhang ohne Verweis im Body wird bei MTOM nicht mitgesendet —
    // das muss man sehen, nicht erraten.
    const referenced = editor.value.includes("cid:" + a.id);
    const extra = [];

    if (referenced) {
      const find = el("button", "btn small", "zeigen");
      find.title = "Verweis im Body markieren";
      find.onclick = () => { showReqView("body"); locateAttachment(a); };
      extra.push(find);
    } else {
      const ins = el("button", "btn small", "einsetzen");
      ins.title = "xop:Include an der Cursorposition einsetzen";
      ins.onclick = () => insertReference(a);
      extra.push(ins);
    }
    const del = el("button", "btn small", "×");
    del.title = "Anhang entfernen (Datei bleibt im Projektordner)";
    del.onclick = async () => {
      await go().RemoveAttachment(S.projectId, S.requestId, a.id);
      S.request = await go().GetRequest(S.projectId, S.requestId);
      renderAttachments();
    };
    extra.push(del);

    box.appendChild(attachmentCard({
      title: a.name,
      meta: `${fmtBytes(a.size)} · cid:${a.id}` + (referenced ? "" : " · kein Verweis im Body"),
      path: a.path,
      editable: true,
      leadIcon: a.name?.toLowerCase().endsWith(".xml") ? "📄" : "📎",
      warn: !referenced,
      extra,
    }));
  }
  renderOrphans(box);
}

/* Anhänge zu entfernen löscht nur den Eintrag, nicht die Datei — sonst wäre
 * ein Fehlklick unwiederbringlich. Ohne diese Liste bliebe sie unsichtbar. */
async function renderOrphans(box) {
  let files = [];
  try { files = await go().OrphanAttachments(S.projectId); } catch { return; }
  if (!files?.length) return;

  const head = el("div", "hint");
  head.style.marginTop = "10px";
  head.textContent = `Dateien im Projektordner ohne Verweis (${files.length}):`;
  box.appendChild(head);

  for (const f of files) {
    const row = el("div", "att-row orphan");
    row.appendChild(el("span", "ico", "🗂"));
    const info = el("div", "info");
    info.appendChild(el("div", "fname", f.name));
    info.appendChild(el("div", "fmeta", `${fmtBytes(f.size)} · gehört zu keinem Request`));
    row.appendChild(info);
    const acts = el("div", "acts");
    const add = el("button", "btn small", "anhängen");
    add.onclick = async () => {
      try {
        await go().AttachExisting(S.projectId, S.requestId, f.path);
        S.request = await go().GetRequest(S.projectId, S.requestId);
        renderAttachments();
        status(`${f.name} wieder angehängt`, "ok");
      } catch (e) { status("Anhängen: " + e, "err"); }
    };
    acts.appendChild(add);
    row.appendChild(acts);
    box.appendChild(row);
  }
}


/* ✕ am Chip: den xop:Include-Verweis aus dem Body nehmen. Ist es der letzte
 * Verweis auf diesen Anhang, wandert auch der Eintrag raus — die Datei bleibt
 * im Projektordner und taucht unter den verwaisten Dateien wieder auf. */
async function removeReferenceAt(cid, from, to) {
  editor.setRangeText("", from, to);
  await saveBody();
  await dropIfUnreferenced(cid, "Verweis entfernt");
}

/* ⇄ am Chip: neue Datei wählen und den Verweis an Ort und Stelle austauschen. */
async function replaceAttachmentAt(cid, from, to) {
  try {
    const neu = await go().AddAttachment(S.projectId, S.requestId);
    if (!neu) return;   // Dialog abgebrochen
    editor.setRangeText(`<xop:Include href="cid:${neu.id}"/>`, from, to);
    await saveBody();
    await dropIfUnreferenced(cid, `ersetzt durch ${neu.name}`);
  } catch (e) {
    status("Ersetzen: " + e, "err");
  }
}

/* Ein Anhang ohne Verweis würde ohnehin nicht mitgesendet — dann soll er auch
 * nicht in der Liste stehen bleiben. Gelöscht wird nur der Eintrag. */
async function dropIfUnreferenced(cid, what) {
  try {
    if (!editor.value.includes("cid:" + cid)) {
      await go().RemoveAttachment(S.projectId, S.requestId, cid);
    }
    S.request = await go().GetRequest(S.projectId, S.requestId);
    renderAttachments();
    status(what, "ok");
  } catch (e) {
    status("Anhang aktualisieren: " + e, "err");
  }
}


/* ------------------------------------------------------------------ Anhang-Karte
 *
 * Eine Darstellung für beide Seiten: Anhänge des Requests und der Antwort.
 * Vorschau liest höchstens 64 KiB, Bearbeiten öffnet einen eigenen kleinen
 * Editor, Rechtsklick bietet die installierten Textprogramme an.
 */

let editorsCache = null;

async function knownEditors() {
  if (!editorsCache) {
    try { editorsCache = await go().ListEditors(); } catch { editorsCache = [{ name: "Standardprogramm", app: "" }]; }
  }
  return editorsCache;
}

function attachmentCard({ title, meta, path, editable, leadIcon, extra = [], warn }) {
  const cardEl = el("div", "att-card");
  const row = el("div", "att" + (warn ? " att-warn" : ""));

  const left = el("div", "info");
  left.appendChild(el("div", "name", title));
  left.appendChild(el("div", "meta", meta));
  if (leadIcon) row.appendChild(el("span", "ico", leadIcon));
  row.appendChild(left);

  const right = el("div", "right");
  for (const b of extra) right.appendChild(b);

  const pane = el("div", "att-preview");
  pane.hidden = true;
  let liveEditor = null;

  const closePane = () => {
    if (liveEditor) { liveEditor.destroy(); liveEditor = null; }
    pane.innerHTML = "";
    pane.hidden = true;
    prev.textContent = "Vorschau";
    edit.textContent = "Bearbeiten";
  };

  const prev = el("button", "btn small", "Vorschau");
  prev.onclick = async () => {
    if (!pane.hidden && !liveEditor) { closePane(); return; }
    closePane();
    prev.disabled = true;
    try {
      const p = await go().PreviewAttachment(path, 64 * 1024);
      const pre = el("pre", "code");
      if (p.isXml && !p.binary) pre.innerHTML = highlightXML(p.text);
      else pre.textContent = p.text;
      pane.appendChild(pre);
      if (p.truncated) pane.appendChild(el("div", "hint", `… gekürzt, insgesamt ${fmtBytes(p.size)}`));
      pane.hidden = false;
      prev.textContent = "Vorschau ausblenden";
    } catch (e) {
      status("Vorschau: " + e, "err");
    } finally { prev.disabled = false; }
  };
  right.appendChild(prev);

  const edit = el("button", "btn small", "Bearbeiten");
  edit.onclick = async () => {
    if (liveEditor) { closePane(); return; }
    closePane();
    edit.disabled = true;
    try {
      const p = await go().PreviewAttachment(path, 1024 * 1024);
      if (p.binary) { status("Binärdatei — nicht bearbeitbar.", "warn"); return; }
      if (p.truncated) { status("Datei zu gross zum Bearbeiten — extern öffnen.", "warn"); return; }

      const host = el("div", "att-edit-host");
      pane.appendChild(host);
      const bar = el("div", "att-edit-bar");
      const save = el("button", "btn primary small", "Speichern");
      const cancel = el("button", "btn small", "Verwerfen");
      bar.appendChild(save); bar.appendChild(cancel);
      pane.appendChild(bar);
      pane.hidden = false;

      liveEditor = SoapEditor.mount(host, {});
      liveEditor.setDoc(p.text);
      edit.textContent = "Bearbeitung schliessen";

      cancel.onclick = closePane;
      save.onclick = async () => {
        try {
          await go().WriteAttachment(S.projectId, path, liveEditor.value);
          status("Anhang gespeichert", "ok");
          closePane();
          if (S.requestId) {
            S.request = await go().GetRequest(S.projectId, S.requestId);
            renderAttachments();
          }
        } catch (e) { status("Speichern: " + e, "err"); }
      };
    } catch (e) {
      status("Bearbeiten: " + e, "err");
    } finally { edit.disabled = false; }
  };
  if (editable) right.appendChild(edit);

  const rev = el("button", "btn small", "⤴");
  rev.title = "Im Finder zeigen";
  rev.onclick = () => go().RevealPath(path).catch((e) => status("Finder: " + e, "err"));
  right.appendChild(rev);

  row.appendChild(right);
  cardEl.appendChild(row);
  cardEl.appendChild(pane);

  // Rechtsklick auf die Karte bietet die installierten Textprogramme an.
  cardEl.addEventListener("contextmenu", async (e) => {
    e.preventDefault();
    e.stopPropagation();
    const eds = await knownEditors();
    showCtx(e.clientX, e.clientY, [
      { label: "Vorschau", run: () => prev.click() },
      ...(editable ? [{ label: "Bearbeiten", run: () => edit.click() }] : []),
      "-",
      { label: "Öffnen mit", sub: eds.map((ed) => ({
          label: ed.name,
          run: () => go().OpenWith(path, ed.app).catch((x) => status("Öffnen: " + x, "err")),
        })) },
      { label: "Im Finder zeigen", run: () => rev.click() },
    ]);
  });
  return cardEl;
}

/* ------------------------------------------------------------------ Kopfzeilen */

function renderHeaders() {
  const box = $("reqHdrList");
  box.innerHTML = "";
  const list = S.request?.headers || [];
  const badge = $("reqHdrBadge");
  const on = list.filter((h) => h.enabled).length;
  badge.hidden = on === 0;
  badge.textContent = String(on);

  if (!list.length) {
    box.appendChild(el("div", "att-empty", "Keine zusätzlichen Kopfzeilen."));
    return;
  }
  list.forEach((h, i) => {
    const row = el("div", "hdr-row" + (h.enabled ? "" : " off"));
    const on = document.createElement("input");
    on.type = "checkbox";
    on.checked = !!h.enabled;
    on.onchange = () => { list[i].enabled = on.checked; saveHeaders(); };
    row.appendChild(on);

    const name = document.createElement("input");
    name.type = "text";
    name.className = "hname";
    name.value = h.name;
    name.placeholder = "Name";
    name.onchange = () => { list[i].name = name.value; saveHeaders(); };
    row.appendChild(name);

    const val = document.createElement("input");
    val.type = "text";
    val.className = "hval";
    val.value = h.value;
    val.placeholder = "Wert";
    val.onchange = () => { list[i].value = val.value; saveHeaders(); };
    row.appendChild(val);

    const del = el("button", "btn small", "×");
    del.onclick = () => { list.splice(i, 1); saveHeaders(); };
    row.appendChild(del);
    box.appendChild(row);
  });
}

async function saveHeaders() {
  try {
    await go().SaveRequestHeaders(S.projectId, S.requestId, S.request.headers || []);
    renderHeaders();
  } catch (e) {
    status("Kopfzeilen speichern: " + e, "err");
  }
}

function showReqView(name) {
  for (const t of document.querySelectorAll("#reqViewTabs .tab")) {
    t.classList.toggle("active", t.dataset.rv === name);
  }
  for (const v of document.querySelectorAll(".req-view")) {
    v.classList.toggle("active", v.id === "rview-" + name);
  }
}

function locateAttachment(a) {
  const i = editor.value.indexOf("cid:" + a.id);
  if (i < 0) { status("Kein Verweis im Body.", "warn"); return; }
  const start = editor.value.lastIndexOf("<", i);
  const end = editor.value.indexOf(">", i) + 1;
  editor.focus();
  editor.setSelectionRange(start < 0 ? i : start, end > 0 ? end : i);
}

async function insertReference(a) {
  try {
    const res = await go().InsertReference(
      S.projectId, S.requestId, editor.value, editor.selectionStart, editor.selectionEnd, a.id);
    editor.setDoc(res.body);
    S.dirty = false;
    updateStats();
    markDirty();
    editor.focus();
    editor.setSelectionRange(res.selStart, res.selEnd);
    renderAttachments();
    if (res.mtomChanged) await refreshProject();
    status("Verweis eingesetzt", "ok");
  } catch (e) {
    status("Verweis einsetzen: " + e, "err");
  }
}

/* attachHere erledigt in einem Klick, was in SoapUI vier Schritte sind:
 * Datei wählen, Content-ID erzeugen, Verweis an die Cursorstelle setzen,
 * MTOM einschalten. Der Editorinhalt geht mit, damit ungespeicherte
 * Änderungen erhalten bleiben. */
async function attachHere() {
  if (!S.requestId) { status("Erst eine Operation auswählen.", "err"); return; }
  const a = editor.selectionStart, b = editor.selectionEnd;
  try {
    const res = await go().AttachAndInsert(
      S.projectId, S.requestId, S.endpointId || "", editor.value, a, b);
    if (!res || res.cancelled) return;

    editor.setDoc(res.body);
    S.dirty = false;              // das Backend hat bereits gespeichert
    updateStats();
    markDirty();
    editor.focus();
    editor.setSelectionRange(res.selStart, res.selEnd);

    S.request = await go().GetRequest(S.projectId, S.requestId);
    renderAttachments();

    let msg = res.reused
      ? `${res.attachment.name} hängt bereits dran — nur ein weiterer Verweis eingefügt`
      : `${res.attachment.name} angehängt, Verweis eingefügt`;
    if (res.mtomChanged) {
      msg += " — MTOM eingeschaltet";
      await refreshProject();
    }
    status(msg, "ok");
  } catch (e) {
    status("Anhang: " + e, "err");
  }
}

/* ------------------------------------------------------------------ XML-Baum */

const escHtml = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/* renderXmlTree baut aus der Antwort einen klappbaren Baum.
 * Der Baum ist eine *Ansicht* — die exakten Bytes stehen weiterhin unter
 * "Raw Response". Lässt sich das Dokument nicht parsen (Fault-Fragment,
 * abgeschnittene Antwort), fällt die Ansicht auf gefärbten Fliesstext zurück. */
function renderXmlTree(container, xml) {
  container.innerHTML = "";
  if (!xml.trim()) return;

  let doc = null;
  try { doc = new DOMParser().parseFromString(xml, "application/xml"); } catch { doc = null; }
  if (!doc || doc.querySelector("parsererror") || !doc.documentElement) {
    const pre = el("pre", "code");
    pre.innerHTML = highlightXML(xml);
    container.appendChild(pre);
    return;
  }
  container.appendChild(xmlNode(doc.documentElement));
}

function tagMarkup(node, selfClose) {
  let out = `<span class="xtag">&lt;${escHtml(node.nodeName)}</span>`;
  for (const a of node.attributes) {
    out += ` <span class="xattr">${escHtml(a.name)}</span>` +
           `<span class="xtag">=</span>` +
           `<span class="xval">"${escHtml(a.value)}"</span>`;
  }
  return out + `<span class="xtag">${selfClose ? "/&gt;" : "&gt;"}</span>`;
}

function closeMarkup(node) {
  return `<span class="xtag">&lt;/${escHtml(node.nodeName)}&gt;</span>`;
}

function xmlNode(node) {
  const wrap = el("div", "xnode");
  const kids = [...node.childNodes].filter(
    (n) => n.nodeType === 1 || n.nodeType === 8 || (n.nodeType === 3 && n.nodeValue.trim() !== ""));
  const hasElements = kids.some((n) => n.nodeType !== 3);

  const line = el("div", "xline");
  const tw = el("span", "xtw" + (hasElements ? "" : " leaf"), hasElements ? "▾" : "");
  line.appendChild(tw);

  const open = el("span");
  line.appendChild(open);

  if (kids.length === 0) {
    open.innerHTML = tagMarkup(node, true);
    wrap.appendChild(line);
    return wrap;
  }

  open.innerHTML = tagMarkup(node, false);

  if (!hasElements) {
    // Nur Text: alles in eine Zeile, das liest sich deutlich ruhiger.
    line.appendChild(el("span", "xtext", kids.map((n) => n.nodeValue).join("").trim()));
    const c = el("span");
    c.innerHTML = closeMarkup(node);
    line.appendChild(c);
    wrap.appendChild(line);
    return wrap;
  }

  line.appendChild(el("span", "xellipsis", " … "));
  wrap.appendChild(line);

  const box = el("div", "xkids");
  for (const k of kids) {
    if (k.nodeType === 1) {
      box.appendChild(xmlNode(k));
    } else {
      const l = el("div", "xline");
      l.appendChild(el("span", "xtw leaf", ""));
      l.appendChild(k.nodeType === 8
        ? el("span", "xcomment", `<!--${k.nodeValue}-->`)
        : el("span", "xtext", k.nodeValue.trim()));
      box.appendChild(l);
    }
  }
  wrap.appendChild(box);

  const closeLine = el("div", "xline xclose");
  closeLine.appendChild(el("span", "xtw leaf", ""));
  const c = el("span");
  c.innerHTML = closeMarkup(node);
  closeLine.appendChild(c);
  wrap.appendChild(closeLine);

  tw.onclick = () => {
    wrap.classList.toggle("collapsed");
    tw.classList.toggle("closed");
  };
  return wrap;
}

function foldAll(collapsed) {
  for (const n of document.querySelectorAll("#respPretty .xnode")) {
    const tw = n.querySelector(":scope > .xline > .xtw");
    if (!tw || tw.classList.contains("leaf")) continue;
    n.classList.toggle("collapsed", collapsed);
    tw.classList.toggle("closed", collapsed);
  }
}

/* ------------------------------------------------------------------ Request-Tabs */

let tabSeq = 0;

/* Jeder Tab hat eine eigene Kennung und merkt sich seinen Endpoint. Damit
 * lässt sich derselbe Request zweimal öffnen und gegen zwei Systeme fahren,
 * ohne zwischen den Endpoints hin und her zu schalten. */
function addTab(rv, op, force = false) {
  if (!force) {
    const existing = S.tabs.find((t) => t.requestId === rv.id && t.projectId === S.projectId);
    if (existing) { S.activeTab = existing.tabId; renderReqTabs(); return; }
  }
  const tab = {
    tabId: "t" + (++tabSeq),
    requestId: rv.id,
    name: op?.name || rv.operation || rv.name,
    projectId: S.projectId,
    endpointId: S.endpointId,
  };
  S.tabs.push(tab);
  S.activeTab = tab.tabId;
  renderReqTabs();
}

function activeTab() { return S.tabs.find((t) => t.tabId === S.activeTab) || null; }

function renderReqTabs() {
  const bar = $("reqTabs");
  bar.innerHTML = "";
  const mine = S.tabs.filter((t) => t.projectId === S.projectId);
  const dupes = new Set();
  const seen = new Set();
  for (const t of mine) {
    if (seen.has(t.requestId)) dupes.add(t.requestId);
    seen.add(t.requestId);
  }

  for (const t of mine) {
    const active = t.tabId === S.activeTab;
    const n = el("div", "req-tab" + (active ? " active" : ""));
    n.dataset.tab = t.tabId;
    if (active && S.dirty) n.appendChild(el("span", "mod"));
    n.appendChild(el("span", null, t.name));
    // Bei mehrfach geöffnetem Request den Endpoint dazuschreiben, sonst
    // sind die Tabs nicht unterscheidbar.
    if (dupes.has(t.requestId)) {
      const ep = (S.project?.endpoints || []).find((e) => e.id === t.endpointId);
      n.appendChild(el("span", "tabep", ep?.name || "—"));
    }
    const x = el("span", "x", "×");
    x.onclick = (e) => { e.stopPropagation(); closeTab(t.tabId); };
    n.appendChild(x);
    n.onclick = () => { if (!active) switchTab(t.tabId); };
    bar.appendChild(n);
  }
}

async function switchTab(tabId) {
  const t = S.tabs.find((x) => x.tabId === tabId);
  if (!t) return;
  if (S.dirty) await saveBody();
  S.activeTab = tabId;
  if (t.endpointId && S.project?.endpoints?.some((e) => e.id === t.endpointId)) {
    S.endpointId = t.endpointId;
    renderEndpoints();
    fillInspector();
  }
  await openRequest(t.requestId, { name: t.name }, tabId);
}

async function closeTab(tabId) {
  const i = S.tabs.findIndex((t) => t.tabId === tabId);
  if (i < 0) return;
  const wasActive = S.tabs[i].tabId === S.activeTab;
  if (wasActive && S.dirty) await saveBody();
  S.tabs.splice(i, 1);
  if (!wasActive) { renderReqTabs(); return; }

  const mine = S.tabs.filter((t) => t.projectId === S.projectId);
  const next = mine[Math.max(0, Math.min(i - 1, mine.length - 1))];
  if (next) { switchTab(next.tabId); return; }

  S.activeTab = null;
  S.requestId = null;
  S.request = null;
  editor.setDoc("");
  S.dirty = false;
  updateStats();
  markDirty();
  clearResult();
  $("reqTitle").textContent = "Request";
  setChip("reqAction", "");
  setChip("reqVersion", "");
  $("btnSend").disabled = true;
  renderTree();
}

/* ------------------------------------------------------------------ Kontextmenü */

function hideCtx() {
  $("ctxMenu").hidden = true;
  const sub = document.getElementById("ctxSub");
  if (sub) sub.remove();
}

function buildItems(target, items) {
  for (const it of items) {
    if (it === "-") { target.appendChild(el("div", "sep")); continue; }
    const b = el("button", null, it.label);
    if (it.sub) {
      b.appendChild(el("span", "kbd", "▸"));
      b.onmouseenter = () => openSub(b, it.sub);
      b.onclick = (e) => { e.stopPropagation(); openSub(b, it.sub); };
    } else {
      if (it.kbd) b.appendChild(el("span", "kbd", it.kbd));
      b.onmouseenter = () => { const s = document.getElementById("ctxSub"); if (s) s.remove(); };
      b.onclick = () => { hideCtx(); it.run?.(); };
    }
    b.disabled = !!it.disabled;
    target.appendChild(b);
  }
}

function openSub(anchor, items) {
  const old = document.getElementById("ctxSub");
  if (old) old.remove();
  if (!items.length) return;
  const m = el("div", "ctx");
  m.id = "ctxSub";
  buildItems(m, items);
  document.body.appendChild(m);
  const r = anchor.getBoundingClientRect();
  const box = m.getBoundingClientRect();
  m.style.left = Math.min(r.right - 4, window.innerWidth - box.width - 8) + "px";
  m.style.top = Math.min(r.top - 5, window.innerHeight - box.height - 8) + "px";
}

function showCtx(x, y, items) {
  const m = $("ctxMenu");
  m.innerHTML = "";
  buildItems(m, items);
  m.hidden = false;
  // Am Rand einklappen statt aus dem Fenster laufen.
  const r = m.getBoundingClientRect();
  m.style.left = Math.min(x, window.innerWidth - r.width - 8) + "px";
  m.style.top = Math.min(y, window.innerHeight - r.height - 8) + "px";
}

/* Untermenü mit allen offenen Requests — zum Springen, und es zeigt
 * gleichzeitig, welcher Request mehrfach offen ist. */
function openRequestsSub() {
  const mine = S.tabs.filter((t) => t.projectId === S.projectId);
  if (!mine.length) return [{ label: "keine offenen Requests", disabled: true }];
  return mine.map((t) => {
    const ep = (S.project?.endpoints || []).find((e) => e.id === t.endpointId);
    return {
      label: (t.tabId === S.activeTab ? "● " : "   ") + t.name + (ep ? ` — ${ep.name}` : ""),
      run: () => switchTab(t.tabId),
    };
  });
}

async function clipCopy(text) {
  try { await navigator.clipboard.writeText(text); status("Kopiert", "ok"); }
  catch { status("Kopieren nicht möglich — ⌘C benutzen.", "warn"); }
}

function ctxItems(e) {
  const inEditor = e.target.closest(".editor-stack");
  const inResp = e.target.closest(".resp-body");
  const opRow = e.target.closest(".tree-row");
  const reqTab = e.target.closest(".req-tab");
  const epTab = e.target.closest(".ep-tab");

  if (reqTab) {
    const id = reqTab.dataset.tab;
    const t = S.tabs.find((x) => x.tabId === id);
    return [
      { label: "Offene Requests", sub: openRequestsSub() },
      "-",
      { label: "Nochmal in neuem Tab öffnen", run: async () => {
          if (!t) return;
          const rv = await go().GetRequest(S.projectId, t.requestId);
          addTab(rv, { name: t.name }, true);
          switchTab(S.activeTab);
        } },
      { label: "Tab schliessen", kbd: "⌘W", run: () => t && closeTab(t.tabId) },
      { label: "Andere Tabs schliessen", run: () => {
          for (const o of [...S.tabs]) if (o.tabId !== id) closeTab(o.tabId);
        } },
    ];
  }
  if (epTab) {
    return [{ label: "Endpoint hinzufügen …", run: addEndpoint }];
  }
  if (inEditor) {
    const sel = editor.value.slice(editor.selectionStart, editor.selectionEnd);
    return [
      { label: "Kopieren", kbd: "⌘C", disabled: !sel, run: () => clipCopy(sel) },
      { label: "Ausschneiden", kbd: "⌘X", disabled: !sel, run: () => {
          clipCopy(sel);
          const a = editor.selectionStart, b = editor.selectionEnd;
          editor.setRangeText("", a, b, "end");
          S.dirty = true; updateStats(); markDirty();
        } },
      { label: "Einfügen", kbd: "⌘V", run: async () => {
          try {
            const t = await navigator.clipboard.readText();
            editor.setRangeText(t, editor.selectionStart, editor.selectionEnd, "end");
            S.dirty = true; updateStats(); markDirty();
          } catch { status("Einfügen nicht möglich — ⌘V benutzen.", "warn"); }
        } },
      { label: "Alles auswählen", kbd: "⌘A", run: () => editor.select() },
      { label: "Suchen …", kbd: "⌘F", run: () => editor.openSearch() },
      "-",
      { label: "Alle Elemente zuklappen", run: () => editor.foldAll() },
      { label: "Alle Elemente aufklappen", run: () => editor.unfoldAll() },
      "-",
      { label: "Datei anhängen & Verweis hier einsetzen", run: attachHere },
      { label: "Formatieren", run: () => $("btnFormat").click() },
      "-",
      { label: "Senden", kbd: "⌘↵", disabled: $("btnSend").disabled, run: send },
      "-",
      { label: "Offene Requests", sub: openRequestsSub() },
    ];
  }
  if (inResp) {
    return [
      { label: "Auswahl kopieren", run: () => clipCopy(String(window.getSelection())) },
      { label: "Ganze Antwort kopieren", run: () => {
          const r = S.results.get(resultKey());
          clipCopy(r?.pretty || r?.envelope || "");
        } },
      "-",
      { label: "Alle zuklappen", run: () => foldAll(true) },
      { label: "Alle aufklappen", run: () => foldAll(false) },
    ];
  }
  if (opRow && opRow.dataset.projectDir) {
    const dir = opRow.dataset.projectDir;
    const name = opRow.dataset.projectName;
    return [
      { label: "Öffnen", run: () => opRow.click() },
      { label: "Im Finder zeigen", run: () => go().RevealPath(dir).catch((e) => status("Finder: " + e, "err")) },
      "-",
      { label: "WSDL neu indizieren …", disabled: S.projectId !== (S.projects.find((p) => p.dir === dir)?.id), run: loadWsdl },
      "-",
      { label: "Aus der Liste entfernen", run: () => removeProject(dir, name, false) },
      { label: "In den Papierkorb legen …", run: () => removeProject(dir, name, true) },
    ];
  }
  if (opRow) {
    const lbl = opRow.querySelector(".lbl")?.textContent;
    const rid = opRow.dataset.requestId;
    return [
      { label: `„${lbl}" öffnen`, disabled: !rid, run: () => opRow.click() },
      { label: "In neuem Tab öffnen", disabled: !rid, run: async () => {
          const rv = await go().GetRequest(S.projectId, rid);
          addTab(rv, { name: lbl }, true);
          switchTab(S.activeTab);
        } },
      "-",
      { label: "Offene Requests", sub: openRequestsSub() },
      "-",
      { label: "WSDL neu indizieren …", run: loadWsdl },
    ];
  }
  return [
    { label: "Offene Requests", sub: openRequestsSub() },
    "-",
    { label: "Neues Projekt …", run: newProject },
    { label: "WSDL laden …", disabled: !S.projectId, run: loadWsdl },
    "-",
    { label: "Endpoint hinzufügen …", disabled: !S.projectId, run: addEndpoint },
  ];
}

/* ------------------------------------------------------------------ Aktionen */

async function refreshProject() {
  S.projects = await go().ListProjects();
  if (!S.projectId) { renderTree(); return; }
  S.project = await go().GetProject(S.projectId);
  if (!S.endpointId && S.project.endpoints?.length) S.endpointId = S.project.endpoints[0].id;
  $("tbProject").textContent = S.project.name;
  $("btnLoadWsdl").disabled = false;
  $("btnSend").disabled = !S.requestId || !S.endpointId;
  renderTree();
  renderEndpoints();
  fillInspector();
}

/* Entfernen heisst standardmässig: aus der Liste nehmen, Dateien bleiben.
 * Der Papierkorb-Weg wird bestätigt und löscht auch dann nicht endgültig —
 * ein Projekt enthält Requests, Anhänge und den WSDL-Cache. */
async function removeProject(dir, name, toTrash) {
  if (toTrash) {
    const ok = await confirmAction({
      title: `„${name}" in den Papierkorb?`,
      sub: `Der Ordner ${dir} wandert in den Papierkorb — samt Requests, Anhängen und WSDL-Cache. Von dort lässt er sich zurückholen.`,
      okLabel: "In den Papierkorb",
    });
    if (!ok) return;
  }
  try {
    await go().RemoveProject(dir, toTrash);
    if (S.projects.find((p) => p.dir === dir)?.id === S.projectId) {
      S.projectId = null;
      S.project = null;
      S.requestId = null;
      S.request = null;
      S.activeTab = null;
      S.tabs = S.tabs.filter((t) => t.projectId !== S.projectId);
      editor.setDoc("");
      S.dirty = false;
      updateStats();
      markDirty();
      clearResult();
      renderReqTabs();
      $("reqTitle").textContent = "Request";
      $("tbProject").textContent = "Kein Projekt geöffnet";
      $("btnSend").disabled = true;
    }
    await refreshProject();
    status(toTrash ? `„${name}" liegt im Papierkorb` : `„${name}" aus der Liste entfernt`, "ok");
  } catch (e) {
    status("Projekt entfernen: " + e, "err");
  }
}

async function newProject() {
  const r = await ask({ title: "Neues Projekt", sub: "Der Ordner wird unter Application Support angelegt.", placeholder: "z. B. Backoffice-Schnittstelle" });
  if (!r?.value) return;
  // Erst sichern, was im Editor steht — sonst geht es beim Wechsel verloren.
  if (S.dirty) await saveBody();
  try {
    const pv = await go().CreateProject(r.value);
    S.projectId = pv.id;
    S.project = pv;
    S.requestId = null;
    S.request = null;
    S.endpointId = null;
    editor.setDoc("");
    S.dirty = false;
    updateStats();
    markDirty();
    clearResult();
    $("reqTitle").textContent = "Request";
    expanded.add("p:" + pv.dir);
    await refreshProject();
    status("Projekt angelegt: " + pv.dir, "ok");
    loadWsdl();
  } catch (e) {
    status("Projekt anlegen: " + e, "err");
  }
}

async function loadWsdl() {
  if (!S.projectId) return;
  // Beim erneuten Laden die bekannte Adresse vorbelegen — das ist der
  // Reindex-Fall, und niemand tippt die URL zweimal.
  const known = S.project?.interfaces?.[0]?.wsdlUrl || "";
  const r = await ask({
    title: known ? "WSDL neu indizieren" : "WSDL laden",
    sub: known
      ? "Das WSDL wird neu geladen und gegen den gespeicherten Stand verglichen. Bestehende Requests bleiben unangetastet."
      : "Endpoints, Services und Operationen werden automatisch erkannt.",
    value: known,
    placeholder: "https://host/service.php?wsdl",
    check: "Serverzertifikat nicht prüfen",
  });
  if (!r?.value) return;
  status("Lade WSDL …");
  try {
    const res = await go().LoadWSDL(S.projectId, r.value, r.check);
    S.project = res.project;
    for (const i of res.project.interfaces || []) expanded.add("i:" + i.id);
    await refreshProject();
    let msg = `${res.interface}: ${res.operationCount} Operationen, ${res.endpoints.length} Endpoint(s)`;
    if (res.newOperations?.length) msg += ` · ${res.newOperations.length} neu`;
    if (res.removedOperations?.length) msg += ` · ${res.removedOperations.length} entfallen`;
    if (res.changedOperations?.length) msg += ` · ${res.changedOperations.length} geändert`;
    status(msg, "ok");
  } catch (e) {
    status("WSDL laden: " + e, "err");
  }
}

async function addEndpoint() {
  if (!S.projectId) return;
  const r = await ask({ title: "Endpoint hinzufügen", sub: "Ein weiteres Zielsystem für dieselben Requests.", placeholder: "https://host/service.php" });
  if (!r?.value) return;
  try {
    let name = r.value;
    try { name = new URL(r.value).hostname.split(".")[0]; } catch {}
    S.project = await go().AddEndpoint(S.projectId, name, r.value);
    await refreshProject();
  } catch (e) {
    status("Endpoint anlegen: " + e, "err");
  }
}

/* ------------------------------------------------------------------ Start */

function wire() {
  $("btnNewProject").onclick = newProject;
  $("btnFirstProject") && ($("btnFirstProject").onclick = newProject);
  $("btnLoadWsdl").onclick = loadWsdl;
  $("btnAddEndpoint").onclick = addEndpoint;
  $("btnSend").onclick = send;
  $("btnFormat").onclick = async () => {
    try {
      editor.value = await go().FormatXML(editor.value);
      S.dirty = true; updateStats(); markDirty();
    } catch (e) { status("Formatieren: " + e, "err"); }
  };
  $("btnInspector").onclick = () => $("inspector").classList.toggle("collapsed");

  // Doppelklick auf den Titelbalken zoomt — Standardverhalten auf dem Mac.
  // Wails startet den Zieh-Vorgang nur bei detail===1, ein Doppelklick
  // kollidiert damit also nicht.
  document.querySelector(".titlebar").addEventListener("dblclick", (e) => {
    if (e.target.closest(".btn")) return;
    window.runtime?.WindowToggleMaximise?.();
  });
  $("btnAddAttach").onclick = attachHere;
  $("btnAttach").onclick = attachHere;
  $("btnAddHeader").onclick = () => {
    if (!S.request) return;
    (S.request.headers ||= []).push({ name: "", value: "", enabled: true });
    saveHeaders();
  };
  for (const t of document.querySelectorAll("#reqViewTabs .tab")) {
    t.onclick = () => showReqView(t.dataset.rv);
  }
  $("btnEdFold").onclick = () => { showReqView("body"); editor.foldAll(); };
  $("btnEdUnfold").onclick = () => { showReqView("body"); editor.unfoldAll(); };

  $("authKind").onchange = () => { toggleAuth(); applyInspector(); };
  for (const id of ["epName", "epUrl", "authUser", "authPwType", "authNonce", "authCreated",
    "authTimestamp", "authMU", "wireBody", "wireAction", "wireEncoding", "wireLine",
    "wireDecl", "wireBom", "wireCharset", "wireGzip", "wireChunked", "wireExpect",
    "mtomMode", "mtomTE", "mtomStartInfo", "httpInsecure", "httpRedirects",
    "httpTimeout", "httpProxy"]) {
    const n = $(id);
    if (!n) continue;
    n.addEventListener("change", applyInspector);
  }
  $("authPass").addEventListener("change", applyInspector);

  $("btnFoldAll").onclick = () => foldAll(true);
  $("btnUnfoldAll").onclick = () => foldAll(false);

  // Eigenes Kontextmenü: das native ist im Produktivbuild auf
  // WKWebView-Ebene abgeschaltet (Wails: debug || EnableDefaultContextMenu).
  document.addEventListener("contextmenu", (e) => {
    e.preventDefault();
    showCtx(e.clientX, e.clientY, ctxItems(e));
  });
  document.addEventListener("click", (e) => { if (!e.target.closest(".ctx")) hideCtx(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") hideCtx(); });
  window.addEventListener("blur", hideCtx);

  for (const t of document.querySelectorAll("#respTabs .tab")) {
    t.onclick = () => {
      document.querySelectorAll("#respTabs .tab").forEach((x) => x.classList.remove("active"));
      document.querySelectorAll(".resp-view").forEach((x) => x.classList.remove("active"));
      t.classList.add("active");
      $("view-" + t.dataset.tab).classList.add("active");
    };
  }

  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); send(); }
    if ((e.metaKey || e.ctrlKey) && e.key === "s") { e.preventDefault(); saveBody(); }
    if ((e.metaKey || e.ctrlKey) && e.key === "w" && S.activeTab) { e.preventDefault(); closeTab(S.activeTab); }
    if ((e.metaKey || e.ctrlKey) && e.altKey && e.key.toLowerCase() === "i") {
      e.preventDefault(); $("inspector").classList.toggle("collapsed");
    }
  });

  // Senkrechter Teiler zwischen Request und Antwort
  let dragging = false;
  $("splitter").addEventListener("mousedown", () => { dragging = true; document.body.style.cursor = "row-resize"; });
  window.addEventListener("mouseup", () => { dragging = false; document.body.style.cursor = ""; });
  window.addEventListener("mousemove", (e) => {
    if (!dragging) return;
    const split = document.querySelector(".split");
    const r = split.getBoundingClientRect();
    const frac = Math.min(0.85, Math.max(0.15, (e.clientY - r.top) / r.height));
    document.querySelector(".request-pane").style.flex = `1 1 ${frac * 100}%`;
    document.querySelector(".response-pane").style.flex = `1 1 ${(1 - frac) * 100}%`;
  });
}

async function boot() {
  wire();
  updateStats();
  // Auf die Wails-Laufzeit warten.
  for (let i = 0; i < 100 && !go(); i++) await new Promise((r) => setTimeout(r, 50));
  if (!go()) { status("Laufzeit nicht verfügbar", "err"); return; }

  try {
    $("statusStore").textContent = await go().SecretStoreName();
    S.projects = await go().ListProjects();
    renderTree();
    if (S.projects.length) await selectProject(S.projects[0]);
  } catch (e) {
    status("Start: " + e, "err");
  }
}

document.addEventListener("DOMContentLoaded", boot);
