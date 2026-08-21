package provider

import (
	"encoding/json"
	"errors"
	"fmt"
)

// systemUserPasswordKey is the one configuration field Graylog accepts but
// never returns: a read echoes the object {"is_set": true} rather than the
// value. It is modelled as its own resource attribute instead of living in
// config_json, so the schema itself says that it cannot be refreshed.
const systemUserPasswordKey = "system_user_password"

// decodeConfigObject parses a configuration document, rejecting anything that
// is not a JSON object. Graylog always deserializes it into a typed class, so
// an array or scalar can never be valid.
func decodeConfigObject(document string) (map[string]any, error) {
	if document == "" {
		return nil, errors.New("config_json must not be empty")
	}
	parsed, err := decodeJSONPreservingNumbers(document)
	if err != nil {
		return nil, fmt.Errorf("config_json is not valid JSON: %w", err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, errors.New("config_json must be a JSON object")
	}
	return object, nil
}

// buildAuthBackendConfig assembles the document sent to Graylog: the
// practitioner's configuration plus the password, when one is set.
//
// Leaving the password out is meaningful rather than an error — Graylog keeps
// the stored one when the field is absent from an update, which is what lets
// a backend whose secret was set elsewhere still be managed here.
func buildAuthBackendConfig(document, password string) (json.RawMessage, error) {
	object, err := decodeConfigObject(document)
	if err != nil {
		return nil, err
	}
	if _, present := object[systemUserPasswordKey]; present {
		return nil, fmt.Errorf(
			"%s must be set through the resource attribute of the same name, not inside config_json",
			systemUserPasswordKey)
	}
	if password != "" {
		object[systemUserPasswordKey] = password
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// refreshAuthBackendConfig turns the server's configuration into the value
// config_json should hold after a read.
//
// The password is dropped first, so the {"is_set": true} sentinel can never
// reach state by any path. What remains is projected onto the keys the
// practitioner actually manages, because Graylog also materializes defaults
// of its own — email_attributes is the usual one — and comparing against
// those would report a difference on every plan.
//
// An empty state document means the resource was just imported and has no
// mask yet; the whole server configuration is adopted, so import produces
// something usable instead of an empty string.
func refreshAuthBackendConfig(serverConfig, stateConfig string) (string, error) {
	parsed, err := decodeJSONPreservingNumbers(serverConfig)
	if err != nil {
		return "", fmt.Errorf("server returned a configuration that is not valid JSON: %w", err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return "", errors.New("server returned a configuration that is not a JSON object")
	}
	delete(object, systemUserPasswordKey)

	stripped, err := CanonicalizeJSONValue(object)
	if err != nil {
		return "", err
	}
	return ProjectAndCanonicalizeJSON(stripped, stateConfig)
}
