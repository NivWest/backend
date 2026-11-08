package api

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

type specInfo struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
}

func parseInfo(t *testing.T) specInfo {
	t.Helper()
	var doc specInfo
	if err := yaml.Unmarshal(OpenAPISpec, &doc); err != nil {
		t.Fatalf("api/openapi.yaml does not parse: %v", err)
	}
	return doc
}

func TestSpecIsOpenAPI31TradeEduAPI(t *testing.T) {
	doc := parseInfo(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1.") {
		t.Errorf("openapi = %q, want 3.1.x", doc.OpenAPI)
	}
	if doc.Info.Title != "TradeEdu API" {
		t.Errorf("info.title = %q, want %q", doc.Info.Title, "TradeEdu API")
	}
}

// info.version follows the VERSION file, which `agentflow.py release start` bumps on every release.
func TestSpecVersionMatchesVERSIONFile(t *testing.T) {
	raw, err := os.ReadFile("../VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if got := parseInfo(t).Info.Version; got != want {
		t.Errorf("api/openapi.yaml info.version = %q, but VERSION is %q: bump info.version with VERSION", got, want)
	}
}
