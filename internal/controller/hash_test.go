package controller

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCanonicalHashVectors(t *testing.T) {
	data, err := os.ReadFile("../../testdata/content-hash-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Value  any    `json:"value"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		hash, err := Hash(vector.Value)
		if err != nil {
			t.Fatal(err)
		}
		if hash != vector.SHA256 {
			t.Fatalf("canonical hash differs: %s", hash)
		}
	}
	structHash, _ := Hash(struct {
		Z bool   `json:"z"`
		A string `json:"a"`
	}{Z: true, A: "first"})
	mapHash, _ := Hash(map[string]any{"a": "first", "z": true})
	if structHash != mapHash {
		t.Fatal("hash depends on struct field ordering")
	}
}

func TestVolumeMountFalseSerializationPreservesReadOnlyBoundary(t *testing.T) {
	spec := map[string]any{"templates": []any{map[string]any{"script": map[string]any{"volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data", "readOnly": false}}}}}}
	omitted := map[string]any{"templates": []any{map[string]any{"script": map[string]any{"volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data"}}}}}}
	readonly := map[string]any{"templates": []any{map[string]any{"script": map[string]any{"volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data", "readOnly": true}}}}}}
	a, _ := workflowSpecHash(spec)
	b, _ := workflowSpecHash(omitted)
	c, _ := workflowSpecHash(readonly)
	if a != b || a == c {
		t.Fatal("volume-mount defaults crossed the read-only boundary")
	}
}

func TestWorkflowHashUpgradePreservesOriginalAlgorithm(t *testing.T) {
	spec := map[string]any{"templates": []any{map[string]any{"script": map[string]any{"volumeMounts": []any{map[string]any{"name": "data", "readOnly": false}}}}}}
	legacy, _ := workflowSpecHashVersion(spec, "1")
	expected, _ := Hash(spec)
	current, _ := workflowSpecHashVersion(spec, "2")
	if legacy != expected || legacy == current {
		t.Fatal("legacy hash algorithm changed")
	}
}
