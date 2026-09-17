#!/bin/sh
set -e

# Executed automatically by the official nginx image entrypoint through /docker-entrypoint.d/*.sh before nginx starts.
# Generate runtime config.js from environment variables. Each analytics provider has an independent variable;
# unset providers remain disabled, load no scripts, and send no external requests. Multiple providers may be enabled together.

# GA4 and Baidu IDs contain only letters, numbers, and hyphens. Remove other characters
# so quotes and similar values cannot break the JavaScript strings in config.js as a defense-in-depth measure.
sanitize_id() {
    printf '%s' "$1" | tr -cd 'A-Za-z0-9-'
}

# URLs and environment names keep their meaning, so only strip characters that could break
# the generated JavaScript string literal (quotes, backslashes, newlines).
sanitize_text() {
    printf '%s' "$1" | tr -d '"\\' | tr -d '\r\n'
}

GA4_ID=$(sanitize_id "${ANALYTICS_GA4_ID:-}")
BAIDU_ID=$(sanitize_id "${ANALYTICS_BAIDU_ID:-}")
API_BASE_URL=$(sanitize_text "${API_BASE_URL:-}")
# Admin console origin. Empty means this environment has no separate admin site and the
# user menu hides the entry; when set it must be an absolute URL (the admin app lives on its own domain).
ADMIN_BASE_URL=$(sanitize_text "${ADMIN_BASE_URL:-}")
SITE_ENV=$(sanitize_id "${SITE_ENV:-production}")

cat > /usr/share/nginx/html/config.js <<EOF
window.__RUNTIME_CONFIG__ = {
  ANALYTICS_GA4_ID: "${GA4_ID}",
  ANALYTICS_BAIDU_ID: "${BAIDU_ID}",
  API_BASE_URL: "${API_BASE_URL}",
  ADMIN_BASE_URL: "${ADMIN_BASE_URL}",
  SITE_ENV: "${SITE_ENV}"
};
EOF
