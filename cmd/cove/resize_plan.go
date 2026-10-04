package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/checkpoint"
)

type diskGrowthPartition struct {
	Index int    `json:"index"`
	Type  string `json:"type"`
	First uint64 `json:"first_sector"`
	Last  uint64 `json:"last_sector"`
}

type diskGrowthPlan struct {
	Version           int                   `json:"version"`
	Disk              string                `json:"disk"`
	SectorBytes       uint64                `json:"sector_bytes"`
	CurrentBytes      uint64                `json:"current_bytes"`
	RequestedBytes    uint64                `json:"requested_bytes"`
	BackupSector      uint64                `json:"backup_sector"`
	Partitions        []diskGrowthPartition `json:"partitions"`
	MutationAvailable bool                  `json:"mutation_available"`
	Blockers          []string              `json:"blockers"`
	NextActions       []string              `json:"next_actions"`
}

type growthGPTHeader struct {
	current, alternate, first, last, table uint64
	count, entrySize                       uint32
	guid                                   []byte
	entries                                []byte
}

func runDiskResizePlan(env commandEnv, args []string) error {
	asJSON := false
	if len(args) > 0 && args[0] == "-json" {
		asJSON = true
		args = args[1:]
	}
	name, size, err := parseDiskResizeArgs(args)
	if err != nil {
		return fmt.Errorf("usage: cove disk resize-plan [-json] <vm> <size>")
	}
	dir, err := requireExistingVMDir("disk resize-plan", name)
	if err != nil {
		return err
	}
	pending, err := checkpoint.New(dir).Pending()
	if err != nil {
		return fmt.Errorf("inspect pending checkpoint recovery: %w", err)
	}
	path := vmPrimaryDiskPath(dir)
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect preview image: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("preview requires a regular raw disk image, not a symlink or device")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open disk for read-only preview: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("preview requires a regular raw disk image")
	}
	target, err := bytefmt.Parse(size)
	if err != nil {
		return fmt.Errorf("parse target size: %w", err)
	}
	plan, err := inspectDiskGrowth(f, uint64(info.Size()), target)
	if err != nil {
		return fmt.Errorf("inspect raw GPT disk: %w", err)
	}
	plan.Disk = path
	if pending {
		plan.Blockers = append(plan.Blockers, "pending checkpoint restore requires cove checkpoint recover before any disk mutation")
	}
	if asJSON {
		return json.NewEncoder(env.Stdout).Encode(plan)
	}
	fmt.Fprintf(env.Stdout, "Read-only disk growth preview: %s\nDisk: %d bytes; requested: %d bytes; sector: %d bytes\n", path, plan.CurrentBytes, plan.RequestedBytes, plan.SectorBytes)
	for _, p := range plan.Partitions {
		fmt.Fprintf(env.Stdout, "Partition %d: %s sectors %d..%d\n", p.Index, p.Type, p.First, p.Last)
	}
	fmt.Fprintln(env.Stdout, "Recovery-preserving offline growth is unavailable:")
	for _, s := range plan.Blockers {
		fmt.Fprintf(env.Stdout, "  - %s\n", s)
	}
	for _, s := range plan.NextActions {
		fmt.Fprintf(env.Stdout, "Next action: %s\n", s)
	}
	return nil
}

func inspectDiskGrowth(r io.ReaderAt, size, target uint64) (diskGrowthPlan, error) {
	p := diskGrowthPlan{Version: 1, CurrentBytes: size, RequestedBytes: target}
	if size > math.MaxInt64 || target > math.MaxInt64 || target < size {
		return p, fmt.Errorf("target must grow the disk and fit signed file offsets")
	}
	mbr := make([]byte, 512)
	if _, err := r.ReadAt(mbr, 0); err != nil {
		return p, fmt.Errorf("read protective MBR: %w", err)
	}
	if mbr[510] != 0x55 || mbr[511] != 0xaa {
		return p, fmt.Errorf("missing protective MBR signature; only raw GPT images can be inspected")
	}
	protective := 0
	for i := 0; i < 4; i++ {
		typ := mbr[446+i*16+4]
		if typ == 0xee {
			protective++
		} else if typ != 0 {
			return p, fmt.Errorf("hybrid MBR layouts are unsupported")
		}
	}
	if protective != 1 {
		return p, fmt.Errorf("expected one protective MBR partition")
	}
	for _, sector := range []uint64{512, 4096} {
		sig := make([]byte, 8)
		if _, err := r.ReadAt(sig, int64(sector)); err == nil && string(sig) == "EFI PART" {
			p.SectorBytes = sector
			break
		}
	}
	if p.SectorBytes == 0 {
		return p, fmt.Errorf("missing GPT header at supported 512 or 4096 byte sector offset")
	}
	sector := p.SectorBytes
	if size%sector != 0 || target%sector != 0 {
		return p, fmt.Errorf("disk and target sizes must align to %d byte sectors", sector)
	}
	primary, err := readGrowthGPT(r, size, sector, 1)
	if err != nil {
		return p, fmt.Errorf("primary GPT: %w", err)
	}
	backup, err := readGrowthGPT(r, size, sector, primary.alternate)
	if err != nil {
		return p, fmt.Errorf("backup GPT: %w", err)
	}
	if backup.alternate != 1 || primary.current != 1 || backup.current != primary.alternate || primary.first != backup.first || primary.last != backup.last || primary.count != backup.count || primary.entrySize != backup.entrySize || !bytes.Equal(primary.guid, backup.guid) || !bytes.Equal(primary.entries, backup.entries) {
		return p, fmt.Errorf("primary and backup GPT disagree")
	}
	tableSectors := (uint64(len(primary.entries)) + sector - 1) / sector
	if primary.table < 2 || primary.table+tableSectors > primary.first || backup.table <= primary.last || backup.table+tableSectors > backup.current || primary.last >= backup.table || primary.first > primary.last {
		return p, fmt.Errorf("GPT metadata overlaps usable sectors")
	}
	p.BackupSector = backup.current
	identities := map[string]bool{}
	for i := uint32(0); i < primary.count; i++ {
		entry := primary.entries[uint64(i)*uint64(primary.entrySize) : uint64(i+1)*uint64(primary.entrySize)]
		if bytes.Equal(entry[:16], make([]byte, 16)) {
			continue
		}
		first, last := binary.LittleEndian.Uint64(entry[32:40]), binary.LittleEndian.Uint64(entry[40:48])
		if first > last || first < primary.first || last > primary.last {
			return p, fmt.Errorf("partition %d outside usable GPT bounds", i+1)
		}
		id := hex.EncodeToString(entry[16:32])
		if id == strings.Repeat("0", 32) || identities[id] {
			return p, fmt.Errorf("partition %d has missing or duplicate identity", i+1)
		}
		identities[id] = true
		typ := hex.EncodeToString(entry[:16])
		switch typ {
		case "ef57347c0000aa11aa1100306543ecac":
			typ = "Apple APFS"
		case "727663520079aa11aa1100306543ecac":
			typ = "Apple APFS Recovery"
		case "616964690067aa11aa1100306543ecac":
			typ = "Apple APFS ISC"
		}
		p.Partitions = append(p.Partitions, diskGrowthPartition{int(i + 1), typ, first, last})
	}
	sort.Slice(p.Partitions, func(i, j int) bool { return p.Partitions[i].First < p.Partitions[j].First })
	for i := 1; i < len(p.Partitions); i++ {
		if p.Partitions[i].First <= p.Partitions[i-1].Last {
			return p, fmt.Errorf("partitions %d and %d overlap", p.Partitions[i-1].Index, p.Partitions[i].Index)
		}
	}
	supported := len(p.Partitions) == 3 && p.Partitions[0].Type == "Apple APFS ISC" && p.Partitions[1].Type == "Apple APFS" && p.Partitions[2].Type == "Apple APFS Recovery"
	if !supported {
		p.Blockers = append(p.Blockers, "layout is outside the candidate ISC + APFS + trailing Recovery inventory")
	} else {
		p.Blockers = append(p.Blockers, "trailing Recovery blocks current APFS growth; Recovery relocation is not enabled")
	}
	if backup.current != size/sector-1 {
		p.Blockers = append(p.Blockers, "backup GPT is not at the image end; backing image may already have been grown")
	}
	p.Blockers = append(p.Blockers, "APFS encryption and filesystem health are unknown from GPT inventory", "exclusive stopped ownership and attached-device checks have not been performed by this read-only preview", "durable backup, Recovery checksum relocation and crash-phase rollback are not implemented", "host APFS growth and normal/Recovery boot have not been physically qualified")
	p.NextActions = []string{"preserve the original image and a verified cold checkpoint before making changes", "keep the existing Recovery refusal; do not delete or recreate Recovery to bypass it", "if a prior backing-image resize left APFS unchanged, retain that image and inspect guest layout; further truncation does not remove the Recovery blocker", "use a larger separately provisioned guest and verified data migration until recovery-preserving growth is qualified"}
	return p, nil
}

func readGrowthGPT(r io.ReaderAt, size, sector, lba uint64) (growthGPTHeader, error) {
	var h growthGPTHeader
	if lba >= size/sector {
		return h, fmt.Errorf("header sector outside image")
	}
	b := make([]byte, sector)
	if _, err := r.ReadAt(b, int64(lba*sector)); err != nil {
		return h, err
	}
	if string(b[:8]) != "EFI PART" || binary.LittleEndian.Uint32(b[8:12]) != 0x10000 {
		return h, fmt.Errorf("unsupported GPT signature or revision")
	}
	n := binary.LittleEndian.Uint32(b[12:16])
	if n < 92 || uint64(n) > sector {
		return h, fmt.Errorf("invalid header size")
	}
	want := binary.LittleEndian.Uint32(b[16:20])
	copyHeader := append([]byte(nil), b[:n]...)
	clear(copyHeader[16:20])
	if crc32.ChecksumIEEE(copyHeader) != want {
		return h, fmt.Errorf("header CRC mismatch")
	}
	if binary.LittleEndian.Uint32(b[20:24]) != 0 {
		return h, fmt.Errorf("nonzero reserved header field")
	}
	h.current = binary.LittleEndian.Uint64(b[24:32])
	h.alternate = binary.LittleEndian.Uint64(b[32:40])
	h.first = binary.LittleEndian.Uint64(b[40:48])
	h.last = binary.LittleEndian.Uint64(b[48:56])
	h.guid = append([]byte(nil), b[56:72]...)
	h.table = binary.LittleEndian.Uint64(b[72:80])
	h.count = binary.LittleEndian.Uint32(b[80:84])
	h.entrySize = binary.LittleEndian.Uint32(b[84:88])
	if h.current != lba || h.alternate == lba || h.alternate >= size/sector || h.first < 2 || h.last >= size/sector {
		return h, fmt.Errorf("invalid header sector bounds")
	}
	if h.count == 0 || h.count > 128 || h.entrySize < 128 || h.entrySize > 1024 || h.entrySize%128 != 0 {
		return h, fmt.Errorf("unsupported partition table dimensions")
	}
	length := uint64(h.count) * uint64(h.entrySize)
	if h.table >= size/sector || length > size-h.table*sector {
		return h, fmt.Errorf("partition table outside image")
	}
	h.entries = make([]byte, length)
	if _, err := r.ReadAt(h.entries, int64(h.table*sector)); err != nil {
		return h, err
	}
	if crc32.ChecksumIEEE(h.entries) != binary.LittleEndian.Uint32(b[88:92]) {
		return h, fmt.Errorf("partition table CRC mismatch")
	}
	return h, nil
}
