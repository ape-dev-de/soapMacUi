# SoapMacUi — Implementierungsplan

Ersatz für SoapUI zum manuellen Testen von SOAP-Schnittstellen.
Stand: 2026-09-07

## 0. Entscheidungen

| Thema | Entscheidung | Begründung |
|---|---|---|
| Sprache | **Go 1.24** | nativ kompiliert, ein Binary, gut lesbar, starke stdlib für XML/HTTP/TLS/MIME |
| UI | **Wails v2** (stabil; v3 ist noch Alpha) | UI läuft im **System-WKWebView** — wird *nicht* mitgeliefert. Fenster ist ein echtes `NSVisualEffectView` → System-Blur statt CSS-Fake |
| Frontend | Svelte 5 + Vite + CodeMirror 6 | kompiliert weg, Bundle ~400 KB, wird ins Binary eingebettet |
| Nicht QML | keine gepflegte Go↔QML-Bindung (`go-qml` tot, `therecipe/qt` unmaintained); Qt wären +60–80 MB im Bundle |
| Nicht Swift | schönste native Optik (macOS 26 Liquid Glass), aber macOS-only. Portabilität schlägt hier Optik |
| Persistenz | eigenes JSON-Format, **kein SoapUI-Import** | stattdessen "Reindex" über die WSDL-URL |
| Secrets | OS-Keychain, nie in der Projektdatei | macOS Keychain jetzt, Windows Credential Manager später — hinter einem Interface |

## 1. Architektur

    soapmacui/
    ├── cmd/soapmacui/main.go     Wails-Bootstrap, Fenster, Menüs
    ├── internal/
    │   ├── project/   Projektmodell, JSON-Persistenz, OS-Pfade
    │   ├── wsdl/      WSDL 1.1 Parser (SOAP 1.1 + 1.2 Bindings)
    │   ├── xsd/       XSD-Modell + Sample-Instance-Generator   <- grösster Brocken
    │   ├── soap/      Envelope-Bau, MTOM/XOP, Wire-Optionen
    │   ├── httpx/     Transport: TLS, Proxy, Redirects, Timing
    │   ├── auth/      Basic, WS-Security UsernameToken, Custom Header
    │   ├── secrets/   Keychain-Abstraktion
    │   ├── script/    goja JS-Engine, Pre/Post-Hooks, Sandbox
    │   └── app/       Wails-Bridge (die an JS exportierte API)
    ├── frontend/      Svelte-UI
    └── build/         Icons, Info.plist, Entitlements, Makefile

Harte Regel: `internal/` kennt Wails nicht. Der komplette Kern ist ohne UI testbar und
per CLI-Harness gegen echte WSDLs fahrbar. Das hält einen späteren UI-Wechsel offen.

## 2. Feature-Blöcke

### 2.1 WSDL / XSD — Sample-Request-Generierung
Der eigentliche Grund, warum man SoapUI benutzt. Risikoreichster Teil, deshalb zuerst.

- WSDL laden über HTTP(S) inkl. Auth (manche WSDLs sind geschützt), `wsdl:import`,
  `xsd:import` / `xsd:include` rekursiv auflösen, relative URLs korrekt, Fetch-Cache.
- Modell: Services → Ports → Binding → Operations, mit SOAPAction, style (document/rpc),
  use (literal/encoded), `soap:body` parts, `soap:header`.
- XSD: `complexType` (sequence/all/choice), `simpleType`-Restrictions (Enumerations →
  erster Wert), `extension`/`restriction` auf complexContent, Attribute, `minOccurs`/
  `maxOccurs` (Arrays → n Beispiele), `nillable`, Substitution Groups (best effort),
  `any`/`anyType` → Kommentar-Platzhalter, **Rekursionserkennung mit Tiefenlimit**.
- Generator baut den Envelope mit korrekten Namespace-Präfixen und `?`-Platzhaltern,
  wie SoapUI. Optionale Elemente wahlweise auskommentiert.
- **Endpoint-Detection**: `soap:address location` aller Ports → Endpoint-Liste automatisch.

Validierung: läuft gegen echte WSDLs aus dem Zielsystem (nicht Teil dieses
Repos, siehe `testdata-local/`) sowie gegen die Beispieldienste in `testdata/`.
Abnahmekriterium P1: generierte Requests sind gegen echte Endpoints sendefähig.

### 2.2 Wire-Exaktheit — First-Class-Feature
Dein konsumierender Server ist pingelig. Deshalb ist "was genau geht über die Leitung"
kein Detail, sondern eigenes Panel. Pro Request und pro Endpoint überschreibbar:

- Body: **byte-exakt so senden wie getippt** (Default) / pretty-print / Whitespace zwischen
  Elementen strippen. Kein automatisches Reformatieren beim Speichern oder Senden.
- XML-Deklaration an/aus, Encoding (UTF-8 / ISO-8859-1), BOM an/aus
- Zeilenenden LF / CRLF
- `SOAPAction`: quoted / unquoted / weggelassen (SOAP 1.1) bzw. `action=`-Parameter im
  Content-Type (SOAP 1.2)
- charset-Parameter im Content-Type vorhanden/abwesend, exakte Schreibweise
- Header-Reihenfolge + freie Roh-Header
- `Expect: 100-continue` an/aus, chunked vs. `Content-Length`, gzip an/aus
- leere Tags self-closing vs. offen/geschlossen

Dazu eine **Raw-Wire-Ansicht**: exakt die gesendeten und empfangenen Bytes (Text/Hex
umschaltbar), inkl. TLS-Handshake-Infos und Timings. Bei einem pingeligen Server ist das
der einzige Weg zu debuggen — und der grösste Vorteil gegenüber SoapUI.

### 2.3 MTOM / Attachments
- **Ausgehend**: `xop:Include href="cid:..."` im Body, Aufbau von
  `multipart/related; type="application/xop+xml"; start=...; start-info=...; boundary=...`.
  Konfigurierbar: Content-ID-Format, Content-Transfer-Encoding (binary/8bit/base64),
  Boundary-String, Reihenfolge der Part-Header.
- **Eingehend**: multipart/related erkennen, Parts zerlegen, Attachments anzeigen/speichern,
  XML-Attachments inline pretty-printed.
- Alternativmodi, weil Server sich unterscheiden: klassisches SwA (SOAP with Attachments)
  und "inline base64, kein MTOM" — umschaltbar pro Request.
- Upload per Dateidialog und Drag & Drop; XML-Anhänge werden angezeigt und auf
  Wohlgeformtheit geprüft.

### 2.4 Projekte, Endpoints, Tabs
- Speicherort über `os.UserConfigDir()`:
  macOS `~/Library/Application Support/SoapMacUi/`, Windows `%AppData%\SoapMacUi\`.
  Ein Ordner pro Projekt, WSDL/XSD-Kopien daneben → offline arbeitsfähig.
- Modell: Projekt → Interfaces → Operations → Requests, plus Endpoints (Base-URLs) und
  Environments (Dev/Test/Prod) mit `${variable}`-Substitution in URL, Header und Body.
- **Endpoint-Tabs** (deine Nachforderung): Tab-Leiste über dem Request-Editor, ein Tab pro
  Endpoint/Environment. Tabwechsel richtet den *aktuellen* Request auf den anderen Endpoint
  um — Body, Header, Attachments und Auth bleiben stehen. Kein Neuaufsetzen, kein Copy-Paste.
- Zusatz, praktisch beim Vergleich Test/RHEL8/Prod: "An alle Endpoints senden" +
  **Response-Diff** zwischen den Tabs.
- Zusätzlich Request-Tabs (mehrere offene Requests wie im Browser).

### 2.5 Reindex über WSDL-URL (statt Import)
- WSDL neu holen, neu parsen, gegen den gespeicherten Stand diffen:
  neue Operationen, entfallene Operationen, geänderte Message-Schemas.
- Diff-Dialog vor dem Übernehmen. **Bestehende Requests werden nie still überschrieben** —
  bei Schema-Änderung wird der Request markiert und ein frisches Sample *daneben* angeboten.
- Alte WSDL-Fassung bleibt im Projektordner, damit man sieht, was sich geändert hat.

### 2.6 Auth & Secrets
- HTTP Basic Auth.
- WS-Security UsernameToken: PasswordText und PasswordDigest (Nonce + Created),
  `mustUnderstand` schaltbar, Timestamp optional, Header-Reihenfolge fixierbar
  (pingelige Server prüfen das).
- Freie Custom-Header mit `${var}` — dein aktueller Weg.
- Secrets: nie in der Projekt-JSON. Interface `secrets.Store`, Backend macOS Keychain,
  Windows Credential Manager später. Die JSON hält nur einen Referenzschlüssel.
  Zusätzlich Modus "nur diese Sitzung" (nur im RAM), für geteilte Rechner.

### 2.7 Script-Hooks (Phase 5, aber von Anfang an eingeplant)
- **goja** — ES5.1+ JS-Engine in reinem Go, kein cgo, keine externe Abhängigkeit.
- `beforeRequest(ctx)`: URL, Header, Body, Attachments, Variablen manipulieren.
- `afterResponse(ctx)`: Asserts, Werte per XPath extrahieren und in Variablen legen
  (→ Request-Verkettung).
- Ebenen: global (Projekt), pro Endpoint, pro Request.
- Sandbox: standardmässig kein Datei- und Netzzugriff; Helfer (`http.get`, `file.read`)
  nur nach explizitem Opt-in — damit eine fremde Projektdatei nichts anrichtet.

### 2.8 Optik
- Fenster transparent + `NSVisualEffectView` (`WindowIsTranslucent`, Titelleiste
  hidden-inset mit Traffic-Lights) → echter System-Blur unter der UI.
- Layout wie Xcode/Mail: links Sidebar (Projekte → Interfaces → Operations → Requests),
  Mitte Split (Request-Editor / Response), rechts Inspector (Auth, Header, Wire-Optionen,
  Attachments, Scripts). Endpoint-Tabs über dem Editor.
- Glas-Panels via `backdrop-filter: blur()` mit feinen Rändern *über* der Vibrancy-Schicht,
  Systemschrift SF Pro, System-Akzentfarbe, Hell/Dunkel automatisch.
- XML-Editor: CodeMirror 6, Folding, Syntax-Check — **ohne Auto-Format**.

### 2.9 Speicher-Architektur (harte Regel)
Wails liefert **keine** Browser-Engine mit (kein Electron): auf macOS System-WKWebView,
auf Windows die geteilte WebView2-Runtime. Es gibt **einen** Renderer-Prozess — 20 Endpoint-
Tabs sind DOM-Elemente, keine 20 Webviews.

Der reale Speicherfresser sind die Nutzdaten, nicht die Tabs. Deshalb verbindlich:

- Request-/Response-Bodies und Attachments leben in **Go**, nie als JS-Strings.
  Das Frontend bekommt IDs und Metadaten.
- Nur der **sichtbare** Tab materialisiert ein CodeMirror-Dokument. Inaktive Tabs sind
  reiner Zustand, kein Editor-Objekt.
- Responses > 1 MB gehen direkt in den Projektordner und werden gefenstert angezeigt
  (virtualisiert), nicht am Stück in den Editor geladen.
- Attachments erreichen JS nie — Datei-Handles bleiben in Go.
- History speichert Metadaten + Dateiverweise, keine Bodies im RAM.

**Budget: < 250 MB RSS bei 20 offenen Endpoint-Tabs und einem geladenen 5-MB-Response.**

### 2.10 Persistenz — Dateien *und* SQLite, getrennt nach Workload

Vier verschiedene Zugriffsmuster, deshalb kein einheitlicher Speicher:

| Daten | Speicher | Warum |
|---|---|---|
| Projektdefinition | JSON-Dateien im Projektordner | diffbar, mergebar, git-fähig, per Repo teilbar |
| Request-Body | **eigene .xml-Datei** | byte-exakt, extern editierbar, `xxd`-bar |
| Scripts | echte `.js`-Dateien | in VS Code editierbar, git-diffbar |
| WSDL/XSD-Cache | Dateien | sind Dateien |
| History | **SQLite** + Bodies als Dateien | viele Zeilen, Filterung, Pruning |
| App-Optionen | eine `settings.json` in App-Data | klein, global |

**Layout**

    ~/Library/Application Support/SoapMacUi/     (Windows: %AppData%\SoapMacUi)
    ├── settings.json          App-Optionen
    ├── workspace.json         Liste bekannter Projektpfade, offene Tabs
    ├── history.db             SQLite (WAL): Metadaten, Timings, Status, Verweise
    └── blobs/ab/cd/<sha256>   Bodies + Attachments, content-addressed, dedupliziert

    <frei wählbar, z.B. im Projekt-Repo>/MeinDienst.soapmac/
    ├── project.json           schemaVersion, Endpoints, Environments, Variablen
    ├── interfaces/dienst.json      geparster WSDL-Stand (Basis für Reindex-Diff)
    ├── requests/
    │   ├── createAntrag.json       Optionen, Header, Auth-Ref, MTOM-Modus
    │   └── createAntrag.xml        der Body — als eigene Datei
    ├── scripts/{before,after}.js
    ├── attachments/
    └── wsdl-cache/

**Warum der Body eine eigene Datei ist**
Direkt aus der Wire-Exaktheits-Anforderung (2.2): in JSON eingebettet wird der Body
escaped und auf UTF-8 normalisiert — BOM, CRLF und trailing Whitespace überleben den
Round-Trip nicht nachprüfbar. Als eigene Datei bleiben die Bytes exakt erhalten, sind
in git diffbar, extern editierbar und mit `xxd` kontrollierbar.

**Warum Projekte nicht in SQLite**
Eine SQLite-Datei ist ein Blob: nicht diffbar, nicht mergebar, im PR nicht reviewbar.
Genau das ist das Elend mit SoapUIs Einzel-XML — zwei Leute editieren,
und der Merge ist ein Konflikt. Mit einer Datei pro Request mergen zwei Kollegen sauber.

**Warum History nicht in JSON**
Entweder eine riesige Datei (lädt komplett in den RAM → verletzt Budget 2.9) oder
zehntausende Kleinstdateien (langsame Verzeichnis-Scans, umständliches Pruning).
SQLite gibt Filterung nach Operation/Endpoint/Status/Zeitraum, günstiges Append und
eine Retention-Policy (N Tage / max. GB) praktisch geschenkt. Bodies bleiben *ausserhalb*
der DB als content-addressed Blobs — 500 identische Requests kosten einen Body.

**Treiber:** `modernc.org/sqlite` — reines Go, **kein cgo**. Hält den Windows-Cross-Build
und das Ein-Binary-Versprechen intakt (`mattn/go-sqlite3` bräuchte cgo).

**Der SoapUI-Fehler, den wir nicht machen**
Request-Optionen werden **vollständig und explizit** geschrieben, auch wenn sie dem
aktuellen Default entsprechen. Kein "weglassen weil Default" — sonst verhält sich ein
Projekt nach einem Update anders als vorher. Zusammen mit `schemaVersion` und
Migrationen heisst das: ein Projekt von vor sechs Monaten sendet exakt dieselben Bytes.
MTOM-Modus, Whitespace-Handling, SOAPAction-Quoting und Encoding sind Teil des
Request-Dokuments, nicht der App-Einstellungen.

**Nebenläufigkeit:** SQLite im WAL-Modus verträgt zwei App-Instanzen. Projektdateien
werden per Filesystem-Watcher überwacht; externe Änderungen (git pull, VS Code) laden
nach, bei Konflikt mit ungespeicherten Änderungen kommt ein Hinweis statt stillem
Überschreiben.

## 3. Build & Auslieferung
- `wails build -platform darwin/universal` → `.app`, signiert mit der vorhandenen
  "Apple Development"-Identity (kein Gatekeeper-Meckern auf deinen Rechnern).
  Notarisierung optional später.
- Ein selbst enthaltenes Binary, keine Runtime-Abhängigkeiten (WKWebView kommt vom OS).
- Makefile: `make dev`, `make build`, `make build-win`, `make test`.
- Erwartete Grösse: ~15–25 MB.

## 4. Phasen

| Phase | Inhalt | Abnahme |
|---|---|---|
| **P0** Fundament | Repo, Go-Module, Wails-Skeleton, Fenster + Vibrancy, Projektmodell + Pfade, Sidebar, **RAM-Spike** | App startet, sieht nach Mac aus, legt Projekte an; **gemessenes RSS mit 20 Tabs + 5-MB-Body unter Budget** |
| **P1** WSDL-Kern | WSDL/XSD-Parser, Sample-Generator, Endpoint-Detection, CLI-Testharness | Gegen echte WSDLs korrekte Requests |
| **P2** Senden | Transport, Wire-Optionen, Raw-Wire-Ansicht, Basic + WSS, Keychain | Erfolgreicher Call gegen echten Endpoint |
| **P3** MTOM | MTOM out/in, SwA, inline-base64, Attachment-Handling | MTOM-Call mit XML-Anhang läuft durch |
| **P4** Tabs | Endpoint-Tabs, Environments, Variablen, Send-to-all + Diff, Reindex | Endpoint-Wechsel ohne Copy-Paste |
| **P5** Scripts | goja, before/after Hooks, Sandbox, Variablen-Extraktion | Verketteter Request-Ablauf |
| **P6** Politur | Shortcuts, History, Export/Import eigenes Format, Signing | Tagesgeschäft-tauglich |

## 5. Risiken
1. **XSD-Generator** ist der einzige echte Unsicherheitsfaktor (Substitution Groups,
   rekursive Typen, `xsd:any`). Mitigation: P1 zuerst, direkt gegen die drei echten WSDLs.
2. **MTOM-Kompatibilität** — Server implementieren XOP unterschiedlich. Mitigation: alle
   Varianten schaltbar + Raw-Wire-Ansicht zum Vergleichen.
3. **Speicherverbrauch** bei vielen Tabs. Mitigation: Messung als P0-Abnahmekriterium
   (siehe 2.9). Reissleine: reisst der Spike das Budget, wird vor P1 auf Swift+SwiftUI
   (macOS-only) oder Go+Gio umgeschwenkt — dann ist nur P0 verloren, nicht der Kern.
4. **Wails v2 auf macOS 26** — Vibrancy-API könnte sich verändert haben. Mitigation:
   in P0 sofort prüfen; Fallback wäre reines CSS-Glas.
