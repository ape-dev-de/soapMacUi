#!/usr/bin/env bash
#
# Erzeugt eine Certificate Signing Request für ein Developer-ID-Zertifikat.
# Der private Schlüssel bleibt lokal; die .csr enthält kein Geheimnis.
#
set -euo pipefail

DIR="${1:-$HOME/Desktop/DeveloperID-CSR}"
NAME="${CSR_NAME:-$(id -F 2>/dev/null || whoami)}"
MAIL="${CSR_EMAIL:-$(git config user.email 2>/dev/null || echo "$(whoami)@localhost")}"
COUNTRY="${CSR_COUNTRY:-DE}"

mkdir -p "$DIR"; chmod 700 "$DIR"
openssl req -new -newkey rsa:2048 -nodes \
  -keyout "$DIR/developerid.key" -out "$DIR/developerid.csr" \
  -subj "/emailAddress=$MAIL/CN=$NAME/C=$COUNTRY"
chmod 600 "$DIR/developerid.key"

cat <<EOF

CSR erzeugt: $DIR/developerid.csr
Schlüssel:   $DIR/developerid.key  (bleibt hier, nicht weitergeben)

Weiter auf developer.apple.com/account/resources/certificates:
  1. "+" → "Developer ID Application"
  2. Profile Type: G2 Sub-CA (die "Previous Sub-CA" läuft 2027 aus)
  3. developerid.csr hochladen, Zertifikat herunterladen
  4. ./scripts/import-developer-id.sh ~/Downloads/developerID_application.cer
EOF
