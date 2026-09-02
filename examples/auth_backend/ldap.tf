# An LDAP authentication backend and the cluster-wide selection that makes it
# the active one. Defining a backend does not activate it; the two are
# separate settings in Graylog.

variable "ldap_bind_password" {
  description = "Password for the LDAP system user."
  type        = string
  sensitive   = true
}

data "graylog_role" "reader" {
  name = "Reader"
}

resource "graylog_auth_backend" "ldap" {
  title       = "Corporate LDAP"
  description = "Directory-backed logins"

  default_roles = [data.graylog_role.reader.id]

  # Write-only: Graylog never returns it, so state is the only record.
  system_user_password = var.ldap_bind_password

  config_json = jsonencode({
    type                     = "ldap"
    servers                  = [{ host = "ldap.example.com", port = 389 }]
    transport_security       = "none"
    verify_certificates      = false
    system_user_dn           = "cn=admin,dc=example,dc=org"
    user_full_name_attribute = "cn"
    user_name_attribute      = "uid"
    user_search_base         = "dc=example,dc=org"
    user_search_pattern      = "(&(uid={0})(objectClass=person))"
    user_unique_id_attribute = "entryUUID"
  })
}

resource "graylog_auth_backend_activation" "active" {
  backend_id = graylog_auth_backend.ldap.id
}
