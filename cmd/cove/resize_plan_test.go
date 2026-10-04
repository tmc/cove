package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func growthFixture(sector int) []byte {
	b := make([]byte, sector*1024)
	b[510], b[511] = 0x55, 0xaa
	b[450] = 0xee
	table := make([]byte, 128*4)
	types := []string{"616964690067aa11aa1100306543ecac", "ef57347c0000aa11aa1100306543ecac", "727663520079aa11aa1100306543ecac"}
	for i, typ := range types {
		e := table[i*128 : (i+1)*128]
		g, _ := hex.DecodeString(typ)
		copy(e, g)
		e[16] = byte(i + 1)
		binary.LittleEndian.PutUint64(e[32:40], uint64(10+i*100))
		binary.LittleEndian.PutUint64(e[40:48], uint64(99+i*100))
	}
	copy(b[sector*2:], table)
	copy(b[sector*1022:], table)
	for _, lba := range []int{1, 1023} {
		h := b[lba*sector : (lba+1)*sector]
		copy(h, "EFI PART")
		binary.LittleEndian.PutUint32(h[8:12], 0x10000)
		binary.LittleEndian.PutUint32(h[12:16], 92)
		binary.LittleEndian.PutUint64(h[24:32], uint64(lba))
		other, tab := 1023, 2
		if lba == 1023 {
			other, tab = 1, 1022
		}
		binary.LittleEndian.PutUint64(h[32:40], uint64(other))
		binary.LittleEndian.PutUint64(h[40:48], 3)
		binary.LittleEndian.PutUint64(h[48:56], 1021)
		h[56] = 1
		binary.LittleEndian.PutUint64(h[72:80], uint64(tab))
		binary.LittleEndian.PutUint32(h[80:84], 4)
		binary.LittleEndian.PutUint32(h[84:88], 128)
		binary.LittleEndian.PutUint32(h[88:92], crc32.ChecksumIEEE(table))
		growthHeaderCRC(h)
	}
	return b
}
func growthHeaderCRC(h []byte) {
	clear(h[16:20])
	binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[:92]))
}
func growthTableCRCs(b []byte) {
	copy(b[1022*512:1023*512], b[2*512:3*512])
	for _, lba := range []int{1, 1023} {
		h := b[lba*512 : (lba+1)*512]
		binary.LittleEndian.PutUint32(h[88:92], crc32.ChecksumIEEE(b[1024:1536]))
		growthHeaderCRC(h)
	}
}
func TestDiskResizePlanGPT(t *testing.T) {
	for _, sector := range []int{512, 4096} {
		t.Run(strconv.Itoa(sector), func(t *testing.T) {
			b := growthFixture(sector)
			before := append([]byte(nil), b...)
			p, err := inspectDiskGrowth(bytes.NewReader(b), uint64(len(b)), uint64(len(b))*2)
			if err != nil {
				t.Fatal(err)
			}
			if p.MutationAvailable || p.SectorBytes != uint64(sector) || len(p.Partitions) != 3 || len(p.Blockers) < 5 {
				t.Fatalf("plan=%+v", p)
			}
			if !bytes.Equal(b, before) {
				t.Fatal("preview changed image")
			}
		})
	}
}
func TestDiskResizePlanRejectsInvalidGPT(t *testing.T) {
	tests := []struct {
		name, want string
		mutate     func([]byte)
	}{
		{"primary CRC", "primary GPT: header CRC", func(b []byte) { b[512+56] ^= 1 }},
		{"backup CRC", "backup GPT: header CRC", func(b []byte) { b[1023*512+56] ^= 1 }},
		{"primary table CRC", "partition table CRC", func(b []byte) { b[1024] ^= 1 }},
		{"backup table CRC", "backup GPT: partition table CRC", func(b []byte) { b[1022*512] ^= 1 }},
		{"overlap", "overlap", func(b []byte) { binary.LittleEndian.PutUint64(b[1024+128+32:], 90); growthTableCRCs(b) }},
		{"out of bounds", "outside usable", func(b []byte) { binary.LittleEndian.PutUint64(b[1024+40:], 1023); growthTableCRCs(b) }},
		{"table overflow", "outside image", func(b []byte) {
			h := b[512:1024]
			binary.LittleEndian.PutUint64(h[72:], math.MaxUint64)
			growthHeaderCRC(h)
		}},
		{"unbounded dimensions", "dimensions", func(b []byte) {
			h := b[512:1024]
			binary.LittleEndian.PutUint32(h[80:], math.MaxUint32)
			growthHeaderCRC(h)
		}},
		{"backup disagreement", "disagree", func(b []byte) { h := b[1023*512:]; h[56] ^= 1; growthHeaderCRC(h) }},
		{"metadata overlap", "metadata overlaps", func(b []byte) {
			for _, lba := range []int{1, 1023} {
				h := b[lba*512:]
				binary.LittleEndian.PutUint64(h[40:], 2)
				growthHeaderCRC(h)
			}
		}},
		{"hybrid MBR", "hybrid", func(b []byte) { b[466] = 7 }},
		{"missing MBR", "signature", func(b []byte) { b[511] = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := growthFixture(512)
			tt.mutate(b)
			before := append([]byte(nil), b...)
			_, err := inspectDiskGrowth(bytes.NewReader(b), uint64(len(b)), uint64(len(b))*2)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
			if !bytes.Equal(b, before) {
				t.Fatal("invalid preview changed disk")
			}
		})
	}
}
func TestDiskResizePlanCapabilities(t *testing.T) {
	b := growthFixture(512)
	b[1024] = 1
	growthTableCRCs(b)
	p, err := inspectDiskGrowth(bytes.NewReader(b), uint64(len(b)), uint64(len(b))*2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Blockers, " "), "outside the candidate") {
		t.Fatal(p)
	}
	b = growthFixture(512)
	b = append(b, make([]byte, 512*100)...)
	p, err = inspectDiskGrowth(bytes.NewReader(b), uint64(len(b)), uint64(len(b))*2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Blockers, " "), "not at the image end") {
		t.Fatal(p)
	}
	for _, target := range []uint64{1, math.MaxUint64, uint64(len(b)) + 1} {
		if _, err := inspectDiskGrowth(bytes.NewReader(b), uint64(len(b)), target); err == nil {
			t.Fatalf("accepted target %d", target)
		}
	}
}
func TestDiskResizePlanCommandReadOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // No host tools or guest commands are available.
	dir := filepath.Join(vmconfig.BaseDir(), "preview")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b := growthFixture(512)
	path := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aux.img", "hw.model"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := handleDiskCommand(commandEnv{Stdout: &out}, []string{"resize-plan", "-json", "preview", "1M"}); err != nil {
		t.Fatal(err)
	}
	var p diskGrowthPlan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.MutationAvailable {
		t.Fatal("mutation enabled")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, after) {
		t.Fatal("source changed")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("preview created state: %v", files)
	}
}
