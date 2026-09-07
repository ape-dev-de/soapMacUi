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

# gh fragt das Terminal nach Farben und Grösse. Die Antworten landen auf
# stdin und würden in der nächsten Eingabe stecken. Abschalten, was geht.
export NO_COLOR=1 CLICOLOR=0 GH_NO_UPDATE_NOTIFIER=1

echo "Repository: $REPO"
echo

# Reste im Eingabepuffer wegwerfen, bevor gefragt wird.
drain_stdin() { while read -r -s -t 0.1 -n 4096 _ 2>/dev/null; do :; done; true; }

# Steuerzeichen entfernen und Rand trimmen. Genau hier ging es schief:
# eine Terminalantwort im Wert macht das Secret ungültig.
clean() {
  printf '%s' "$1" | LC_ALL=C tr -d '\000-\037\177' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

ask() {   # ask <Variablenname> <Prompt> <Regex> <Fehlertext> [-s]
  local __var="$1" __prompt="$2" __re="$3" __err="$4" __silent="${5:-}" __val
  while :; do
    drain_stdin
    if [ "$__silent" = "-s" ]; then
      read -r -s -p "$__prompt" __val < /dev/tty; echo
    else
      read -r -p "$__prompt" __val < /dev/tty
    fi
    __val="$(clean "$__val")"
    if [ -z "$__val" ]; then echo "     Eingabe ist leer." >&2; continue; fi
    if [ -n "$__re" ] && ! [[ "$__val" =~ $__re ]]; then echo "     $__err" >&2; continue; fi
    eval "$__var=\$__val"
    return 0
  done
}

# ---------------------------------------------------------------- Zertifikat

echo "1/3  Signaturzertifikat"
# Kein mapfile: macOS liefert Bash 3.2 aus, das gibt es erst ab Bash 4.
IDS=()
while IFS= read -r line; do
  [ -n "$line" ] && IDS+=("$line")
done < <(security find-identity -v -p codesigning 2>/dev/null \
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
  drain_stdin
  read -r -p "  Welche verwenden? [0-$(( ${#IDS[@]} - 1 ))] " IDX < /dev/tty
  IDX="$(clean "$IDX")"
fi
SIGN_ID="$(clean "$(sed -E 's/.*"(.*)"/\1/' <<<"${IDS[$IDX]}")")"
HASH="$(awk '{print $2}' <<<"${IDS[$IDX]}")"
echo "  Verwende: $SIGN_ID"

P12="${P12_PATH:-$HOME/Desktop/DeveloperID-CSR/developerid.p12}"
TMP="$(mktemp -d)"
trap 'if [ -f "$TMP/cert.p12" ]; then dd if=/dev/urandom of="$TMP/cert.p12" \
      bs=1024 count=64 conv=notrunc 2>/dev/null || true; fi; rm -rf "$TMP"' EXIT

if [ -f "$P12" ] && P12PW="$(security find-generic-password -s soapmacui-p12 \
                              -a developerid -w 2>/dev/null)"; then
  # import-developer-id.sh hat schon eine .p12 gebaut und das zufällige
  # Transportpasswort im Schlüsselbund hinterlegt — kein zweiter Export nötig.
  echo "  Verwende $P12 (Passwort aus dem Schlüsselbund)"
  cp "$P12" "$TMP/cert.p12"
else
  echo "  Keine vorbereitete .p12 gefunden — exportiere aus dem Schlüsselbund."
  echo "  Das Transportpasswort wird zufällig erzeugt; du musst es dir nicht merken."
  P12PW="$(openssl rand -base64 24)"
  security export -t identities -f pkcs12 -P "$P12PW" -o "$TMP/cert.p12" \
    -k "$(security default-keychain -d user | tr -d ' "')" 2>/dev/null \
    || security export -t identities -f pkcs12 -P "$P12PW" -o "$TMP/cert.p12"
  [ -s "$TMP/cert.p12" ] || { echo "  Export fehlgeschlagen." >&2; exit 1; }
fi

base64 -i "$TMP/cert.p12" | gh secret set MACOS_CERT_P12 --repo "$REPO"
printf '%s' "$P12PW"   | gh secret set MACOS_CERT_PASSWORD --repo "$REPO"
printf '%s' "$SIGN_ID" | gh secret set MACOS_SIGN_IDENTITY --repo "$REPO"
unset P12PW
echo "  ✓ MACOS_CERT_P12, MACOS_CERT_PASSWORD, MACOS_SIGN_IDENTITY"

# -------------------------------------------------------------- Notarisierung

echo
echo "2/3  Notarisierung"
TEAM_ID="$(clean "$(sed -E 's/.*\(([A-Z0-9]+)\)$/\1/' <<<"$SIGN_ID")")"
echo "  Team-ID aus dem Zertifikat: $TEAM_ID"
[[ "$TEAM_ID" =~ ^[A-Z0-9]{10}$ ]] || { echo "  Team-ID sieht falsch aus: '$TEAM_ID'" >&2; exit 1; }

ask APPLE_ID "  Apple-ID (E-Mail): " \
    '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$' \
    "Das sieht nicht nach einer E-Mail-Adresse aus."

echo
echo "  Es wird ein *app-spezifisches* Passwort gebraucht, nicht das Passwort"
echo "  deiner Apple-ID. Anlegen unter appleid.apple.com → Anmelden und"
echo "  Sicherheit → App-spezifische Passwörter. Form: abcd-efgh-ijkl-mnop"
ask APP_PW "  App-spezifisches Passwort: " \
    '^[A-Za-z]{4}-[A-Za-z]{4}-[A-Za-z]{4}-[A-Za-z]{4}$' \
    "Erwartet wird die Form abcd-efgh-ijkl-mnop (vier Vierergruppen)." -s

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
