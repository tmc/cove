package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
)

const diskutilPath = "/usr/sbin/diskutil"

var runDiskutil = runDiskutilCommand

type diskutilResult struct {
	stdout []byte
	stderr []byte
}

type macOSAPFSRoot struct {
	container       string
	physicalStore   string
	physicalDisk    string
	storePartition  uint32
	containerBytes  uint64
	recoveryBlocker string
}

func resizeMacOSAPFS(ctx context.Context, preflightOnly bool) (*pb.ResizeMacOSAPFSResponse, error) {
	if os.Geteuid() != 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("APFS resize requires the root daemon agent"))
	}
	if _, err := os.Stat(diskutilPath); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("stat diskutil: %v", err))
	}

	root, err := inspectMacOSAPFSRoot(ctx)
	if err != nil {
		return nil, err
	}
	resp := &pb.ResizeMacOSAPFSResponse{
		Container:                 root.container,
		PhysicalStore:             root.physicalStore,
		PhysicalDisk:              root.physicalDisk,
		StorePartition:            root.storePartition,
		ContainerTotalBytesBefore: root.containerBytes,
	}
	if root.recoveryBlocker != "" {
		err := fmt.Errorf("Recovery partition blocks APFS expansion: %s is followed by %s on /dev/%s", root.physicalStore, root.recoveryBlocker, root.physicalDisk)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if preflightOnly {
		return resp, nil
	}

	out, err := runDiskutil(ctx, "apfs", "resizeContainer", root.container, "0")
	resp.Stdout = strings.TrimSpace(string(out.stdout))
	resp.Stderr = strings.TrimSpace(string(out.stderr))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, diskutilError("resize APFS container", out, err))
	}

	updated, err := inspectMacOSAPFSRoot(ctx)
	if err != nil {
		return nil, err
	}
	resp.ContainerTotalBytesAfter = updated.containerBytes
	resp.Expanded = true
	return resp, nil
}

func inspectMacOSAPFSRoot(ctx context.Context) (macOSAPFSRoot, error) {
	infoOut, err := runDiskutil(ctx, "info", "/")
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, diskutilError("inspect root APFS container", infoOut, err))
	}
	info := string(infoOut.stdout)
	container, err := parseDiskutilAPFSContainer(info)
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	apfsOut, err := runDiskutil(ctx, "apfs", "list", container)
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, diskutilError("inspect APFS physical store", apfsOut, err))
	}
	physicalStore, err := parseDiskutilAPFSPhysicalStore(string(apfsOut.stdout))
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	physicalDisk, storePartition, err := parseDiskutilPartitionID(physicalStore)
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	listOut, err := runDiskutil(ctx, "list", "/dev/"+physicalDisk)
	if err != nil {
		return macOSAPFSRoot{}, connect.NewError(connect.CodeFailedPrecondition, diskutilError("inspect physical disk partitions", listOut, err))
	}
	return macOSAPFSRoot{
		container:       container,
		physicalStore:   physicalStore,
		physicalDisk:    physicalDisk,
		storePartition:  storePartition,
		containerBytes:  parseDiskutilBytesLine(info, "Container Total Space:"),
		recoveryBlocker: parseDiskutilRecoveryBlocker(string(listOut.stdout), physicalDisk, storePartition),
	}, nil
}

func runDiskutilCommand(ctx context.Context, args ...string) (diskutilResult, error) {
	var out diskutilResult
	cmd := exec.CommandContext(ctx, diskutilPath, args...)
	cmd.Stdout = bytes.NewBuffer(nil)
	cmd.Stderr = bytes.NewBuffer(nil)
	err := cmd.Run()
	if b, ok := cmd.Stdout.(*bytes.Buffer); ok {
		out.stdout = append([]byte(nil), b.Bytes()...)
	}
	if b, ok := cmd.Stderr.(*bytes.Buffer); ok {
		out.stderr = append([]byte(nil), b.Bytes()...)
	}
	return out, err
}

func diskutilError(op string, out diskutilResult, err error) error {
	var parts []string
	if s := strings.TrimSpace(string(out.stdout)); s != "" {
		parts = append(parts, "stdout: "+s)
	}
	if s := strings.TrimSpace(string(out.stderr)); s != "" {
		parts = append(parts, "stderr: "+s)
	}
	if len(parts) == 0 {
		parts = append(parts, err.Error())
	}
	return fmt.Errorf("%s: %v: %s", op, err, strings.Join(parts, "; "))
}

func parseDiskutilAPFSContainer(out string) (string, error) {
	v := parseDiskutilColonLine(out, "APFS Container")
	if v == "" {
		return "", errors.New("could not find APFS container for /")
	}
	return v, nil
}

func parseDiskutilAPFSPhysicalStore(out string) (string, error) {
	v := parseDiskutilColonLine(out, "APFS Physical Store Disk")
	if v == "" {
		return "", errors.New("could not find APFS physical store for /")
	}
	return v, nil
}

func parseDiskutilColonLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		_, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		return strings.TrimSpace(v)
	}
	return ""
}

var diskutilPartitionRE = regexp.MustCompile(`^(disk[0-9]+)s([0-9]+)$`)

func parseDiskutilPartitionID(id string) (string, uint32, error) {
	m := diskutilPartitionRE.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return "", 0, fmt.Errorf("could not parse APFS physical store %s", id)
	}
	n, err := strconv.ParseUint(m[2], 10, 32)
	if err != nil || n == 0 {
		return "", 0, fmt.Errorf("could not parse APFS physical store %s", id)
	}
	return m[1], uint32(n), nil
}

func parseDiskutilRecoveryBlocker(out, disk string, storePartition uint32) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Apple_APFS_Recovery") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[len(fields)-1]
		physicalDisk, part, err := parseDiskutilPartitionID(id)
		if err != nil || physicalDisk != disk || part <= storePartition {
			continue
		}
		return id
	}
	return ""
}

func parseDiskutilBytesLine(out, prefix string) uint64 {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, prefix) {
			continue
		}
		return parseDiskutilBytes(line)
	}
	return 0
}

var diskutilBytesRE = regexp.MustCompile(`\(([0-9]+) Bytes\)`)

func parseDiskutilBytes(line string) uint64 {
	m := diskutilBytesRE.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
