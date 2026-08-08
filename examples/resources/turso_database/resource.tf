resource "turso_database" "application" {
  name              = "application-prod"
  group             = turso_group.primary.name
  size_limit_bytes  = 500000000
  delete_protection = true
}
