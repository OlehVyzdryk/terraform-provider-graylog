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

// newTestClientForClusterConfig builds a client against a test server. The
// constructor probes /api/system for the version, so callers route that path
// separately from the endpoint under test.
func newTestClientForClusterConfig(base string) *Client {
	c := New(base, "dGVzdA==")
	c.MaxRetries = 0
	return c
}

// clusterConfigServer answers the version probe and delegates everything else
// to the supplied handler, recording the last observed request path and body.
func clusterConfigServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// detectVersion probes both /api/system and /system before any real
		// call; neither is the endpoint under test.
		if r.URL.Path == "/api/system" || r.URL.Path == "/system" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"7.0.0"}`))
			return
		}
		gotPath = r.URL.Path
		if b, err := io.ReadAll(r.Body); err == nil {
			gotBody = string(b)
		}
		handler(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, &gotPath, &gotBody
}

func TestGetClusterConfig_ReturnsDocument(t *testing.T) {
	ts, gotPath, _ := clusterConfigServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"interval":10}`))
	})

	c := newTestClientForClusterConfig(ts.URL)
	raw, err := c.GetClusterConfig("org.graylog2.users.UserConfiguration")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("returned document is not valid JSON: %v", err)
	}
	if doc["enabled"] != true {
		t.Fatalf("unexpected document: %s", string(raw))
	}
	if want := "/api/system/cluster_config/org.graylog2.users.UserConfiguration"; *gotPath != want {
		t.Fatalf("expected path %s, got %s", want, *gotPath)
	}
}

// Graylog answers 204 (not 404) for a class it knows about that has no stored
// document. Both must surface as ErrNotFound so the resource can drop out of
// state instead of erroring.
func TestGetClusterConfig_NoContentIsNotFound(t *testing.T) {
	ts, _, _ := clusterConfigServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	c := newTestClientForClusterConfig(ts.URL)
	if _, err := c.GetClusterConfig("org.graylog2.users.UserConfiguration"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetClusterConfig_NotFound(t *testing.T) {
	ts, _, _ := clusterConfigServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestClientForClusterConfig(ts.URL)
	if _, err := c.GetClusterConfig("org.graylog2.users.Unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// A literal "null" body is not a document either.
func TestGetClusterConfig_NullBodyIsNotFound(t *testing.T) {
	ts, _, _ := clusterConfigServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("null\n"))
	})

	c := newTestClientForClusterConfig(ts.URL)
	if _, err := c.GetClusterConfig("org.graylog2.users.UserConfiguration"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// The document must reach Graylog byte-for-byte: it is deserialized into a
// concrete class, so a re-encoded copy risks losing numeric precision.
func TestUpdateClusterConfig_SendsDocumentVerbatim(t *testing.T) {
	ts, gotPath, gotBody := clusterConfigServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"threshold":20000000}`))
	})

	c := newTestClientForClusterConfig(ts.URL)
	doc := json.RawMessage(`{"threshold":20000000}`)
	out, err := c.UpdateClusterConfig("org.graylog2.indexer.searches.SearchesClusterConfig", doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/api/system/cluster_config/org.graylog2.indexer.searches.SearchesClusterConfig"; *gotPath != want {
		t.Fatalf("expected path %s, got %s", want, *gotPath)
	}
	if strings.TrimSpace(*gotBody) != `{"threshold":20000000}` {
		t.Fatalf("request body was re-encoded: %s", *gotBody)
	}
	if strings.Contains(string(out), "e+") {
		t.Fatalf("response lost numeric fidelity: %s", string(out))
	}
}

// Some classes acknowledge a write with an empty body; the caller then keeps
// the document it sent rather than treating the write as a failure.
func TestUpdateClusterConfig_EmptyBody(t *testing.T) {
	ts, _, _ := clusterConfigServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	c := newTestClientForClusterConfig(ts.URL)
	out, err := c.UpdateClusterConfig("org.graylog2.users.UserConfiguration", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil document, got %s", string(out))
	}
}

func TestDeleteClusterConfig(t *testing.T) {
	ts, gotPath, _ := clusterConfigServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	c := newTestClientForClusterConfig(ts.URL)
	if err := c.DeleteClusterConfig("org.graylog2.users.UserConfiguration"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/api/system/cluster_config/org.graylog2.users.UserConfiguration"; *gotPath != want {
		t.Fatalf("expected path %s, got %s", want, *gotPath)
	}
}

// Deleting a document that is already gone is the desired end state, not an
// error — otherwise a partially applied destroy can never be completed.
func TestDeleteClusterConfig_NotFoundIsSuccess(t *testing.T) {
	ts, _, _ := clusterConfigServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestClientForClusterConfig(ts.URL)
	if err := c.DeleteClusterConfig("org.graylog2.users.UserConfiguration"); err != nil {
		t.Fatalf("expected nil error for missing document, got %v", err)
	}
}

// The class name lands in the request path, so it must be escaped rather than
// interpolated raw.
func TestClusterConfigPath_EscapesClass(t *testing.T) {
	got := clusterConfigPath("org.graylog2.Weird/Class Name")
	if strings.Contains(got, " ") {
		t.Fatalf("path contains an unescaped space: %s", got)
	}
	if strings.Count(got, "/") != 4 {
		t.Fatalf("class separator leaked into the path: %s", got)
	}
}
