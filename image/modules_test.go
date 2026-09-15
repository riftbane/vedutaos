package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeModuleTree(t *testing.T) *modules {
	t.Helper()
	root := t.TempDir()
	files := fakeKernel(t, "6.1.0-test", map[string][]string{
		"panel_mipi_dbi": {"drm_kms_helper", "drm", "drm_mipi_dbi", "backlight"},
		"drm_mipi_dbi":   {"drm_kms_helper", "drm", "backlight"},
		"drm_kms_helper": {"drm", "backlight"},
		"drm":            {"backlight", "quirks"},
		"backlight":      nil,
		"quirks":         nil,
		"spi_bcm2835":    nil,
		"virtio_gpu":     {"drm", "virtio"},
	}, []string{"vfat", "evdev", "virtio"})
	for name, b := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	m, err := loadModules(root)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolveOrdersDependenciesFirst(t *testing.T) {
	m := fakeModuleTree(t)
	order, err := m.resolve([]string{"vfat", "panel-mipi-dbi", "spi-bcm2835", "evdev"})
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	for _, n := range []string{"panel_mipi_dbi", "drm", "backlight", "quirks", "spi_bcm2835"} {
		if _, ok := pos[n]; !ok {
			t.Errorf("%s missing from %v", n, order)
		}
	}
	for _, n := range []string{"vfat", "evdev", "virtio_gpu"} {
		if _, ok := pos[n]; ok {
			t.Errorf("%s should not be in %v", n, order)
		}
	}
	after := func(a, b string) {
		if pos[a] < pos[b] {
			t.Errorf("%s loads before %s: %v", a, b, order)
		}
	}
	after("panel_mipi_dbi", "drm_mipi_dbi")
	after("drm_mipi_dbi", "drm_kms_helper")
	after("drm_kms_helper", "drm")
	after("drm", "backlight")
	after("drm", "quirks")
	if m.release != "6.1.0-test" {
		t.Errorf("release %q", m.release)
	}
	if p := m.plainPath("panel_mipi_dbi"); !strings.HasPrefix(p, "/lib/modules/6.1.0-test/kernel/drivers/panel") || !strings.HasSuffix(p, ".ko") {
		t.Errorf("plain path %q", p)
	}
}

func TestResolveRefusesTheUnknown(t *testing.T) {
	m := fakeModuleTree(t)
	_, err := m.resolve([]string{"panel-mipi-dbi", "nothing_here"})
	if err == nil || !strings.Contains(err.Error(), "nothing_here") {
		t.Errorf("%v", err)
	}
	// virtio_gpu needs virtio, which is built in: fine.
	if _, err := m.resolve([]string{"virtio-gpu"}); err != nil {
		t.Error(err)
	}
}
