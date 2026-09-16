package fdt

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

// testdata/small.dtb is testdata/small.dts compiled by dtc -@.
func small(t *testing.T) *Tree {
	t.Helper()
	b, err := os.ReadFile("testdata/small.dtb")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestParse(t *testing.T) {
	tree := small(t)
	if !reflect.DeepEqual(tree.Reserved, [][2]uint64{{0x40000000, 0x1000}}) {
		t.Errorf("reserved %v", tree.Reserved)
	}
	if v, _ := tree.Root.Prop("compatible"); !bytes.Equal(v, Strings("a,b", "c,d")) {
		t.Errorf("compatible %q", v)
	}
	spi := tree.Label("spi1")
	if spi == nil || spi.Name != "spi@5011000" {
		t.Fatalf("label spi1: %+v", spi)
	}
	if v, ok := spi.Prop("empty"); !ok || len(v) != 0 {
		t.Errorf("empty property %q %v", v, ok)
	}
	if got := tree.Phandle(tree.Label("pio")); got != 1 {
		t.Errorf("pio's phandle %d, want 1", got)
	}
	if v, _ := tree.Find("/chosen").Prop("ref"); !bytes.Equal(v, Cells(1, 7, 4, 0)) {
		t.Errorf("ref %x", v)
	}
	if tree.Find("/soc/nothing") != nil || tree.Find("soc") != nil || tree.Label("nothing") != nil {
		t.Error("found what is not there")
	}
}

// TestRoundTrip: what Bytes writes parses back to the same tree, edits included.
func TestRoundTrip(t *testing.T) {
	tree := small(t)
	again, err := Parse(tree.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, tree) {
		t.Fatal("the tree changed on the way through Bytes")
	}
	spi := tree.Label("spi1")
	spi.Set("status", Strings("okay"))
	panel := spi.Add("panel@0")
	panel.Set("reg", Cells(0))
	fresh := tree.Root.Add("backlight")
	if got := tree.Phandle(fresh); got != 3 {
		t.Errorf("a new node's phandle is %d, want 3", got)
	}
	if tree.Root.Add("backlight") != fresh {
		t.Error("Add made a second node of the same name")
	}
	edited, err := Parse(tree.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(edited, tree) {
		t.Fatal("the edited tree changed on the way through Bytes")
	}
	if v, _ := edited.Find("/soc/spi@5011000").Prop("status"); string(v) != "okay\x00" {
		t.Errorf("status %q", v)
	}
}

func TestParseRefusesGarbage(t *testing.T) {
	b, _ := os.ReadFile("testdata/small.dtb")
	for name, bad := range map[string][]byte{
		"empty":     nil,
		"not a dtb": []byte("0123456789012345678901234567890123456789"),
		"truncated": b[:len(b)/2],
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
