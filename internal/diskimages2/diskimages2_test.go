package diskimages2_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/diskimages2"
)

func TestFrameworkAvailable(t *testing.T) {
	if !diskimages2.Available() {
		t.Skip("DiskImages2 framework not available on this system")
	}
	t.Log("DiskImages2 framework loaded successfully")
}

func TestCreateASIFAndAttachDetach(t *testing.T) {
	if !diskimages2.Available() {
		t.Skip("DiskImages2 framework not available")
	}

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.sparseimage")

	// Create a 100MB ASIF image.
	const size = 100 * 1024 * 1024
	if err := diskimages2.CreateASIF(imgPath, size); err != nil {
		t.Fatalf("CreateASIF: %v", err)
	}

	fi, err := os.Stat(imgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	t.Logf("Created ASIF image: %s (%d bytes on disk, %d virtual)", imgPath, fi.Size(), size)

	// Verify ASIF is sparse (on-disk size should be much smaller than virtual).
	if fi.Size() > size/2 {
		t.Errorf("ASIF image not sparse: on-disk %d >= half of virtual %d", fi.Size(), size)
	}

	// Attach.
	handle, err := diskimages2.Attach(imgPath, diskimages2.AttachOptions{AutoMount: false})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Logf("Attached: BSDName=%s RegEntryID=%d", handle.BSDName, handle.RegEntryID)

	if handle.BSDName == "" {
		t.Error("BSDName is empty")
	}

	// Detach.
	if err := diskimages2.DetachHandle(handle); err != nil {
		t.Fatalf("DetachHandle: %v", err)
	}
	t.Log("Detached successfully")
}

func TestRetrieveInfo(t *testing.T) {
	if !diskimages2.Available() {
		t.Skip("DiskImages2 framework not available")
	}

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "info-test.sparseimage")

	const size = 50 * 1024 * 1024
	if err := diskimages2.CreateASIF(imgPath, size); err != nil {
		t.Fatalf("CreateASIF: %v", err)
	}

	info, err := diskimages2.RetrieveInfo(imgPath)
	if err != nil {
		t.Fatalf("RetrieveInfo: %v", err)
	}

	t.Logf("ImageInfo keys: %d", len(info.Raw))
	for k, v := range info.Raw {
		t.Logf("  %s = %s", k, v)
	}
}

func TestListAttached(t *testing.T) {
	if !diskimages2.Available() {
		t.Skip("DiskImages2 framework not available")
	}

	devices, err := diskimages2.ListAttached()
	if err != nil {
		t.Fatalf("ListAttached: %v", err)
	}
	t.Logf("Currently attached: %d devices", len(devices))
	for _, d := range devices {
		t.Logf("  %s → %s (%d bytes)", d.BSDName, d.ImageURL, d.MediaSize)
	}
}

func TestResize(t *testing.T) {
	if !diskimages2.Available() {
		t.Skip("DiskImages2 framework not available")
	}

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "resize-test.sparseimage")

	const initialSize = 50 * 1024 * 1024
	if err := diskimages2.CreateASIF(imgPath, initialSize); err != nil {
		t.Fatalf("CreateASIF: %v", err)
	}

	const newSize = 200 * 1024 * 1024
	if err := diskimages2.Resize(imgPath, newSize); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	t.Logf("Resized from %d to %d bytes", initialSize, newSize)
}

// Benchmarks comparing DiskImages2 vs hdiutil CLI.

func BenchmarkCreateASIF(b *testing.B) {
	if !diskimages2.Available() {
		b.Skip("DiskImages2 framework not available")
	}
	dir := b.TempDir()
	const size = 100 * 1024 * 1024

	b.ResetTimer()
	for i := range b.N {
		imgPath := filepath.Join(dir, "bench-asif-"+itoa(i)+".sparseimage")
		if err := diskimages2.CreateASIF(imgPath, size); err != nil {
			b.Fatalf("CreateASIF: %v", err)
		}
	}
}

func BenchmarkCreateHdiutil(b *testing.B) {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		b.Skip("hdiutil not available")
	}
	dir := b.TempDir()

	b.ResetTimer()
	for i := range b.N {
		imgPath := filepath.Join(dir, "bench-hdi-"+itoa(i))
		cmd := exec.Command("hdiutil", "create",
			"-size", "100m",
			"-type", "SPARSE",
			"-fs", "APFS",
			"-volname", "BenchTest",
			imgPath,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("hdiutil create: %v\n%s", err, out)
		}
	}
}

func BenchmarkAttachDetach(b *testing.B) {
	if !diskimages2.Available() {
		b.Skip("DiskImages2 framework not available")
	}
	dir := b.TempDir()
	imgPath := filepath.Join(dir, "bench-attach.sparseimage")
	const size = 50 * 1024 * 1024
	if err := diskimages2.CreateASIF(imgPath, size); err != nil {
		b.Fatalf("CreateASIF: %v", err)
	}

	b.ResetTimer()
	for range b.N {
		h, err := diskimages2.Attach(imgPath, diskimages2.AttachOptions{AutoMount: false})
		if err != nil {
			b.Fatalf("Attach: %v", err)
		}
		if err := diskimages2.DetachHandle(h); err != nil {
			b.Fatalf("DetachHandle: %v", err)
		}
	}
}

func BenchmarkAttachDetachHdiutil(b *testing.B) {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		b.Skip("hdiutil not available")
	}
	dir := b.TempDir()
	imgBase := filepath.Join(dir, "bench-hdi-attach")
	cmd := exec.Command("hdiutil", "create",
		"-size", "50m",
		"-type", "SPARSE",
		"-fs", "APFS",
		"-volname", "BenchAttach",
		imgBase,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("hdiutil create: %v\n%s", err, out)
	}
	imgPath := imgBase + ".sparseimage"

	b.ResetTimer()
	for range b.N {
		out, err := exec.Command("hdiutil", "attach", imgPath, "-nobrowse", "-nomount").Output()
		if err != nil {
			b.Fatalf("hdiutil attach: %v", err)
		}
		// Parse device from output (first field of first line).
		device := parseFirstDevice(out)
		if device == "" {
			b.Fatal("could not parse device from hdiutil output")
		}
		if err := exec.Command("hdiutil", "detach", device, "-force").Run(); err != nil {
			b.Fatalf("hdiutil detach: %v", err)
		}
	}
}

func BenchmarkRetrieveInfo(b *testing.B) {
	if !diskimages2.Available() {
		b.Skip("DiskImages2 framework not available")
	}
	dir := b.TempDir()
	imgPath := filepath.Join(dir, "bench-info.sparseimage")
	const size = 50 * 1024 * 1024
	if err := diskimages2.CreateASIF(imgPath, size); err != nil {
		b.Fatalf("CreateASIF: %v", err)
	}

	b.ResetTimer()
	for range b.N {
		if _, err := diskimages2.RetrieveInfo(imgPath); err != nil {
			b.Fatalf("RetrieveInfo: %v", err)
		}
	}
}

func BenchmarkRetrieveInfoHdiutil(b *testing.B) {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		b.Skip("hdiutil not available")
	}
	dir := b.TempDir()
	imgBase := filepath.Join(dir, "bench-hdi-info")
	cmd := exec.Command("hdiutil", "create",
		"-size", "50m",
		"-type", "SPARSE",
		"-fs", "APFS",
		"-volname", "BenchInfo",
		imgBase,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("hdiutil create: %v\n%s", err, out)
	}
	imgPath := imgBase + ".sparseimage"

	b.ResetTimer()
	for range b.N {
		if out, err := exec.Command("hdiutil", "imageinfo", imgPath).CombinedOutput(); err != nil {
			b.Fatalf("hdiutil imageinfo: %v\n%s", err, out)
		}
	}
}

// parseFirstDevice extracts the first /dev/diskN from hdiutil attach output.
func parseFirstDevice(out []byte) string {
	s := string(out)
	for i := 0; i < len(s); i++ {
		if i+9 < len(s) && s[i:i+9] == "/dev/disk" {
			end := i + 9
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			if end > i+9 {
				return s[i:end]
			}
		}
	}
	return ""
}

// itoa is a simple int-to-string for benchmark naming.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
