terraform {
  required_providers {
    turso = {
      source = "matt2jog/turso"
    }
  }
}

provider "turso" {
  organization = "my-organization"
}
