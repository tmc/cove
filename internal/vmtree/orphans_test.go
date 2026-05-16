package vmtree

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRenderOrphanListPlain(t *testing.T) {
	forked := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		orphans []*vmTreeNode
		want    []string
		empty   bool
	}{
		{
			name:    "empty list prints sentinel",
			orphans: nil,
			want:    []string{"No orphan VMs."},
		},
		{
			name: "parent only",
			orphans: []*vmTreeNode{
				{name: "child", parent: "missing"},
			},
			want: []string{"child (parent missing: missing)"},
		},
		{
			name: "parent and snapshot",
			orphans: []*vmTreeNode{
				{name: "child", parent: "missing", parentSnapshot: "snap1"},
			},
			want: []string{"child (parent missing: missing@snap1)"},
		},
		{
			name: "parent and forkedAt",
			orphans: []*vmTreeNode{
				{name: "child", parent: "missing", forkedAt: forked},
			},
			want: []string{"child (parent missing: missing, forked 2026-04-01)"},
		},
		{
			name: "parent snapshot and forkedAt",
			orphans: []*vmTreeNode{
				{name: "c1", parent: "p1", parentSnapshot: "s1", forkedAt: forked},
				{name: "c2", parent: "p2"},
			},
			want: []string{
				"c1 (parent missing: p1@s1, forked 2026-04-01)",
				"c2 (parent missing: p2)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderOrphanList(&buf, tt.orphans, false); err != nil {
				t.Fatalf("renderOrphanList() error = %v", err)
			}
			got := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("renderOrphanList() = %q, want substring %q", got, want)
				}
			}
		})
	}
}

func TestRenderOrphanListJSON(t *testing.T) {
	forked := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	orphans := []*vmTreeNode{
		{name: "c1", parent: "p1", parentSnapshot: "s1", forkedAt: forked},
		{name: "c2", parent: "p2"},
	}
	var buf bytes.Buffer
	if err := renderOrphanList(&buf, orphans, true); err != nil {
		t.Fatalf("renderOrphanList(json) error = %v", err)
	}
	var got []vmTreeJSONNode
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v; got %s", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d nodes, want 2", len(got))
	}
	for _, n := range got {
		if !n.Orphan {
			t.Errorf("node %q Orphan = false, want true", n.Name)
		}
	}
	if got[0].Name != "c1" || got[0].ParentVM != "p1" || got[0].ParentSnapshot != "s1" {
		t.Errorf("first node = %+v, want c1/p1/s1", got[0])
	}
	if got[0].ForkedAt != "2026-04-01T12:00:00Z" {
		t.Errorf("first node ForkedAt = %q, want RFC3339 UTC", got[0].ForkedAt)
	}
	if got[1].Name != "c2" || got[1].ParentSnapshot != "" || got[1].ForkedAt != "" {
		t.Errorf("second node = %+v, want bare c2", got[1])
	}
}

func TestRenderOrphanListJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderOrphanList(&buf, nil, true); err != nil {
		t.Fatalf("renderOrphanList(json,nil) error = %v", err)
	}
	var got []vmTreeJSONNode
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v; got %s", err, buf.String())
	}
	if len(got) != 0 {
		t.Errorf("got %d nodes, want 0", len(got))
	}
}
