package buildinput

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ginoh/goal-executor/internal/goalfile"
)

func TestSnapshotUsesFixedIncludedFiles(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Dockerfile", "FROM scratch\nCOPY main.txt /main.txt\n")
	write(".dockerignore", "*.log\nDockerfile\n.dockerignore\n")
	write("main.txt", "one")
	write("debug.log", "old")
	goal := filepath.Join(root, "goal.yaml")
	s1, err := Prepare(goal, ".", "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s1.Dir)
	if _, err := os.Stat(filepath.Join(s1.Dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	write("main.txt", "two")
	write("debug.log", "new")
	content, err := os.ReadFile(filepath.Join(s1.Dir, "main.txt"))
	if err != nil || string(content) != "one" {
		t.Fatalf("snapshot changed: %q %v", content, err)
	}
	s2, err := Prepare(goal, ".", "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s2.Dir)
	if s1.ID == s2.ID {
		t.Fatal("source change did not change input ID")
	}
	write("debug.log", "newer")
	s3, err := Prepare(goal, ".", "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s3.Dir)
	if s2.ID != s3.ID {
		t.Fatal("ignored file changed input ID")
	}
}

func TestDockerfileSpecificIgnoreAndNegation(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Dockerfile", "FROM scratch")
	write(".dockerignore", "main.txt\n")
	write("Dockerfile.dockerignore", "*.txt\n!main.txt\n")
	write("main.txt", "keep")
	write("other.txt", "exclude")
	s, err := Prepare(filepath.Join(root, "goal.yaml"), ".", "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s.Dir)
	if _, err := os.Stat(filepath.Join(s.Dir, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "other.txt")); !os.IsNotExist(err) {
		t.Fatalf("file was not excluded: %v", err)
	}
}

func TestExampleBuildInput(t *testing.T) {
	goalPath := filepath.Join("..", "..", "examples", "goal.yaml")
	d, err := goalfile.Load(goalPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Prepare(goalPath, d.Application.Build.Context, d.Application.Build.Dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s.Dir)
	for _, name := range []string{"cmd/demo-api/main.go", "examples/demo/Dockerfile", ".dockerignore"} {
		if _, err := os.Stat(filepath.Join(s.Dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("unexpected README in build input: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "examples", "goal.yaml")); !os.IsNotExist(err) {
		t.Fatalf("goal file must not affect app build input: %v", err)
	}
}

func TestInputIDIncludesSelectedDockerfile(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"Dockerfile.a": "FROM scratch\nLABEL variant=a\n",
		"Dockerfile.b": "FROM scratch\nLABEL variant=b\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	prepare := func(dockerfile string) Snapshot {
		t.Helper()
		s, err := Prepare(filepath.Join(root, "goal.yaml"), ".", dockerfile)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(s.Dir); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	a, b, again := prepare("Dockerfile.a"), prepare("Dockerfile.b"), prepare("./Dockerfile.a")
	// Both copies contain both Dockerfiles; only the selected build definition differs.
	if a.ID == b.ID {
		t.Fatal("switching Dockerfile reused the input ID")
	}
	if a.ID != again.ID {
		t.Fatal("equivalent Dockerfile paths changed the input ID")
	}
}
