package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func lookupServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Client, *string, *string) {
	t.Helper()
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// detectVersion probes both paths before any real call.
		if r.URL.Path == "/api/system" || r.URL.Path == "/system" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"7.0.0"}`))
			return
		}
		// EscapedPath, not Path: the latter is already decoded, which would
		// hide a missing escape rather than reveal it.
		gotPath = r.URL.EscapedPath()
		if b, err := io.ReadAll(r.Body); err == nil {
			gotBody = string(b)
		}
		handler(w, r)
	}))
	t.Cleanup(ts.Close)

	c := New(ts.URL, "dGVzdA==")
	c.MaxRetries = 0
	return c, &gotPath, &gotBody
}

// The tables endpoint answers with a paginated envelope even when addressed
// by ID. Decoding it as a bare object yields a zero-valued LookupTable and no
// error, which is a silent data-loss bug rather than a visible failure.
func TestGetLookupTable_UnwrapsPaginatedEnvelope(t *testing.T) {
	c, gotPath, _ := lookupServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"total": 1, "page": 1, "per_page": 50, "count": 1, "query": null,
			"caches": {}, "data_adapters": {},
			"lookup_tables": [{
				"id": "abc123", "name": "geoip-city-lookup", "title": "GeoIP City Lookup",
				"description": "d", "cache_id": "cache1", "data_adapter_id": "adapter1",
				"default_single_value": "", "default_single_value_type": "NULL",
				"default_multi_value": "", "default_multi_value_type": "NULL"
			}]
		}`))
	})

	table, err := c.GetLookupTable("geoip-city-lookup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if table.ID != "abc123" {
		t.Fatalf("envelope not unwrapped, got %+v", table)
	}
	if table.CacheID != "cache1" || table.DataAdapterID != "adapter1" {
		t.Fatalf("bindings lost during unwrap: %+v", table)
	}
	if want := "/api/system/lookup/tables/geoip-city-lookup"; *gotPath != want {
		t.Fatalf("expected path %s, got %s", want, *gotPath)
	}
}

// An envelope that resolves to nothing is a missing resource, not an empty
// object silently written into state.
func TestGetLookupTable_EmptyEnvelopeIsNotFound(t *testing.T) {
	c, _, _ := lookupServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"total":0,"lookup_tables":[]}`))
	})

	if _, err := c.GetLookupTable("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// Graylog rejects an update whose body omits the id with "URL parameter does
// not match parameter in request body", so the client must inject it even
// when the caller leaves it blank.
func TestUpdateLookup_PutsIDInTheBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(c *Client) error
	}{
		{"cache", func(c *Client) error {
			_, err := c.UpdateLookupCache("id42", &LookupCache{Name: "n", Title: "t", Config: json.RawMessage(`{"type":"guava_cache"}`)})
			return err
		}},
		{"adapter", func(c *Client) error {
			_, err := c.UpdateLookupAdapter("id42", &LookupAdapter{Name: "n", Title: "t", Config: json.RawMessage(`{"type":"csvfile"}`)})
			return err
		}},
		{"table", func(c *Client) error {
			_, err := c.UpdateLookupTable("id42", &LookupTable{Name: "n", Title: "t"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, gotPath, gotBody := lookupServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Errorf("expected PUT, got %s", r.Method)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"id42","name":"n","title":"t"}`))
			})

			if err := tc.call(c); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(*gotBody), &body); err != nil {
				t.Fatalf("request body is not valid JSON: %v", err)
			}
			if body["id"] != "id42" {
				t.Fatalf("id missing from the request body: %s", *gotBody)
			}
			if !strings.HasSuffix(*gotPath, "/id42") {
				t.Fatalf("id missing from the request path: %s", *gotPath)
			}
		})
	}
}

// The configuration must reach Graylog exactly as written: re-encoding
// through map[string]any would turn 20000000 into 2e+07.
func TestCreateLookupCache_SendsConfigVerbatim(t *testing.T) {
	c, _, gotBody := lookupServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","name":"n","title":"t","config":{"max_size":20000000}}`))
	})

	created, err := c.CreateLookupCache(&LookupCache{
		Name: "n", Title: "t",
		Config: json.RawMessage(`{"type":"guava_cache","max_size":20000000}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(*gotBody, `"max_size":20000000`) {
		t.Fatalf("config was re-encoded on the way out: %s", *gotBody)
	}
	if strings.Contains(string(created.Config), "e+") {
		t.Fatalf("config lost numeric fidelity on the way in: %s", string(created.Config))
	}
}

func TestDeleteLookup_NotFoundIsSuccess(t *testing.T) {
	c, _, _ := lookupServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if err := c.DeleteLookupCache("gone"); err != nil {
		t.Fatalf("cache: expected nil, got %v", err)
	}
	if err := c.DeleteLookupAdapter("gone"); err != nil {
		t.Fatalf("adapter: expected nil, got %v", err)
	}
	if err := c.DeleteLookupTable("gone"); err != nil {
		t.Fatalf("table: expected nil, got %v", err)
	}
}

// A name can contain characters that would otherwise alter the request path.
func TestLookupPaths_EscapeTheIdentifier(t *testing.T) {
	c, gotPath, _ := lookupServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","name":"n","title":"t"}`))
	})

	_, _ = c.GetLookupCache("weird/name")
	if strings.Contains(*gotPath, "weird/name") {
		t.Fatalf("identifier separator leaked into the path: %s", *gotPath)
	}
}
