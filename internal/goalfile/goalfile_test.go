package goalfile

import (
	"strings"
	"testing"
)

const valid = `environment: demo
application:
  build:
    context: ..
    dockerfile: examples/demo/Dockerfile
  port: 8080
checks:
  readiness:
    http: {path: /health, status: 200}
  verification:
    http: {path: /message, status: 200, bodyEquals: hello}
goal:
  state: verified
`

func TestDecode(t *testing.T) {
	d, err := Decode(strings.NewReader(valid))
	if err != nil || d.Environment != "demo" || d.Goal.State != "verified" || d.Checks.Verification.HTTP.BodyEquals == nil || *d.Checks.Verification.HTTP.BodyEquals != "hello" {
		t.Fatalf("document=%+v err=%v", d, err)
	}
	ready := strings.Replace(valid, "state: verified", "state: ready", 1)
	ready = strings.Replace(ready, "  verification:\n    http: {path: /message, status: 200, bodyEquals: hello}\n", "", 1)
	if _, err := Decode(strings.NewReader(ready)); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalid(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":         valid + "extra: value\n",
		"duplicate":       valid + "goal: {state: ready}\n",
		"nested unknown":  strings.Replace(valid, "port: 8080", "port: 8080\n  extra: value", 1),
		"alias":           strings.Replace(valid, "environment: demo", "environment: &env demo", 1) + "extra: *env\n",
		"type":            strings.Replace(valid, "port: 8080", "port: '8080'", 1),
		"missing":         strings.Replace(valid, "  port: 8080\n", "", 1),
		"no verification": strings.Replace(valid, "  verification:\n    http: {path: /message, status: 200, bodyEquals: hello}\n", "", 1),
		"multi document":  valid + "---\n" + valid,
		"bad state":       strings.Replace(valid, "state: verified", "state: done", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
