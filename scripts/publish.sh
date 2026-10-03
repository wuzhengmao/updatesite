#!/usr/bin/env bash
# Publish a release into an archive directory.
#
# It creates <root>/<app>/<version>/, copies the given files into it, writes a
# SHA256SUMS manifest and a release.json, and optionally pokes the update site
# so the release shows up immediately.
#
# Usage:
#   ./publish.sh -a myapp -v 1.2.0 dist/MyApp-1.2.0-windows-x64.exe
#   ./publish.sh -a myapp -v 1.2.0 -n notes/1.2.0.md -m -t "体验优化版" dist/*
#
# See docs/RELEASE-SPEC.md for the layout this produces.
set -euo pipefail

APP=""
VERSION=""
ROOT="${ARCHIVE_ROOT:-./apps}"
CHANNEL="stable"
NOTES=""
TITLE=""
MANDATORY=""
FORCE=""
SITE_URL="${SITE_URL:-}"
TOKEN="${RESCAN_TOKEN:-}"
DRY_RUN=""
FILES=()

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '  %s\n' "$*"; }

usage() {
  sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
  cat <<'EOF'

Options:
  -a, --app <id>        应用 ID（目录名，[a-z0-9._-]）
  -v, --version <ver>   版本号，例如 1.2.0 或 1.2.0-rc.1
  -r, --root <dir>      归档根目录，默认 $ARCHIVE_ROOT 或 ./apps
  -c, --channel <name>  发布通道，默认 stable
  -n, --notes <file>    更新说明（Markdown），复制为版本目录下的 CHANGELOG.md
  -t, --title <text>    版本标题
  -m, --mandatory       标记为强制更新
  -f, --force           覆盖已存在的版本目录
  -u, --url <base>      发布后调用 <base>/api/v1/rescan 通知站点
  -k, --token <token>   调用 rescan 时使用的令牌
      --dry-run         只打印将要执行的操作
  -h, --help            显示本帮助
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    -a|--app)     APP="${2:?}"; shift 2 ;;
    -v|--version) VERSION="${2:?}"; shift 2 ;;
    -r|--root)    ROOT="${2:?}"; shift 2 ;;
    -c|--channel) CHANNEL="${2:?}"; shift 2 ;;
    -n|--notes)   NOTES="${2:?}"; shift 2 ;;
    -t|--title)   TITLE="${2:?}"; shift 2 ;;
    -m|--mandatory) MANDATORY=1; shift ;;
    -f|--force)   FORCE=1; shift ;;
    -u|--url)     SITE_URL="${2:?}"; shift 2 ;;
    -k|--token)   TOKEN="${2:?}"; shift 2 ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    -*)           die "unknown option: $1" ;;
    *)            FILES+=("$1"); shift ;;
  esac
done

[ -n "$APP" ]     || { usage; die "--app is required"; }
[ -n "$VERSION" ] || { usage; die "--version is required"; }
[ ${#FILES[@]} -gt 0 ] || die "no files given"

# The app id doubles as a directory name, so keep it to a safe character set.
case "$APP" in
  *[!a-zA-Z0-9._-]*|.*) die "invalid app id: $APP" ;;
esac
# The version doubles as a directory name too.
case "$VERSION" in
  *[!a-zA-Z0-9._+-]*|.*) die "invalid version: $VERSION" ;;
esac

for f in "${FILES[@]}"; do
  [ -f "$f" ] || die "not a file: $f"
done
if [ -n "$NOTES" ]; then
  [ -f "$NOTES" ] || die "notes file not found: $NOTES"
fi

TARGET="$ROOT/$APP/$VERSION"
if [ -e "$TARGET" ] && [ -z "$FORCE" ]; then
  die "$TARGET already exists (use --force to overwrite)"
fi

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$@"
  else
    die "neither sha256sum nor shasum is available"
  fi
}

echo "publishing $APP $VERSION -> $TARGET"
if [ -n "$DRY_RUN" ]; then
  info "would copy: ${FILES[*]}"
  [ -n "$NOTES" ] && info "would install notes from $NOTES"
  info "would write release.json (channel=$CHANNEL)"
  exit 0
fi

mkdir -p "$TARGET"
COPIED=()
for f in "${FILES[@]}"; do
  cp -f "$f" "$TARGET/"
  COPIED+=("$(basename "$f")")
  info "added $(basename "$f")"
done

# Checksum exactly the files that were just published, nothing else.
( cd "$TARGET" && sha256_of "${COPIED[@]}" > SHA256SUMS )
info "wrote SHA256SUMS (${#COPIED[@]} files)"

if [ -n "$NOTES" ]; then
  cp -f "$NOTES" "$TARGET/CHANGELOG.md"
  info "installed CHANGELOG.md"
fi

# release.json is only written when it carries something worth saying.
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
{
  echo "{"
  printf '  "channel": "%s"' "$(json_escape "$CHANNEL")"
  [ -n "$TITLE" ] && printf ',\n  "title": "%s"' "$(json_escape "$TITLE")"
  [ -n "$MANDATORY" ] && printf ',\n  "mandatory": true'
  printf '\n}\n'
} > "$TARGET/release.json"
info "wrote release.json"

if [ -n "$SITE_URL" ]; then
  url="${SITE_URL%/}/api/v1/rescan"
  if [ -n "$TOKEN" ]; then
    curl -fsS -X POST -H "Authorization: Bearer $TOKEN" "$url" >/dev/null
  else
    curl -fsS -X POST "$url" >/dev/null
  fi
  info "notified $url"
fi

echo "done: $TARGET"
