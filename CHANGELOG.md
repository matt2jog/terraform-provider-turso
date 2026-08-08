# Changelog

## 0.1.4 - 2026-08-08

- Preserve the configured or prior-state organization during in-place group
  and database configuration updates.

## 0.1.3 - 2026-08-08

- Preserve Turso's canonical location key when importing a group whose primary
  region uses the shorter API representation.

## 0.1.0 - 2026-08-07

- Add provider configuration for organization, token, and API URL.
- Add managed Turso group and database resources with deletion protection.
- Add organization, locations, group, and database data sources.
- Add import support using `organization/name`.
- Add bounded retries, cancellation, readiness polling, and redacted errors.
