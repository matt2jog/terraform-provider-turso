resource "turso_group" "primary" {
  name              = "primary"
  location          = "aws-us-east-1"
  delete_protection = true
}
