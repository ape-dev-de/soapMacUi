#!/usr/bin/env bash
#
# Legt die Repository-Secrets an, die .github/workflows/release.yml braucht.
#
# Das Skript zeigt keine Geheimnisse an und schreibt keine in Dateien, die
# liegen bleiben: das Zertifikat wandert über eine Pipe direkt zu `gh`, die
# temporäre .p12 wird danach überschrieben und gelöscht.
#
#   ./scripts/setup-release-secrets.sh [owner/repo]
#
set -euo pipefail

REPO="${1:-}"
if [ -z "$REPO" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
fi
if [ -z "$REPO" ]; then
  echo "Kein Repository erkannt. Aufruf: $0 owner/repo" >&2
  exit 1
fi

command -v gh >/dev/null || { echo "gh fehlt: brew install gh" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "Nicht angemeldet: gh auth login" >&2; exit 1; }

echo "Repository: $REPO"
echo

# ---------------------------------------------------------------- Zertifikat

echo "1/3  Signaturzertifikat"
mapfile -t IDS < <(security find-identity -v -p codesigning 2>/dev/null \
  | grep "Developer ID Application" || true)

if [ "${#IDS[@]}" -eq 0 ]; then
  cat >&2 <<'HINT'

  Kein "Developer ID Application"-Zertifikat im Schlüsselbund.

  Ohne dieses Zertifikat ist keine Notarisierung möglich, und die App zeigt
  bei anderen Nutzern eine Gatekeeper-Warnung. Ein "Apple Development"-
  Zertifikat genügt dafür nicht.

  Anlegen (einmalig, Account-Holder-Rolle nötig):
    1. developer.apple.com/account/resources/certificates → "+"
    2. Art: "Developer ID Application"
    3. CSR aus Schlüsselbundverwaltung → Zertifikatsassistent →
       "Zertifikat von einer Zertifizierungsinstanz anfordern"
    4. Zertifikat laden und per Doppelklick in den Schlüsselbund legen
    5. Dieses Skript erneut ausführen

HINT
  exit 1
fi

echo "  Gefunden:"
for i in "${!IDS[@]}"; do echo "    [$i] ${IDS[$i]#*\"}" | sed 's/"$//'; done
IDX=0
if [ "${#IDS[@]}" -gt 1 ]; then
  read -r -p "  Welche verwenden? [0-$(( ${#IDS[@]} - 1 ))] " IDX
fi
SIGN_ID="$(sed -E 's/.*"(.*)"/\1/' <<<"${IDS[$IDX]}")"
HASH="$(awk '{print $2}' <<<"${IDS[$IDX]}")"
echo "  Verwende: $SIGN_ID"

TMP="$(mktemp -d)"
trap 'if [ -f "$TMP/cert.p12" ]; then dd if=/dev/urandom of="$TMP/cert.p12" \
      bs=1024 count=64 conv=notrunc 2>/dev/null || true; fi; rm -rf "$TMP"' EXIT

echo
echo "  Für den Export wird ein Transportpasswort gebraucht. Es schützt nur die"
echo "  temporäre Datei und wird gleich als Secret hinterlegt — denk dir eines aus."
read -r -s -p "  Transportpasswort: " P12PW; echo
read -r -s -p "  Wiederholen:       " P12PW2; echo
[ "$P12PW" = "$P12PW2" ] || { echo "  Passwörter stimmen nicht überein." >&2; exit 1; }

echo "  Exportiere (der Schlüsselbund fragt gleich nach Erlaubnis) …"
security export -t identities -f pkcs12 -P "$P12PW" -o "$TMP/cert.p12" \
  -k "$(security default-keychain -d user | tr -d ' "')" 2>/dev/null \
  || security export -t identities -f pkcs12 -P "$P12PW" -o "$TMP/cert.p12"

[ -s "$TMP/cert.p12" ] || { echo "  Export fehlgeschlagen." >&2; exit 1; }

base64 -i "$TMP/cert.p12" | gh secret set MACOS_CERT_P12 --repo "$REPO"
printf '%s' "$P12PW"      | gh secret set MACOS_CERT_PASSWORD --repo "$REPO"
printf '%s' "$SIGN_ID"    | gh secret set MACOS_SIGN_IDENTITY --repo "$REPO"
unset P12PW P12PW2
echo "  ✓ MACOS_CERT_P12, MACOS_CERT_PASSWORD, MACOS_SIGN_IDENTITY"

# -------------------------------------------------------------- Notarisierung

echo
echo "2/3  Notarisierung"
TEAM_ID="$(sed -E 's/.*\(([A-Z0-9]+)\)$/\1/' <<<"$SIGN_ID")"
echo "  Team-ID aus dem Zertifikat: $TEAM_ID"
read -r -p "  Apple-ID (E-Mail): " APPLE_ID
echo
echo "  Es wird ein *app-spezifisches* Passwort gebraucht, nicht das Passwort"
echo "  deiner Apple-ID. Anlegen unter appleid.apple.com → Anmelden und"
echo "  Sicherheit → App-spezifische Passwörter."
read -r -s -p "  App-spezifisches Passwort: " APP_PW; echo

printf '%s' "$APPLE_ID" | gh secret set APPLE_ID --repo "$REPO"
printf '%s' "$TEAM_ID"  | gh secret set APPLE_TEAM_ID --repo "$REPO"
printf '%s' "$APP_PW"   | gh secret set APPLE_APP_PASSWORD --repo "$REPO"
unset APP_PW
echo "  ✓ APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD"

# ------------------------------------------------------------------ Kontrolle

echo
echo "3/3  Kontrolle"
gh secret list --repo "$REPO" | sed 's/^/  /'

cat <<EOF

Fertig. Release auslösen:

    git tag v0.1.0 && git push origin v0.1.0

Lokal notarisieren geht danach auch:

    xcrun notarytool store-credentials notary \\
      --apple-id "<Apple-ID>" --team-id "$TEAM_ID" --password "<App-Passwort>"
    make notarize SIGN_ID="$SIGN_ID"
EOF
