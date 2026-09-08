# WSDL-Begriffe und ihre Entsprechung im Projektmodell

Diese Seite hält fest, was WSDL 1.1 definiert und wie SoapMacUi es abbildet.
Der Grund für die Seite: die Begriffe klingen ähnlich, decken sich aber nicht.
Insbesondere hat `port` bei uns **keine** Entsprechung, und ein `Endpoint` im
Projekt ist etwas anderes als eine `soap:address` im WSDL.

## Der Aufbau eines WSDL

```
definitions
 ├── types            (XSD — Elemente und Typen)
 ├── message          (Nachrichten, bestehen aus parts)
 ├── portType         (abstrakte Schnittstelle: die operations)
 ├── binding          (bindet EIN portType an ein Protokoll: SOAP 1.1/1.2, Style, Use)
 └── service
      └── port        (bindet EIN binding an EINE Adresse)
           └── soap:address location="https://…"
```

Gelesen wird das von unten nach oben: ein `port` sagt „dieses `binding` ist
unter dieser Adresse erreichbar", ein `binding` sagt „dieses `portType` spreche
ich als SOAP 1.1 im Stil document/literal", und das `portType` sagt, welche
Operationen es überhaupt gibt.

## Kardinalitäten

| Beziehung | Anzahl | Anmerkung |
|---|---|---|
| `definitions` → `service` | 1:n | mehrere Services in einem WSDL sind zulässig |
| `service` → `port` | 1:n | hier entstehen mehrere Adressen |
| `port` → `binding` | n:1 | **mehrere Ports dürfen dasselbe Binding referenzieren** |
| `port` → `soap:address` | 1:1 | genau eine Adresse je Port |
| `binding` → `portType` | n:1 | **mehrere Bindings dürfen dasselbe portType referenzieren** |
| `portType` → `operation` | 1:n | |
| `operation` → `message` | 1:1 je Richtung | plus n Faults |
| `message` → `part` | 1:n | |

### Kann Binding A in Port A **und** Port B stecken?

Ja. `port/@binding` ist eine Referenz, keine Enthaltensbeziehung — dasselbe
Binding darf von beliebig vielen Ports referenziert werden. Der übliche Fall
ist dieselbe Schnittstelle unter zwei Adressen, etwa eine gespiegelte oder
lastverteilte Bereitstellung:

```xml
<service name="BookstoreService">
  <port name="BookstorePort"   binding="tns:BookstoreSoapBinding">
    <soap:address location="https://books.example.org/soap/v1"/>
  </port>
  <port name="BookstorePortAlt" binding="tns:BookstoreSoapBinding">
    <soap:address location="https://books-2.example.org/soap/v1"/>
  </port>
</service>
```

Der umgekehrte Fall ist genauso häufig: **zwei Bindings auf demselben
portType**, eines für SOAP 1.1 und eines für SOAP 1.2, jedes in einem eigenen
Port mit eigener Adresse. Dieselben Operationen, zwei Protokolle.

### Hängt die Adresse an der Operation?

Nein. Die Adresse steht am Port, nicht an der Operation. Alle Operationen eines
`portType` teilen sich die Adresse ihres Ports; unterschieden werden sie über
`soapAction` und das Wurzelelement im Body, nicht über die URL.

Die einzige Ausnahme betrifft das reine HTTP-Binding (`http:binding`, kein
SOAP): dort hängt `http:operation/@location` einen relativen Pfad an die
Port-Adresse. Für SOAP-Bindings gibt es das nicht, und SoapMacUi überspringt
Nicht-SOAP-Bindings ohnehin (`wsdl/view.go:12`).

## Die Abbildung im Projektmodell

| WSDL | SoapMacUi | Wo | Anmerkung |
|---|---|---|---|
| `definitions` | `Interface` | `project/model.go:82` | eine Schnittstelle je geladener WSDL-URL |
| `@targetNamespace` | `Interface.TargetNS` | | |
| — | `Interface.CacheFiles` | | die abgelegten Quelldokumente |
| `service/@name` | `Operation.Service` | `project/model.go:99` | nur als Zeichenkette, kein eigener Typ |
| `port/@name` | `Operation.Port` | `project/model.go:100` | **kein eigener Typ** — siehe unten |
| `binding/@name` | `Operation.Binding` | `project/model.go:101` | dazu `Style`, `Use`, `Version` |
| `portType` | — | | löst sich in die Operationsliste auf |
| `operation` | `Operation` | `project/model.go:95` | eine je `service × port × operation` |
| `soapAction` | `Operation.SOAPAction` | | |
| `soap:address/@location` | `Operation.WSDLEndpoint` | `project/model.go:111` | die Adresse **des eigenen Ports** |
| `message` / `part` | — | | verbraucht der Request-Generator (`soap.Builder` + `xsd`) |
| `types` (XSD) | — | | dito; das Ergebnis ist der erzeugte Body |
| — | `Endpoint` | `project/model.go:48` | **nur bei uns**: das gewählte Ziel |
| — | `Request` | `project/model.go:127` | **nur bei uns**: ein gespeicherter Aufruf |

### Der fehlende Gegenpart zu `port`

`Definitions.Operations()` (`wsdl/view.go:7`) läuft `service → port →
portType.operations` und erzeugt daraus eine **flache Liste**. Service, Port und
Binding überleben nur als Zeichenketten an der Operation — als Herkunftsangabe,
nicht als Struktur.

Praktische Folge: ein WSDL mit zwei SOAP-Ports über demselben portType erzeugt
**jede Operation doppelt**, einmal je Port, mit unterschiedlichem
`Operation.Port` und `Operation.WSDLEndpoint`. Der Schlüssel beim Reindex ist
entsprechend das Tripel `service/port/operation` (`opKey` in
`project/store.go`).

### `Endpoint` ist kein `port`

Ein `Endpoint` im Projekt ist das **vom Benutzer gewählte Ziel** samt allem, was
den Aufruf dorthin bestimmt: Auth, TLS, Wire-Optionen, MTOM, Kopfzeilen,
Variablen. Beim Laden eines WSDL werden die gefundenen Port-Adressen als
Endpoints vorbelegt (`project/store.go`, „Endpoints aus dem WSDL ergänzen"),
danach sind sie frei editierbar und überleben jeden Reindex.

Deshalb darf ein Projekt Endpoints haben, die in keinem WSDL vorkommen — der
Normalfall, sobald man dieselbe Schnittstelle gegen ein zweites System testet.

> **Offene Kante:** gehört eine Operation zu Port A, ist es ein Fehler, sie an
> die Adresse von Port B zu schicken. Die Information liegt vor
> (`Operation.Port`, `Operation.WSDLEndpoint`), gewarnt wird bisher nicht.

## Warum das nicht dasselbe wie „Environments" ist

| | beschreibt | steht wo |
|---|---|---|
| **WSDL-Ports** | auf welchen Wegen derselbe Vertrag angeboten wird — Protokoll, Bindung, Adresse | im Vertrag selbst |
| **Environments** | derselbe Vertrag, mehrfach ausgerollt (Test, Staging, Produktion) | nirgends im WSDL |

Ein WSDL kennt keine Umgebungen. Prod und Staging haben in aller Regel je ein
eigenes WSDL mit je eigener Adresse — und häufig genug unterscheiden sich die
beiden Dokumente auch inhaltlich, weil auf dem Testsystem eine neuere Fassung
läuft. Genau deshalb gibt es in SoapMacUi **keine** Umgebungsebene: mehrere
Endpoints im Projekt reichen, und wo die Verträge auseinanderlaufen, gehören
sie ohnehin in getrennte Schnittstellen oder Projekte.
