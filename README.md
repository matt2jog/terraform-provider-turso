# Terraform Provider for Turso

This provider manages stable Turso Platform API infrastructure at
`registry.terraform.io/matt2jog/turso`.

It intentionally manages only groups and databases. Database credentials,
database contents, SQL migrations, usage history, and auth-token rotation stay
outside Terraform so secret values never enter plans or state.

## Example

```hcl
terraform {
  required_providers {
    turso = {
      source  = "matt2jog/turso"
      version = "~> 0.1"
    }
  }
}

provider "turso" {
  organization = "my-organization"
  # token defaults to TURSO_API_TOKEN, then TURSO_API_KEY.
}

resource "turso_group" "primary" {
  name              = "primary"
  location          = "aws-us-east-1"
  delete_protection = true
}

resource "turso_database" "application" {
  name               = "application-prod"
  group              = turso_group.primary.name
  size_limit_bytes   = 500000000
  delete_protection  = true
}
```

Protected objects require a deliberate two-step deletion: set
`delete_protection = false` and apply, then destroy. The provider never lowers
protection automatically during destroy.

## Authentication

Use an organization-scoped Turso Platform API token where possible. Supply it
through `TURSO_API_TOKEN` (preferred), `TURSO_API_KEY` (compatibility), or the
sensitive provider argument. Do not commit tokens.

## Development

```shell
go test ./...
go vet ./...
go build ./...
```

All HTTP tests use local mock servers. Live acceptance tests and live resource
creation are intentionally absent from the default suite.

Before the provider is published, build it into a dedicated plugin directory
and point Terraform at that directory with a development override:

```hcl
provider_installation {
  dev_overrides {
    "matt2jog/turso" = "C:/absolute/path/to/dev-plugins"
  }
  direct {}
}
```

Save that block in a Terraform CLI configuration file and set
`TF_CLI_CONFIG_FILE` to its path. The plugin directory must contain a binary
named `terraform-provider-turso` (or `terraform-provider-turso.exe` on
Windows). Run normal Terraform commands with the override active; the local
override does not require a Registry release.
