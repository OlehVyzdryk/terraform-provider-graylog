# graylog_cluster_config (Resource)

Manages a document in Graylog's cluster configuration store (`/system/cluster_config/{class}`).

Graylog keeps cluster-wide settings as JSON documents keyed by the fully qualified name of the Java class that reads them. Many of those settings have no dedicated API and therefore no dedicated resource; this resource manages any of them.

## Example Usage

```hcl
# Session and token policy
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

# Order in which message processors run
resource "graylog_cluster_config" "processors" {
  class = "org.graylog2.messageprocessors.MessageProcessorsConfig"

  config_json = jsonencode({
    processor_order = [
      "org.graylog2.messageprocessors.MessageFilterChainProcessor",
      "org.graylog.plugins.pipelineprocessor.processors.PipelineInterpreter",
    ]
    disabled_processors = []
  })
}
```

## Whole-document semantics

Graylog deserializes the body into the target class, so **the document must be complete**. Omitting a field the class requires is rejected at apply time with `Couldn't parse cluster configuration ... Null <field>`; it is not treated as "leave that field alone". Read the current document before adopting a class:

```shell
curl -u admin:<password> \
  http://graylog.example.com/api/system/cluster_config/org.graylog2.users.UserConfiguration
```

The exact field set differs between Graylog versions — `UserConfiguration` takes two fields on 6.x and five on 7.x — so a document written for one version may be rejected by another.

## Which classes are accepted

The class must be resolvable by the server *and* covered by the server's `safe_classes` setting, which defaults to the `org.graylog.` and `org.graylog2.` prefixes. Graylog answers:

| Situation | Response |
|---|---|
| Class known, document stored | `200` with the document |
| Class known, nothing stored | `204` (some classes instead return `200` with the compiled-in defaults) |
| Class cannot be resolved | `404` |
| Class outside `safe_classes` | `400` |

`GET /api/system/cluster_config` lists the classes a server knows about.

## Argument Reference

- `class` (String, Required) — Fully qualified name of the Graylog configuration class. Changing it forces replacement, because a different class is a different document.
- `config_json` (String, Required) — The configuration document, JSON-encoded. Key order and whitespace are not significant.
- `timeouts` (Block, Optional) — `create`, `update` and `delete` timeouts.

## Attribute Reference

- `id` (String) — Identical to `class`.

## Behaviour on destroy

Destroying the resource deletes the stored document, which makes Graylog fall back to the default compiled into the server. It does **not** restore a value that existed before Terraform took over. To adopt an existing document without overwriting it first, import it.

## Import

```shell
terraform import graylog_cluster_config.users org.graylog2.users.UserConfiguration
```

Import stores the server's document in canonical JSON form, so the first plan afterwards may show a formatting-only difference against your configuration. Applying once reconciles it.
