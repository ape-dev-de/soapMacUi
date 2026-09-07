# SoapMacUi
#
# SIGN_ID       Signaturidentität. Für den eigenen Rechner reicht eine
#               "Apple Development"-Identität, für die Weitergabe an andere
#               braucht es "Developer ID Application".
#               Verfügbare Identitäten: security find-identity -v -p codesigning
# NOTARY_PROFILE  Name des mit `xcrun notarytool store-credentials` angelegten
#               Schlüsselbund-Profils.
#
# Beispiel: make sign SIGN_ID="Developer ID Application: Deine Firma (TEAMID)"

SIGN_ID        ?=
NOTARY_PROFILE ?= notary
APP      := build/bin/SoapMacUi.app
WAILS    := $(shell go env GOPATH)/bin/wails

.PHONY: help dev build build-universal build-win sign notarize test lint clean run

help:
	@grep -E '^[a-z-]+:.*?##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s "$$(printf '\t')"

dev:            ## Natives Fenster, unsigniert, Hot Reload + Devtools (Rechtsklick)
	$(WAILS) dev

build:          ## Apple Silicon, unsigniert (Wails signiert nur ad-hoc, ohne Zertifikat)
	$(WAILS) build -platform darwin/arm64 -skipbindings

build-universal: ## Universal Binary (arm64 + amd64)
	$(WAILS) build -platform darwin/universal -skipbindings

build-win:      ## Windows-Build (nice to have, ungetestet)
	$(WAILS) build -platform windows/amd64 -skipbindings

sign: build     ## Signieren mit Hardened Runtime (SIGN_ID setzen)
	@test -n "$(SIGN_ID)" || { \
	  echo "SIGN_ID ist nicht gesetzt."; \
	  echo "Verfügbare Identitäten:"; security find-identity -v -p codesigning; \
	  echo 'Beispiel: make sign SIGN_ID="Apple Development: … (TEAMID)"'; \
	  exit 1; }
	codesign --force --deep --options runtime \
	  --entitlements build/darwin/entitlements.plist \
	  --sign "$(SIGN_ID)" $(APP)
	codesign --verify --verbose=2 $(APP)

notarize: sign  ## Notarisieren (Developer-ID-Zertifikat + NOTARY_PROFILE nötig)
	ditto -c -k --keepParent $(APP) build/bin/SoapMacUi.zip
	xcrun notarytool submit build/bin/SoapMacUi.zip --keychain-profile $(NOTARY_PROFILE) --wait
	xcrun stapler staple $(APP)

test:           ## Alle Tests, inklusive der echten Kunden-WSDLs
	go test ./... -count=1

lint:           ## Formatierung und Vet
	gofmt -l . | grep -v '^frontend/' || true
	go vet ./...

run: build      ## Unsigniert bauen und starten
	open $(APP)

bin: build      ## Unsigniert bauen und das nackte Binary starten (Log im Terminal)
	$(APP)/Contents/MacOS/SoapMacUi

clean:
	rm -rf build/bin
