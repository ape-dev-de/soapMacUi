#!/usr/bin/env bash
#
# Zeigt, was bei Apples Notary-Dienst für dieses Team ansteht — auch die
# Einreichungen aus der CI, denn die laufen über dieselbe Apple-ID.
#
# Voraussetzung, einmalig:
#   xcrun notarytool store-credentials notary \
#     --apple-id "<Apple-ID>" --team-id <TEAM_ID> --password "<App-Passwort>"
#
#   ./scripts/notary-status.sh              Übersicht
#   ./scripts/notary-status.sh <id>         Details und Apples Protokoll
#
set -euo pipefail
PROFILE="${NOTARY_PROFILE:-notary}"

if ! xcrun notarytool history --keychain-profile "$PROFILE" >/dev/null 2>&1; then
  cat >&2 <<EOF
Kein Schlüsselbund-Profil "$PROFILE".

Einmalig anlegen:
  xcrun notarytool store-credentials $PROFILE \\
    --apple-id "<Apple-ID>" --team-id <TEAM_ID> --password "<App-Passwort>"
EOF
  exit 1
fi

if [ $# -ge 1 ]; then
  ID="$1"
  echo "=== Status ==="
  xcrun notarytool info "$ID" --keychain-profile "$PROFILE"
  echo
  echo "=== Protokoll von Apple ==="
  # Steht hier "Invalid", nennt das Protokoll den genauen Grund — meist ein
  # fehlender Hardened Runtime oder ein unsigniertes verschachteltes Binary.
  xcrun notarytool log "$ID" --keychain-profile "$PROFILE" || \
    echo "(noch kein Protokoll — die Einreichung läuft vermutlich noch)"
  exit 0
fi

echo "=== Letzte Einreichungen ==="
xcrun notarytool history --keychain-profile "$PROFILE" | sed 's/^/  /'
cat <<'EOF'

Bedeutung der Status:
  Accepted     durch; jetzt stapeln (xcrun stapler staple <App>)
  In Progress  in Apples Warteschlange
  Invalid      abgelehnt — Grund steht im Protokoll:
                 ./scripts/notary-status.sh <id>
  Rejected     abgelehnt aus formalen Gründen (selten)
EOF
