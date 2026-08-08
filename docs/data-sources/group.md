---
page_title: "turso_group Data Source"
description: |-
  Reads an existing Turso group.
---

# turso_group

```terraform
data "turso_group" "primary" {
  name = "primary"
}
```

`organization` is optional and defaults to the provider organization. The data
source exports the stable ID, UUID, locations, primary location, and deletion
protection.
