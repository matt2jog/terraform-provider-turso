---
page_title: "Provider: Turso"
description: |-
  Manage stable Turso groups and databases without placing database tokens in Terraform state.
---

# Turso Provider

Use the Turso provider to manage groups and databases through the Turso Platform
API. SQL schemas, data, migrations, and database auth tokens are deliberately
outside the provider.

## Example Usage

```terraform
provider "turso" {
  organization = "my-organization"
}
```

## Schema

### Optional

- `organization` (String) Turso organization slug. Falls back to
  `TURSO_ORGANIZATION`, then `TURSO_ORG`.
- `token` (String, Sensitive) Platform API token. Falls back to
  `TURSO_API_TOKEN`, then `TURSO_API_KEY`.
- `api_url` (String) API base URL. Falls back to `TURSO_API_URL`, then
  `https://api.turso.tech`.
