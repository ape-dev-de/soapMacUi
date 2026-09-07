#!/usr/bin/env bash
#
# Nimmt das von Apple ausgestellte Zertifikat, verheiratet es mit dem privaten
# Schlüssel aus dem CSR-Schritt und legt beides in den Schlüsselbund.
#
#   ./scripts/import-developer-id.sh ~/Downloads/developerID_application.cer \
#                                    ~/Desktop/DeveloperID-CSR/developerid.key
#
set -euo pipefail

CER="${1:-$HOME/Downloads/developerID_application.cer}"
KEY="${2:-$HOME/Desktop/DeveloperID-CSR/developerid.key}"

[ -f "$CER" ] || { echo "Zertifikat nicht gefunden: $CER" >&2; exit 1; }
[ -f "$KEY" ] || { echo "Privater Schlüssel nicht gefunden: $KEY" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "1/4  Zertifikat lesen"
# Apple liefert DER; PEM kommt aber auch vor.
if ! openssl x509 -inform DER -in "$CER" -out "$TMP/cert.pem" 2>/dev/null; then
  openssl x509 -inform PEM -in "$CER" -out "$TMP/cert.pem"
fi
openssl x509 -in "$TMP/cert.pem" -noout -subject -enddate | sed 's/^/     /'

echo "2/4  Kette von Apple holen"
# Es gibt zwei Zwischenstellen. Welche gebraucht wird, steht im Aussteller
# des Zertifikats — die falsche ergäbe eine unvollständige Kette.
ISSUER="$(openssl x509 -in "$TMP/cert.pem" -noout -issuer)"
if grep -q "OU *= *G2" <<<"$ISSUER"; then
  CA_URL="https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer"
  echo "     Aussteller: G2 Sub-CA"
else
  CA_URL="https://www.apple.com/certificateauthority/DeveloperIDCA.cer"
  echo "     Aussteller: vorherige Sub-CA"
  echo "     Achtung: diese Zwischenstelle läuft am 01.02.2027 ab, und damit"
  echo "     auch dein Zertifikat. Ein neues mit G2 hält bis 2031."
fi
curl -fsS -o "$TMP/ca.cer"   "$CA_URL"
curl -fsS -o "$TMP/root.cer" https://www.apple.com/appleca/AppleIncRootCertificate.cer
openssl x509 -inform DER -in "$TMP/ca.cer"   -out "$TMP/ca.pem"
openssl x509 -inform DER -in "$TMP/root.cer" -out "$TMP/root.pem"
cat "$TMP/ca.pem" "$TMP/root.pem" > "$TMP/chain.pem"

echo "3/4  Passen Zertifikat und Schlüssel zusammen?"
A="$(openssl x509 -in "$TMP/cert.pem" -noout -modulus | openssl md5)"
B="$(openssl rsa  -in "$KEY"          -noout -modulus | openssl md5)"
[ "$A" = "$B" ] || { echo "     Sie passen nicht zusammen — falscher Schlüssel oder falsches Zertifikat." >&2; exit 1; }
echo "     passt"

echo "4/4  In den Schlüsselbund"
# Das Transportpasswort schützt nur diese Datei und wird nirgends wieder
# gebraucht — also würfeln statt abfragen. Niemand muss es kennen.
PW="$(openssl rand -base64 24)"
openssl pkcs12 -export -legacy \
  -inkey "$KEY" -in "$TMP/cert.pem" -certfile "$TMP/chain.pem" \
  -name "Developer ID Application" -passout "pass:$PW" -out "$TMP/developerid.p12" \
  2>/dev/null \
  || openssl pkcs12 -export \
       -inkey "$KEY" -in "$TMP/cert.pem" -certfile "$TMP/chain.pem" \
       -name "Developer ID Application" -passout "pass:$PW" -out "$TMP/developerid.p12"

OUT="$HOME/Desktop/DeveloperID-CSR/developerid.p12"
cp "$TMP/developerid.p12" "$OUT"
chmod 600 "$OUT"
security import "$OUT" -k "$HOME/Library/Keychains/login.keychain-db" \
  -P "$PW" -T /usr/bin/codesign -T /usr/bin/security

# Passwort im Schlüsselbund hinterlegen, damit der Secrets-Schritt die .p12
# öffnen kann, ohne dass es jemand abtippt.
security delete-generic-password -s "soapmacui-p12" -a "developerid" >/dev/null 2>&1 || true
security add-generic-password -s "soapmacui-p12" -a "developerid" -w "$PW" -U
unset PW

echo
echo "Im Schlüsselbund:"
security find-identity -v -p codesigning | grep "Developer ID Application" | sed 's/^/  /' || true
cat <<EOF

Die .p12 liegt unter:
  $OUT

Damit weiter:
  ./scripts/setup-release-secrets.sh <owner>/<repo>

Das Transportpasswort der .p12 wurde zufällig erzeugt und liegt im
Schlüsselbund unter dem Dienst "soapmacui-p12" — setup-release-secrets.sh
holt es von dort. Du musst es dir nicht merken.

Danach den privaten Schlüssel wegräumen; wer ihn hat, kann in deinem Namen
signieren:
  rm -P ~/Desktop/DeveloperID-CSR/developerid.key
EOF
