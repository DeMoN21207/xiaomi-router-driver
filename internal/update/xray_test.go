package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBundleDeclaredXrayRequiredAndExecutable(t *testing.T) {
	root := t.TempDir()
	writeValidBundle(t, root, "linux", "arm64")
	path := filepath.Join(root, "bundle-info.txt")
	info, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(info, []byte("\nxray_path=bin/xray\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundle(root); err == nil {
		t.Fatal("accepted bundle with missing declared Xray")
	}
	writeRuntimeFile(t, root, "bin/xray", "xray")
	if err := os.Chmod(filepath.Join(root, "bin/xray"), 0600); err != nil {
		t.Fatal(err)
	}
	bundle, err := ValidateBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	app := t.TempDir()
	if _, err := installBundle(app, filepath.Join(app, "data"), bundle, time.Now()); err != nil {
		t.Fatal(err)
	}
	installed, err := os.Stat(filepath.Join(app, "bin/xray"))
	if err != nil {
		t.Fatal(err)
	}
	if installed.Mode().Perm()&0111 == 0 {
		t.Fatal("installed Xray is not executable")
	}
}
