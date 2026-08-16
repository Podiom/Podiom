#!/usr/bin/env bash
# Fails when `npx cap sync` changed a committed file in a native project.
#
# The ios/ and android/ projects are generated once and then committed, but
# Capacitor keeps writing into them: adding a plugin rewrites the iOS
# Package.swift and the Android settings/build gradle files. If someone adds a
# plugin and commits only package.json, the native projects on disk no longer
# match the configuration, and the next person to build gets a failure that has
# nothing to do with their change.
#
# So CI syncs and then asserts nothing moved. Build output and copied web assets
# are gitignored by each platform's own .gitignore, so a diff here is always a
# real drift.
#
# Usage: check-native-sync.sh <ios|android>
set -euo pipefail

platform="${1:?usage: check-native-sync.sh <ios|android>}"

if git diff --quiet -- "$platform"; then
  echo "$platform project is in sync with capacitor.config.ts"
  exit 0
fi

cat >&2 <<EOF
error: the committed $platform project is out of sync with the Capacitor
       configuration. Run:

           npm run sync

       and commit the resulting changes under $platform/.

Files that changed when CI ran the sync:
EOF
git --no-pager diff --stat -- "$platform" >&2
echo >&2
git --no-pager diff -- "$platform" >&2
exit 1
