package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression test for #18: Graylog rejects "type" in create payloads and requires
// converters to be present as an array, including when it is empty.
func TestCreateInputExtractor_UsesExtractorTypeAndEmptyConvertersArray(t *testing.T) {
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/system/inputs/input-1/extractors" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"extractor_id": "extractor-1"})
	}))
	defer ts.Close()

	c := newInputTestClient(ts.URL)
	extractor := &Extractor{
		Title:           "Extract email",
		ExtractorType:   "grok",
		SourceField:     "message",
		ExtractorConfig: map[string]any{"grok_pattern": "%{EMAILADDRESS}"},
	}

	created, err := c.CreateInputExtractor("input-1", extractor)
	if err != nil {
		t.Fatalf("CreateInputExtractor: %v", err)
	}
	if created.ID != "extractor-1" {
		t.Fatalf("expected extractor id, got %+v", created)
	}
	if got := body["extractor_type"]; got != "grok" {
		t.Fatalf("expected extractor_type=grok, got %#v", got)
	}
	if _, exists := body["type"]; exists {
		t.Fatalf("create payload must not contain read-only type field: %#v", body)
	}
	converters, ok := body["converters"].([]any)
	if !ok {
		t.Fatalf("expected converters to be a JSON array, got %#v", body["converters"])
	}
	if len(converters) != 0 {
		t.Fatalf("expected an empty converters array, got %#v", converters)
	}
}
