package http

import (
	"regexp"
	"testing"
)

func TestEmbeddedFrontendIndexReferencesEmbeddedAssets(t *testing.T) {
	index, err := frontendFiles.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	assets := regexp.MustCompile(`/assets/([^"' ]+)`).FindAllSubmatch(index, -1)
	if len(assets) < 2 {
		t.Fatalf("frontend index does not reference its built assets: %s", index)
	}
	for _, match := range assets {
		path := "frontend/dist/assets/" + string(match[1])
		if _, err := frontendFiles.ReadFile(path); err != nil {
			t.Fatalf("frontend index references missing embedded asset %s: %v", path, err)
		}
	}
}
