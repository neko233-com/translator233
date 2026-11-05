package update

import (
	"os"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.1", "1.0.2", -1},
		{"v1.0.2", "1.0.2", 0},
		{"1.2", "1.10", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-beta.1", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetNames(t *testing.T) {
	if got := AssetName("", "windows", "amd64"); got != "translator233-windows-amd64.exe" {
		t.Errorf("cli asset = %q", got)
	}
	if got := AssetName("gui", "darwin", "arm64"); got != "translator233-gui-darwin-arm64" {
		t.Errorf("gui asset = %q", got)
	}
	if got := MSIName("windows", "arm64"); got != "translator233-windows-arm64.msi" {
		t.Errorf("msi asset = %q", got)
	}
	if got := MSIName("darwin", "arm64"); got != "" {
		t.Errorf("darwin has no MSI, got %q", got)
	}
}

func TestPreparePicksInstaller(t *testing.T) {
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: "translator233-windows-amd64.exe"},
			{Name: "translator233-windows-amd64.msi"},
		},
	}
	// Prepare uses the host platform; run it only where the names apply.
	plan, err := Prepare("1.0.0", rel)
	if err != nil {
		t.Skipf("host has no matching asset: %v", err)
	}
	if plan == nil {
		t.Fatal("expected an update")
	}
	if plan.Latest != "9.9.9" {
		t.Fatalf("latest = %q", plan.Latest)
	}
}

func TestPrepareUpToDate(t *testing.T) {
	rel := &Release{Tag: "v1.0.0"}
	plan, err := Prepare("1.0.1", rel)
	if err != nil {
		t.Fatal(err)
	}
	if plan != nil {
		t.Fatalf("no update expected, got %+v", plan)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/file.bin"
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := VerifySHA256(path, want); err != nil {
		t.Fatalf("digest should match: %v", err)
	}
	if err := VerifySHA256(path, "deadbeef"); err == nil {
		t.Fatal("expected a mismatch error")
	}
}

