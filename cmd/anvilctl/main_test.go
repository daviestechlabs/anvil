package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineValidationAndHeldPreparation(t *testing.T) {
	var out bytes.Buffer
	err := execute([]string{"validate", "--recipe", "../../examples/recipe.yaml", "--template", "../../examples/workflow-template.yaml", "--run", "../../examples/run.yaml"}, &out)
	if err != nil || !strings.Contains(out.String(), `"valid":true`) {
		t.Fatalf("validation: %v %s", err, out.String())
	}
	out.Reset()
	err = execute([]string{"prepare", "--recipe", "../../examples/recipe.yaml", "--template", "../../examples/workflow-template.yaml", "--namespace", "portable"}, &out)
	if err != nil || !strings.Contains(out.String(), "enabled: false") || strings.Contains(out.String(), "namespace: anvil-training") {
		t.Fatalf("prepare: %v %s", err, out.String())
	}
}
func TestValidationRejectsChangedTemplate(t *testing.T) {
	b, err := os.ReadFile("../../examples/workflow-template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "template.yaml")
	_ = os.WriteFile(path, bytes.ReplaceAll(b, []byte("weight -= 0.1"), []byte("weight -= 0.2")), 0600)
	err = execute([]string{"validate", "--recipe", "../../examples/recipe.yaml", "--template", path, "--run", "../../examples/run.yaml"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "content hash differs") {
		t.Fatal("accepted modified template", err)
	}
}
