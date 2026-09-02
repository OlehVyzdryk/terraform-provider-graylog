package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAuthBackendConfig_InjectsPassword(t *testing.T) {
	got, err := buildAuthBackendConfig(`{"type":"ldap","user_name_attribute":"uid"}`, "s3cret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if doc[systemUserPasswordKey] != "s3cret" {
		t.Fatalf("password was not injected: %s", string(got))
	}
}

// Omitting the password is how a practitioner says "keep whatever is stored";
// Graylog leaves the existing one alone when the field is absent.
func TestBuildAuthBackendConfig_OmitsPasswordWhenUnset(t *testing.T) {
	got, err := buildAuthBackendConfig(`{"type":"ldap"}`, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(got), systemUserPasswordKey) {
		t.Fatalf("an unset password must not appear in the request: %s", string(got))
	}
}

// Two sources of truth for one field would silently disagree, so the config
// document is not allowed to carry it.
func TestBuildAuthBackendConfig_RejectsPasswordInsideConfig(t *testing.T) {
	_, err := buildAuthBackendConfig(`{"type":"ldap","system_user_password":"x"}`, "")
	if err == nil {
		t.Fatal("expected a password inside config_json to be rejected")
	}
	if !strings.Contains(err.Error(), systemUserPasswordKey) {
		t.Fatalf("error should name the offending field, got %v", err)
	}
}

func TestBuildAuthBackendConfig_RejectsNonObject(t *testing.T) {
	for _, document := range []string{"", "[]", `"scalar"`, "{not json"} {
		if _, err := buildAuthBackendConfig(document, ""); err == nil {
			t.Fatalf("expected %q to be rejected", document)
		}
	}
}

// The sentinel Graylog returns in place of the password must never reach
// state, by any path.
func TestRefreshAuthBackendConfig_DropsTheSentinel(t *testing.T) {
	server := `{"type":"ldap","user_name_attribute":"uid","system_user_password":{"is_set":true}}`

	for name, state := range map[string]string{
		"with a mask":    `{"type":"ldap","user_name_attribute":"uid"}`,
		"without a mask": "",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := refreshAuthBackendConfig(server, state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(got, "is_set") || strings.Contains(got, systemUserPasswordKey) {
				t.Fatalf("the write-only field leaked into state: %s", got)
			}
		})
	}
}

// Regression: an imported backend has no config_json, which used to make the
// projection mask empty so both sides compared as {} and the server
// configuration was never adopted.
func TestRefreshAuthBackendConfig_ImportAdoptsServerConfig(t *testing.T) {
	server := `{"type":"ldap","user_name_attribute":"uid","system_user_password":{"is_set":true},"email_attributes":["mail"]}`

	got, err := refreshAuthBackendConfig(server, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" || got == "{}" {
		t.Fatalf("import left config_json empty: %q", got)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if doc["user_name_attribute"] != "uid" {
		t.Fatalf("server configuration was not adopted: %s", got)
	}
	// Import has no mask, so server-side extras come along; that is the point.
	if _, present := doc["email_attributes"]; !present {
		t.Fatalf("import should adopt the whole configuration: %s", got)
	}
}

// Once a mask exists, keys the server added on its own stay out of it.
func TestRefreshAuthBackendConfig_ProjectsOntoManagedKeys(t *testing.T) {
	server := `{"type":"ldap","user_name_attribute":"uid","email_attributes":["mail"]}`
	state := `{"type":"ldap","user_name_attribute":"uid"}`

	got, err := refreshAuthBackendConfig(server, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, err := CanonicalizeJSONFromString(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("unchanged configuration would show a diff\n want: %s\n got : %s", want, got)
	}
}

func TestRefreshAuthBackendConfig_ReportsRealDrift(t *testing.T) {
	server := `{"type":"ldap","user_name_attribute":"sAMAccountName"}`
	state := `{"type":"ldap","user_name_attribute":"uid"}`

	got, err := refreshAuthBackendConfig(server, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "sAMAccountName") {
		t.Fatalf("server value did not win: %s", got)
	}
}

func TestRefreshAuthBackendConfig_InvalidServerDocument(t *testing.T) {
	if _, err := refreshAuthBackendConfig(`{not json`, `{}`); err == nil {
		t.Fatal("expected an error for an unparseable server configuration")
	}
	if _, err := refreshAuthBackendConfig(`["array"]`, `{}`); err == nil {
		t.Fatal("expected an error for a non-object server configuration")
	}
}

// The document is decoded and re-encoded on its way to Graylog, so numbers
// must survive: an LDAP port re-encoded as 3.89e+02 would be rejected.
func TestBuildAuthBackendConfig_PreservesNumbers(t *testing.T) {
	got, err := buildAuthBackendConfig(
		`{"type":"ldap","servers":[{"host":"ldap.example.com","port":389}],"timeout":20000000}`, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(got), "e+") {
		t.Fatalf("numbers lost their notation: %s", string(got))
	}
	if !strings.Contains(string(got), `"port":389`) {
		t.Fatalf("port was rewritten: %s", string(got))
	}
	if !strings.Contains(string(got), `"timeout":20000000`) {
		t.Fatalf("large integer was rewritten: %s", string(got))
	}
}
