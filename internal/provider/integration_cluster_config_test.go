//go:build integration

package provider

import (
	"encoding/base64"
	"errors"
	"os"
	"testing"

	"github.com/Ultrafenrir/terraform-provider-graylog/internal/client"
)

func clusterConfigTestClient(t *testing.T) *client.Client {
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

// The resource stores the practitioner's document in state and never the
// server echo, which is only sound if Graylog persists cluster configuration
// verbatim. This asserts exactly that invariant on every matrix version: if
// some version ever starts enriching stored documents with materialized
// defaults, this fails and the Read strategy has to change.
func TestIntegration_ClusterConfigRoundTripIsVerbatim(t *testing.T) {
	c := clusterConfigTestClient(t)

	var class string
	var original []byte
	for _, candidate := range clusterConfigProbeClasses {
		doc, err := c.GetClusterConfig(candidate)
		if err != nil {
			continue
		}
		class, original = candidate, doc
		break
	}
	if class == "" {
		t.Skipf("none of the probe classes returned a document: %v", clusterConfigProbeClasses)
	}
	t.Logf("using class %s", class)

	canonicalOriginal, err := CanonicalizeJSONFromString(string(original))
	if err != nil {
		t.Fatalf("server document is not valid JSON: %v", err)
	}

	// Writing the document back unchanged must be a no-op, byte-for-byte
	// after canonicalization.
	if _, err := c.UpdateClusterConfig(class, original); err != nil {
		t.Fatalf("UpdateClusterConfig error: %v", err)
	}

	readBack, err := c.GetClusterConfig(class)
	if err != nil {
		t.Fatalf("GetClusterConfig after write error: %v", err)
	}
	canonicalReadBack, err := CanonicalizeJSONFromString(string(readBack))
	if err != nil {
		t.Fatalf("document read back is not valid JSON: %v", err)
	}

	if canonicalReadBack != canonicalOriginal {
		t.Fatalf("cluster configuration was not stored verbatim for %s\n before: %s\n  after: %s",
			class, canonicalOriginal, canonicalReadBack)
	}
}

// Graylog answers 204 for a known class with nothing stored and 404 for a
// class it cannot resolve. The client must fold both into ErrNotFound so the
// resource drops out of state rather than erroring.
func TestIntegration_ClusterConfigUnknownClassIsNotFound(t *testing.T) {
	c := clusterConfigTestClient(t)

	// Inside the default safe_classes prefixes, so this reaches class
	// resolution instead of being rejected by the allowlist.
	_, err := c.GetClusterConfig(clusterConfigAbsentClass)
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unresolvable class, got %v", err)
	}
}

// Destroy must converge even when the document is already gone, otherwise a
// partially applied destroy can never be completed.
func TestIntegration_ClusterConfigDeleteAbsentIsIdempotent(t *testing.T) {
	c := clusterConfigTestClient(t)

	if err := c.DeleteClusterConfig(clusterConfigAbsentClass); err != nil {
		t.Fatalf("deleting an absent document should succeed, got %v", err)
	}
}

// A document that omits fields the target class requires must fail loudly at
// apply time rather than being silently half-applied.
func TestIntegration_ClusterConfigRejectsIncompleteDocument(t *testing.T) {
	c := clusterConfigTestClient(t)

	var class string
	for _, candidate := range clusterConfigProbeClasses {
		if _, err := c.GetClusterConfig(candidate); err == nil {
			class = candidate
			break
		}
	}
	if class == "" {
		t.Skipf("none of the probe classes returned a document: %v", clusterConfigProbeClasses)
	}

	if _, err := c.UpdateClusterConfig(class, []byte(`{"tf_provider_not_a_real_field":true}`)); err == nil {
		t.Fatalf("expected %s to reject a document missing its required fields", class)
	}
}
