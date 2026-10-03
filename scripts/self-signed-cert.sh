#!/usr/bin/env bash
# Generate a self-signed certificate so HTTPS can be tried out locally.
#
#   ./scripts/self-signed-cert.sh              # writes ./certs/server.{crt,key}
#   HOST=update.example.com ./scripts/self-signed-cert.sh /etc/updatesite/certs
#
# Browsers will warn about a self-signed certificate. Use a real certificate
# from an internal CA or Let's Encrypt for anything beyond testing.
set -euo pipefail

DIR="${1:-./certs}"
HOST="${HOST:-localhost}"
DAYS="${DAYS:-825}"
EXTRA_IP="${EXTRA_IP:-127.0.0.1}"

command -v openssl >/dev/null 2>&1 || {
  echo "error: openssl is not installed" >&2
  exit 1
}

mkdir -p "$DIR"

# The subject and the SAN go into a config file rather than "-subj /CN=...".
# Git Bash on Windows rewrites an argument that starts with a slash into a
# Windows path, which turns "/CN=host" into "C:/Program Files/Git/CN=host" and
# makes openssl reject the subject.
CONF="$(mktemp)"
trap 'rm -f "$CONF"' EXIT
cat >"$CONF" <<EOF
[req]
prompt = no
distinguished_name = dn
x509_extensions = v3

[dn]
CN = $HOST

[v3]
subjectAltName = DNS:$HOST, DNS:localhost, IP:$EXTRA_IP
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF

# The subjectAltName is what clients actually check; CN alone is ignored by
# every modern browser.
openssl req -x509 -newkey rsa:2048 -nodes -sha256 \
  -days "$DAYS" \
  -keyout "$DIR/server.key" \
  -out "$DIR/server.crt" \
  -config "$CONF"

chmod 600 "$DIR/server.key"

echo "wrote $DIR/server.crt and $DIR/server.key (CN=$HOST, valid $DAYS days)"
echo
echo "enable HTTPS with:"
echo "  TLS_CERT=$DIR/server.crt TLS_KEY=$DIR/server.key TLS_REDIRECT=true docker compose up -d"
