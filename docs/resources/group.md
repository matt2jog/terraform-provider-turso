---
page_title: "turso_group Resource"
description: |-
  Manages a Turso database group.
---

# turso_group

```terraform
resource "turso_group" "primary" {
  name              = "primary"
  location          = "aws-us-east-1"
  delete_protection = true
}
```

## Schema

### Required

- `name` (String, Forces replacement)
- `location` (String, Forces replacement) Initial primary Turso location key.

### Optional

- `delete_protection` (Boolean) Defaults to `true`. Set it to `false` and
  apply before destroy.

### Read-only

- `id` (`organization/name`)
- `organization`
- `uuid`
- `primary_location`
- `locations`

## Import

```shell
terraform import turso_group.primary my-organization/primary
```
