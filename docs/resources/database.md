---
page_title: "turso_database Resource"
description: |-
  Manages a Turso database without managing its auth tokens or contents.
---

# turso_database

```terraform
resource "turso_database" "application" {
  name              = "application-prod"
  group             = turso_group.primary.name
  size_limit_bytes  = 500000000
  delete_protection = true
}
```

## Schema

### Required

- `name` (String, Forces replacement)
- `group` (String, Forces replacement)

### Optional

- `size_limit_bytes` (Number) Uses Turso's account default when omitted.
- `delete_protection` (Boolean) Defaults to `true`. Set it to `false` and
  apply before destroy.

### Read-only

- `id` (`organization/name`)
- `organization`
- `uuid`
- `hostname`
- `url` (Nonsecret `libsql://` endpoint)
- `primary_location`
- `regions`

## Import

```shell
terraform import turso_database.application my-organization/application-prod
```
