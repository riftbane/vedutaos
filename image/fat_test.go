package image

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCardDisk: a card folder becomes a FAT disk QEMU can write to, and the saves written
// on it come back into the folder.
func TestCardDisk(t *testing.T) {
	if err := Check(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	cardDir := filepath.Join(dir, "card")
	os.MkdirAll(filepath.Join(cardDir, "games", "cave"), 0o755)
	os.WriteFile(filepath.Join(cardDir, "games", "cave", "main.lua"), []byte("-- cave"), 0o644)
	img := filepath.Join(dir, "card.img")
	if err := CardDisk(img, cardDir, "VEDUTA"); err != nil {
		t.Fatal(err)
	}
	if err := CopyFromCardDisk(img, "saves", cardDir); err != nil {
		t.Fatalf("a disk without saves: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cardDir, "saves")); err == nil {
		t.Fatal("saves appeared from nowhere")
	}
	// What a game writes on the console.
	written := filepath.Join(dir, "written", "saves", "cave")
	os.MkdirAll(written, 0o755)
	os.WriteFile(filepath.Join(written, "slot1.json"), []byte(`{"gold":3}`), 0o644)
	if err := mtools(img, "mcopy", "-s", "-Q", filepath.Join(dir, "written", "saves"), "::/"); err != nil {
		t.Fatal(err)
	}
	if err := CopyFromCardDisk(img, "saves", cardDir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(cardDir, "saves", "cave", "slot1.json")); err != nil || string(b) != `{"gold":3}` {
		t.Fatalf("save copied back: %q %v", b, err)
	}
	// Again, over what the folder has.
	os.WriteFile(filepath.Join(written, "slot1.json"), []byte(`{"gold":4}`), 0o644)
	mtools(img, "mcopy", "-s", "-o", "-Q", filepath.Join(dir, "written", "saves"), "::/")
	if err := CopyFromCardDisk(img, "saves", cardDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cardDir, "saves", "cave", "slot1.json")); string(b) != `{"gold":4}` {
		t.Fatalf("second copy: %q", b)
	}
}
