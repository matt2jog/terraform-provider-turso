---
page_title: "turso_database Data Source"
description: |-
  Reads an existing Turso database without reading auth tokens or contents.
---

# turso_database

```terraform
data "turso_database" "application" {
  name = "application-prod"
}
```

`organization` is optional and defaults to the provider organization. The data
source exports group, size limit, deletion protection, UUID, hostname, URL,
primary location, and regions.
