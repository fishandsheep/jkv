package main

import (
	"testing"

	"jkv/internal/catalog"
)

func TestSelectReleaseAlias(t *testing.T) {
	releases := []catalog.Release{
		{Candidate: "java", Version: "21.0.2+1-tem", Vendor: "temurin"},
		{Candidate: "java", Version: "21.0.3.0.1-dragonwell", Vendor: "dragonwell"},
	}
	for want, version := range map[string]string{
		"": "21.0.2+1-tem", "21-tem": "21.0.2+1-tem", "21-dragonwell": "21.0.3.0.1-dragonwell",
	} {
		got, err := selectRelease(releases, want)
		if err != nil || got.Version != version {
			t.Fatalf("%q: got=%v err=%v", want, got, err)
		}
	}
}
