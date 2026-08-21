# Cluster-wide settings that Graylog stores as JSON documents keyed by the
# class that reads them. The document is replaced wholesale, so it must carry
# every field the class requires - read the current one first:
#
#   curl -u admin:<password> \
#     http://graylog.example.com/api/system/cluster_config/org.graylog2.users.UserConfiguration

resource "graylog_cluster_config" "users" {
  class = "org.graylog2.users.UserConfiguration"

  config_json = jsonencode({
    enable_global_session_timeout        = true
    global_session_timeout_interval      = "PT4H"
    allow_access_token_for_external_user = false
    restrict_access_token_to_admins      = true
    default_ttl_for_new_tokens           = "PT720H"
  })
}

resource "graylog_cluster_config" "message_processors" {
  class = "org.graylog2.messageprocessors.MessageProcessorsConfig"

  config_json = jsonencode({
    processor_order = [
      "org.graylog2.messageprocessors.MessageFilterChainProcessor",
      "org.graylog.plugins.pipelineprocessor.processors.PipelineInterpreter",
    ]
    disabled_processors = []
  })
}
