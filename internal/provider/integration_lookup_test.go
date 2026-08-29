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

func lookupTestClient(t *testing.T) *client.Client {
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

const (
	testCacheConfig   = `{"type":"guava_cache","max_size":1000,"expire_after_access":60,"expire_after_access_unit":"SECONDS","expire_after_write":0}`
	testAdapterConfig = `{"type":"csvfile","path":"/etc/graylog/server/tf-itest.csv","separator":",","quotechar":"\"","key_column":"k","value_column":"v","check_interval":60,"case_insensitive_lookup":false}`
)

// Exercises the whole stack the way a GeoIP or CSV enrichment setup does:
// cache, adapter, then a table binding the two.
func TestIntegration_LookupStackCRUD(t *testing.T) {
	c := lookupTestClient(t)

	cache, err := c.CreateLookupCache(&client.LookupCache{
		Name: "tf-itest-cache", Title: "TF itest cache", Description: "created by the test suite",
		Config: json.RawMessage(testCacheConfig),
	})
	if err != nil {
		t.Fatalf("CreateLookupCache error: %v", err)
	}
	if cache.ID == "" {
		t.Fatal("expected the created cache to have an ID")
	}
	t.Cleanup(func() { _ = c.DeleteLookupCache(cache.ID) })

	adapter, err := c.CreateLookupAdapter(&client.LookupAdapter{
		Name: "tf-itest-adapter", Title: "TF itest adapter", Description: "created by the test suite",
		Config: json.RawMessage(testAdapterConfig),
	})
	if err != nil {
		t.Fatalf("CreateLookupAdapter error: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteLookupAdapter(adapter.ID) })

	table, err := c.CreateLookupTable(&client.LookupTable{
		Name: "tf-itest-table", Title: "TF itest table", Description: "created by the test suite",
		CacheID: cache.ID, DataAdapterID: adapter.ID,
		DefaultSingleValueType: "NULL", DefaultMultiValueType: "NULL",
	})
	if err != nil {
		t.Fatalf("CreateLookupTable error: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteLookupTable(table.ID) })

	// The tables endpoint answers with a paginated envelope even when
	// addressed by ID; decoding it as a bare object would yield an empty
	// struct rather than an error.
	got, err := c.GetLookupTable(table.ID)
	if err != nil {
		t.Fatalf("GetLookupTable by id error: %v", err)
	}
	if got.ID != table.ID || got.Name != "tf-itest-table" {
		t.Fatalf("envelope was not unwrapped correctly: %+v", got)
	}
	if got.CacheID != cache.ID || got.DataAdapterID != adapter.ID {
		t.Fatalf("table bindings not round-tripped: %+v", got)
	}

	// Every endpoint also resolves a name, which is what makes
	// `terraform import` by name work.
	byName, err := c.GetLookupTable("tf-itest-table")
	if err != nil {
		t.Fatalf("GetLookupTable by name error: %v", err)
	}
	if byName.ID != table.ID {
		t.Fatalf("name lookup resolved to a different object: %s != %s", byName.ID, table.ID)
	}

	// Update carries the ID in the body as well as the path; Graylog rejects
	// a request that omits it.
	updated, err := c.UpdateLookupTable(table.ID, &client.LookupTable{
		Name: "tf-itest-table", Title: "TF itest table renamed", Description: "updated",
		CacheID: cache.ID, DataAdapterID: adapter.ID,
		DefaultSingleValueType: "NULL", DefaultMultiValueType: "NULL",
	})
	if err != nil {
		t.Fatalf("UpdateLookupTable error: %v", err)
	}
	if updated.Title != "TF itest table renamed" {
		t.Fatalf("update did not take effect: %+v", updated)
	}
}

// Graylog materializes the defaults of the cache type when it stores a
// configuration, so the echo carries keys the practitioner never wrote.
// Projection is what keeps that from surfacing as a permanent diff.
func TestIntegration_LookupCacheEchoIsEnriched(t *testing.T) {
	c := lookupTestClient(t)

	cache, err := c.CreateLookupCache(&client.LookupCache{
		Name: "tf-itest-echo", Title: "TF itest echo",
		Config: json.RawMessage(testCacheConfig),
	})
	if err != nil {
		t.Fatalf("CreateLookupCache error: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteLookupCache(cache.ID) })

	read, err := c.GetLookupCache(cache.ID)
	if err != nil {
		t.Fatalf("GetLookupCache error: %v", err)
	}

	var sent, echoed map[string]any
	if err := json.Unmarshal([]byte(testCacheConfig), &sent); err != nil {
		t.Fatalf("test fixture is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(read.Config, &echoed); err != nil {
		t.Fatalf("server config is not valid JSON: %v", err)
	}
	if len(echoed) <= len(sent) {
		t.Skipf("this version does not enrich the echo (%d keys sent, %d returned); projection is a no-op here",
			len(sent), len(echoed))
	}

	// The projected echo must equal the practitioner's own document, which is
	// exactly the condition for an empty plan.
	projected, err := ProjectAndCanonicalizeJSON(string(read.Config), testCacheConfig)
	if err != nil {
		t.Fatalf("projection error: %v", err)
	}
	canonicalSent, err := CanonicalizeJSONFromString(testCacheConfig)
	if err != nil {
		t.Fatalf("canonicalization error: %v", err)
	}
	if projected != canonicalSent {
		t.Fatalf("projected echo differs from the submitted configuration\n sent: %s\n got : %s",
			canonicalSent, projected)
	}
}

// Graylog guards referential integrity server-side, so the provider does not
// need a pre-delete usage scan; Terraform's dependency graph plus this error
// are enough.
func TestIntegration_LookupCacheInUseCannotBeDeleted(t *testing.T) {
	c := lookupTestClient(t)

	cache, err := c.CreateLookupCache(&client.LookupCache{
		Name: "tf-itest-inuse-cache", Title: "TF itest in-use cache",
		Config: json.RawMessage(testCacheConfig),
	})
	if err != nil {
		t.Fatalf("CreateLookupCache error: %v", err)
	}
	adapter, err := c.CreateLookupAdapter(&client.LookupAdapter{
		Name: "tf-itest-inuse-adapter", Title: "TF itest in-use adapter",
		Config: json.RawMessage(testAdapterConfig),
	})
	if err != nil {
		t.Fatalf("CreateLookupAdapter error: %v", err)
	}
	table, err := c.CreateLookupTable(&client.LookupTable{
		Name: "tf-itest-inuse-table", Title: "TF itest in-use table",
		CacheID: cache.ID, DataAdapterID: adapter.ID,
		DefaultSingleValueType: "NULL", DefaultMultiValueType: "NULL",
	})
	if err != nil {
		t.Fatalf("CreateLookupTable error: %v", err)
	}

	t.Cleanup(func() {
		_ = c.DeleteLookupTable(table.ID)
		_ = c.DeleteLookupAdapter(adapter.ID)
		_ = c.DeleteLookupCache(cache.ID)
	})

	err = c.DeleteLookupCache(cache.ID)
	if err == nil {
		t.Fatal("expected Graylog to refuse deleting a cache that a table still references")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "in use") {
		t.Logf("refusal message differs on this version: %v", err)
	}
}

func TestIntegration_LookupNotFound(t *testing.T) {
	c := lookupTestClient(t)

	if _, err := c.GetLookupCache("tf-itest-absent"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("cache: expected ErrNotFound, got %v", err)
	}
	if _, err := c.GetLookupAdapter("tf-itest-absent"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("adapter: expected ErrNotFound, got %v", err)
	}
	if _, err := c.GetLookupTable("tf-itest-absent"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("table: expected ErrNotFound, got %v", err)
	}

	// Deleting something already gone is the desired end state.
	if err := c.DeleteLookupCache("tf-itest-absent"); err != nil {
		t.Fatalf("delete absent cache should succeed, got %v", err)
	}
}
