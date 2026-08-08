---
page_title: "turso_organization Data Source"
description: |-
  Reads nonsecret Turso organization configuration.
---

# turso_organization

```terraform
data "turso_organization" "current" {}
```

`slug` is optional and defaults to the provider organization. Read-only fields
include name, type, plan identity, overage and MFA settings, block status, and
platform.
