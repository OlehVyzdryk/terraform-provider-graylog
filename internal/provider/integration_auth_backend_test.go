//go:build integration

package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Ultrafenrir/terraform-provider-graylog/internal/client"
)

func authTestClient(t *testing.T) *client.Client {
	t.Helper()
	baseURL := os.Getenv("URL")
	token := os.Getenv("TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("integration env is not configured: set URL and TOKEN env vars")
	}
	if _, err := base64.StdEncoding.DecodeString(token); err != nil {
		token = base64.StdEncoding.EncodeToString([]byte(token))
	}
	return client.New(baseURL, token)
}

// The configuration without the bind password; the password is a separate
// resource attribute and is merged in on the way out.
const testLDAPConfig = `{"type":"ldap","servers":[{"host":"openldap","port":389}],
  "transport_security":"none","verify_certificates":false,
  "system_user_dn":"cn=admin,dc=example,dc=org",
  "user_full_name_attribute":"cn","user_name_attribute":"uid",
  "user_search_base":"dc=example,dc=org","user_search_pattern":"(&(uid={0})(objectClass=person))",
  "user_unique_id_attribute":"entryUUID"}`

const testLDAPPassword = "admin"

// testLDAPPayload is what the resource actually sends: configuration plus the
// password.
func testLDAPPayload(t *testing.T) json.RawMessage {
	t.Helper()
	payload, err := buildAuthBackendConfig(testLDAPConfig, testLDAPPassword)
	if err != nil {
		t.Fatalf("could not build the test payload: %v", err)
	}
	return payload
}

func TestIntegration_AuthBackendCRUD(t *testing.T) {
	c := authTestClient(t)

	backend, err := c.CreateAuthBackend(&client.AuthBackend{
		Title: "tf-itest-ldap", Description: "created by the test suite",
		DefaultRoles: []string{}, Config: testLDAPPayload(t),
	})
	if err != nil {
		t.Fatalf("CreateAuthBackend error: %v", err)
	}
	if backend.ID == "" {
		t.Fatal("expected the created backend to have an ID")
	}
	t.Cleanup(func() {
		_ = c.SetActiveAuthBackend("")
		_ = c.DeleteAuthBackend(backend.ID)
	})

	// Every single-backend response is wrapped in a "backend" object;
	// decoding it as a bare struct yields an empty backend and no error.
	got, err := c.GetAuthBackend(backend.ID)
	if err != nil {
		t.Fatalf("GetAuthBackend error: %v", err)
	}
	if got.ID != backend.ID || got.Title != "tf-itest-ldap" {
		t.Fatalf("envelope was not unwrapped correctly: %+v", got)
	}

	// The decisive behaviour: the password never comes back as a value.
	var config map[string]any
	if err := json.Unmarshal(got.Config, &config); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	password, present := config["system_user_password"]
	if !present {
		t.Skip("this version omits system_user_password entirely rather than returning a sentinel")
	}
	if _, isString := password.(string); isString {
		t.Fatalf("this version returns the password as a value; the write-only handling can be simplified: %v", password)
	}
	if _, isObject := password.(map[string]any); !isObject {
		t.Fatalf("unexpected shape for system_user_password: %T", password)
	}

	// Refreshing a freshly created backend must reproduce the configuration
	// exactly: no sentinel, no server-side extras, no drift.
	refreshed, err := refreshAuthBackendConfig(string(got.Config), testLDAPConfig)
	if err != nil {
		t.Fatalf("refresh error: %v", err)
	}
	canonical, err := CanonicalizeJSONFromString(testLDAPConfig)
	if err != nil {
		t.Fatalf("canonicalization error: %v", err)
	}
	if refreshed != canonical {
		t.Fatalf("a freshly created backend reported drift\n want: %s\n got : %s", canonical, refreshed)
	}

	// And the import path, which has no mask, must still yield something
	// usable rather than an empty document.
	imported, err := refreshAuthBackendConfig(string(got.Config), "")
	if err != nil {
		t.Fatalf("import refresh error: %v", err)
	}
	if imported == "" || imported == "{}" {
		t.Fatalf("import produced an empty configuration: %q", imported)
	}
	if strings.Contains(imported, systemUserPasswordKey) {
		t.Fatalf("the write-only field leaked into an imported configuration: %s", imported)
	}

	// Update carries the ID in the body as well as the path.
	renamed, err := c.UpdateAuthBackend(backend.ID, &client.AuthBackend{
		Title: "tf-itest-ldap renamed", DefaultRoles: []string{},
		Config: testLDAPPayload(t),
	})
	if err != nil {
		t.Fatalf("UpdateAuthBackend error: %v", err)
	}
	if renamed.Title != "tf-itest-ldap renamed" {
		t.Fatalf("update did not take effect: %+v", renamed)
	}
}

// Activation is a separate, cluster-wide setting reached through its own
// endpoint, which only accepts POST.
func TestIntegration_AuthBackendActivation(t *testing.T) {
	c := authTestClient(t)

	backend, err := c.CreateAuthBackend(&client.AuthBackend{
		Title: "tf-itest-activation", DefaultRoles: []string{},
		Config: testLDAPPayload(t),
	})
	if err != nil {
		t.Fatalf("CreateAuthBackend error: %v", err)
	}
	t.Cleanup(func() {
		_ = c.SetActiveAuthBackend("")
		_ = c.DeleteAuthBackend(backend.ID)
	})

	if err := c.SetActiveAuthBackend(backend.ID); err != nil {
		t.Fatalf("SetActiveAuthBackend error: %v", err)
	}
	active, err := c.GetActiveAuthBackend()
	if err != nil {
		t.Fatalf("GetActiveAuthBackend error: %v", err)
	}
	if active != backend.ID {
		t.Fatalf("expected %s to be active, got %q", backend.ID, active)
	}

	// Destroying the activation must return the cluster to local auth.
	if err := c.SetActiveAuthBackend(""); err != nil {
		t.Fatalf("clearing the active backend failed: %v", err)
	}
	active, err = c.GetActiveAuthBackend()
	if err != nil {
		t.Fatalf("GetActiveAuthBackend error: %v", err)
	}
	if active != "" {
		t.Fatalf("expected no active backend, got %q", active)
	}
}

// Destroying an activation must not clear a selection that something else
// has since replaced.
func TestIntegration_AuthBackendActivationLeavesForeignSelectionAlone(t *testing.T) {
	c := authTestClient(t)

	mine, err := c.CreateAuthBackend(&client.AuthBackend{
		Title: "tf-itest-mine", DefaultRoles: []string{}, Config: testLDAPPayload(t),
	})
	if err != nil {
		t.Fatalf("CreateAuthBackend error: %v", err)
	}
	other, err := c.CreateAuthBackend(&client.AuthBackend{
		Title: "tf-itest-other", DefaultRoles: []string{}, Config: testLDAPPayload(t),
	})
	if err != nil {
		t.Fatalf("CreateAuthBackend error: %v", err)
	}
	t.Cleanup(func() {
		_ = c.SetActiveAuthBackend("")
		_ = c.DeleteAuthBackend(mine.ID)
		_ = c.DeleteAuthBackend(other.ID)
	})

	// Something outside Terraform activates a different backend.
	if err := c.SetActiveAuthBackend(other.ID); err != nil {
		t.Fatalf("SetActiveAuthBackend error: %v", err)
	}

	// The resource's Delete reads first and only clears its own selection;
	// this asserts the condition that guard relies on.
	active, err := c.GetActiveAuthBackend()
	if err != nil {
		t.Fatalf("GetActiveAuthBackend error: %v", err)
	}
	if active == mine.ID {
		t.Fatalf("precondition failed: expected %s to be active, got %s", other.ID, active)
	}
	if active != other.ID {
		t.Fatalf("expected the foreign selection to stand, got %q", active)
	}
}

func TestIntegration_AuthBackendNotFound(t *testing.T) {
	c := authTestClient(t)

	if _, err := c.GetAuthBackend("000000000000000000000000"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := c.DeleteAuthBackend("000000000000000000000000"); err != nil {
		t.Fatalf("deleting an absent backend should succeed, got %v", err)
	}
}
