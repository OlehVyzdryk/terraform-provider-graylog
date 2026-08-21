# A GeoIP enrichment stack: cache + adapter + the table that pipeline rules
# resolve by name. Referencing the .id attributes rather than literal IDs is
# what makes Terraform create these in order and destroy them in reverse -
# Graylog refuses to delete a cache or adapter a table still references.

resource "graylog_lookup_cache" "geoip" {
  name        = "geoip-cache"
  title       = "GeoIP Cache"
  description = "Node-local, in-memory cache"

  config_json = jsonencode({
    type                     = "guava_cache"
    max_size                 = 1000
    expire_after_access      = 60
    expire_after_access_unit = "SECONDS"
    expire_after_write       = 0
  })
}

resource "graylog_lookup_adapter" "geoip_city" {
  name  = "geoip-city"
  title = "GeoIP City"

  config_json = jsonencode({
    type                = "maxmind_geoip"
    path                = "/usr/share/graylog/data/geolocation/GeoLite2-City.mmdb"
    database_type       = "MAXMIND_CITY"
    check_interval      = 1
    check_interval_unit = "MINUTES"
  })
}

resource "graylog_lookup_table" "geoip_city" {
  name            = "geoip-city-lookup"
  title           = "GeoIP City Lookup"
  cache_id        = graylog_lookup_cache.geoip.id
  data_adapter_id = graylog_lookup_adapter.geoip_city.id
}

resource "graylog_pipeline_rule" "geoip_lookup" {
  description = "Enrich messages with geolocation derived from src_ip"

  source = <<-EOT
    rule "GeoIP Lookup"
    when
      has_field("src_ip")
      && NOT is_null(lookup_value("geoip-city-lookup", to_string($message.src_ip)))
    then
      let geo = lookup("geoip-city-lookup", to_string($message.src_ip));
      set_field("src_ip_geo_country", geo["country"].iso_code);
      set_field("src_ip_geo_city", geo["city"].names.en);
    end
  EOT

  depends_on = [graylog_lookup_table.geoip_city]
}
