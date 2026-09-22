package goalfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = "environment: demo\napiVersion: v1\ndataset: sample-v1\nrequireIntegrationTest: true\n"

func TestDecode(t *testing.T) {
	for _, flag := range []string{"true", "false"} {
		goal, err := Decode(strings.NewReader("# a comment\n" + strings.Replace(valid, "true", flag, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if goal.Environment != "demo" || goal.APIVersion != "v1" || goal.Dataset != "sample-v1" || goal.RequireIntegrationTest != (flag == "true") {
			t.Fatalf("unexpected goal: %+v", goal)
		}
	}
}

func TestRejectMalformedGoals(t *testing.T) {
	cases := map[string]string{
		"empty": "", "null": "null", "list": "[one, two]", "syntax": "environment: [",
		"unknown":                   valid + "extra: value\n",
		"duplicate":                 valid + "apiVersion: v2\n",
		"missing string":            strings.Replace(valid, "dataset: sample-v1\n", "", 1),
		"missing bool":              strings.Replace(valid, "requireIntegrationTest: true\n", "", 1),
		"numeric string":            strings.Replace(valid, "v1", "12", 1),
		"empty string":              strings.Replace(valid, "v1", "' '", 1),
		"null string":               strings.Replace(valid, "v1", "null", 1),
		"quoted bool":               strings.Replace(valid, "true", "'true'", 1),
		"legacy bool":               strings.Replace(valid, "true", "yes", 1),
		"null bool":                 strings.Replace(valid, "true", "null", 1),
		"mapping value":             strings.Replace(valid, "v1", "{nested: v1}", 1),
		"second document":           valid + "---\n" + valid,
		"empty second document":     valid + "---\n",
		"invalid trailing document": valid + "---\n[",
		"alias":                     strings.Replace(strings.Replace(valid, "demo", "&name demo", 1), "v1", "*name", 1),
		"merge":                     "base: &base {environment: demo}\n<<: *base\n" + valid,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("accepted invalid goal")
			}
		})
	}
	for _, field := range []string{"environment: demo\n", "apiVersion: v1\n", "dataset: sample-v1\n", "requireIntegrationTest: true\n"} {
		if _, err := Decode(strings.NewReader(strings.Replace(valid, field, "", 1))); err == nil {
			t.Fatalf("accepted missing %s", field)
		}
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.yaml")
	if _, err := Load(path); err == nil {
		t.Fatal("accepted missing file")
	}
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(valid, "v1", "v2", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	goal, err := Load(path)
	if err != nil || goal.APIVersion != "v2" {
		t.Fatalf("did not reload: %+v %v", goal, err)
	}
}
