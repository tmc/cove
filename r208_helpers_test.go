package main

import (
	"flag"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestCloneVolumeMounts(t *testing.T) {
	if got := cloneVolumeMounts(nil); got != nil {
		t.Fatalf("nil input: got %v, want nil", got)
	}
	if got := cloneVolumeMounts([]vmconfig.VolumeMount{}); got != nil {
		t.Fatalf("empty input: got %v, want nil", got)
	}
	in := []vmconfig.VolumeMount{{HostPath: "/a"}, {HostPath: "/b"}}
	out := cloneVolumeMounts(in)
	if len(out) != 2 || out[0].HostPath != "/a" || out[1].HostPath != "/b" {
		t.Fatalf("unexpected clone contents: %+v", out)
	}
	out[0].HostPath = "/x"
	if in[0].HostPath != "/a" {
		t.Fatalf("clone aliased source slice: in[0]=%q", in[0].HostPath)
	}
}

func TestCloneSharedFolderEntries(t *testing.T) {
	if got := cloneSharedFolderEntries(nil); got != nil {
		t.Fatalf("nil input: got %v, want nil", got)
	}
	if got := cloneSharedFolderEntries([]SharedFolderEntry{}); got != nil {
		t.Fatalf("empty input: got %v, want nil", got)
	}
	in := []SharedFolderEntry{{Tag: "t1", Path: "/p1"}, {Tag: "t2", Path: "/p2"}}
	out := cloneSharedFolderEntries(in)
	if len(out) != 2 || out[0].Tag != "t1" || out[1].Tag != "t2" {
		t.Fatalf("unexpected clone contents: %+v", out)
	}
	out[0].Tag = "changed"
	if in[0].Tag != "t1" {
		t.Fatalf("clone aliased entry slice: in[0].Tag=%q", in[0].Tag)
	}
}

func TestFlagWasSet(t *testing.T) {
	saved := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = saved })

	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	flag.String("alpha", "", "")
	flag.String("beta", "", "")
	if err := flag.CommandLine.Parse([]string{"-alpha=1"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !flagWasSet("alpha") {
		t.Fatal("flagWasSet(alpha) = false, want true")
	}
	if flagWasSet("beta") {
		t.Fatal("flagWasSet(beta) = true, want false")
	}
	if flagWasSet("gamma") {
		t.Fatal("flagWasSet(gamma) = true, want false (unknown name)")
	}
}
