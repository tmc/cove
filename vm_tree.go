package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

type vmTreeNode struct {
	name           string
	parent         string
	parentSnapshot string
	forkedAt       time.Time
	orphan         bool
	children       []*vmTreeNode
}

// VMTreeOptions configures vm tree rendering.
type VMTreeOptions struct {
	// JSON emits a structured tree (forest of root nodes with children).
	JSON bool
	// Orphans filters output to VMs whose ParentVM points at a missing
	// VM. The orphans are listed flat (no children walked, no roots
	// shown), which is the natural shape for a "what's broken?" sweep.
	Orphans bool
}

// PrintVMTree writes the VM fork lineage tree with default options
// (ASCII, full forest). Kept as a stable entry point for callers that
// don't need flag handling.
func PrintVMTree(w io.Writer) error {
	return PrintVMTreeWithOptions(w, VMTreeOptions{})
}

// PrintVMTreeWithOptions writes the VM fork lineage tree honoring opts.
func PrintVMTreeWithOptions(w io.Writer, opts VMTreeOptions) error {
	roots, orphans, err := loadVMTree()
	if err != nil {
		return err
	}
	if opts.Orphans {
		return renderOrphanList(w, orphans, opts.JSON)
	}
	if len(roots) == 0 && len(orphans) == 0 {
		if opts.JSON {
			fmt.Fprintln(w, "[]")
			return nil
		}
		fmt.Fprintln(w, "No VMs found.")
		return nil
	}
	// Orphans appear as additional roots in the default forest, the
	// same shape PrintVMTree had before this change. The JSON shape
	// also folds them in so consumers don't need to special-case.
	all := append(append([]*vmTreeNode{}, roots...), orphans...)
	sortVMTree(all)
	if opts.JSON {
		return renderTreeJSON(w, all)
	}
	for i, root := range all {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printVMTreeNode(w, root, "", true, true)
	}
	return nil
}

// loadVMTree reads the VM inventory, builds the parent→children
// forest, and returns the roots and the orphan list separately. A
// node is orphan if its ParentVM is non-empty but does not resolve
// to any VM in the inventory.
func loadVMTree() (roots []*vmTreeNode, orphans []*vmTreeNode, err error) {
	vms, err := vmconfig.List(nil)
	if err != nil {
		return nil, nil, err
	}
	if len(vms) == 0 {
		return nil, nil, nil
	}

	nodes := make(map[string]*vmTreeNode)
	for _, vm := range vms {
		cfg, lerr := vmconfig.Load(vm.Path)
		if lerr != nil {
			return nil, nil, fmt.Errorf("load %s config: %w", vm.Name, lerr)
		}
		nodes[vm.Name] = &vmTreeNode{
			name:           vm.Name,
			parent:         cfg.ParentVM,
			parentSnapshot: cfg.ParentSnapshot,
			forkedAt:       cfg.ForkedAt,
		}
	}

	for _, node := range nodes {
		if node.parent == "" {
			roots = append(roots, node)
			continue
		}
		parent := nodes[node.parent]
		if parent == nil {
			node.orphan = true
			orphans = append(orphans, node)
			continue
		}
		parent.children = append(parent.children, node)
	}
	sortVMTree(roots)
	sortVMTree(orphans)
	return roots, orphans, nil
}

func sortVMTree(nodes []*vmTreeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].name < nodes[j].name
	})
	for _, node := range nodes {
		sortVMTree(node.children)
	}
}

func printVMTreeNode(w io.Writer, node *vmTreeNode, prefix string, last bool, root bool) {
	if root {
		fmt.Fprintln(w, vmTreeLabel(node))
	} else {
		branch := "|-- "
		if last {
			branch = "`-- "
		}
		fmt.Fprintln(w, prefix+branch+vmTreeLabel(node))
	}

	childPrefix := prefix
	if !root {
		if last {
			childPrefix += "    "
		} else {
			childPrefix += "|   "
		}
	}
	for i, child := range node.children {
		printVMTreeNode(w, child, childPrefix, i == len(node.children)-1, false)
	}
}

// vmTreeJSONNode is the on-the-wire shape for --json output. ForkedAt
// is rendered in RFC3339 if non-zero, omitted otherwise; same for
// ParentSnapshot. Children are listed as nested nodes; the parent
// field is implicit from nesting.
type vmTreeJSONNode struct {
	Name           string           `json:"name"`
	ParentVM       string           `json:"parentVM,omitempty"`
	ParentSnapshot string           `json:"parentSnapshot,omitempty"`
	ForkedAt       string           `json:"forkedAt,omitempty"`
	Orphan         bool             `json:"orphan,omitempty"`
	Children       []vmTreeJSONNode `json:"children,omitempty"`
}

func toVMTreeJSON(node *vmTreeNode) vmTreeJSONNode {
	out := vmTreeJSONNode{
		Name:           node.name,
		ParentVM:       node.parent,
		ParentSnapshot: node.parentSnapshot,
		Orphan:         node.orphan,
	}
	if !node.forkedAt.IsZero() {
		out.ForkedAt = node.forkedAt.UTC().Format(time.RFC3339)
	}
	for _, child := range node.children {
		out.Children = append(out.Children, toVMTreeJSON(child))
	}
	return out
}

func renderTreeJSON(w io.Writer, roots []*vmTreeNode) error {
	out := make([]vmTreeJSONNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, toVMTreeJSON(r))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func renderOrphanList(w io.Writer, orphans []*vmTreeNode, asJSON bool) error {
	if asJSON {
		out := make([]vmTreeJSONNode, 0, len(orphans))
		for _, o := range orphans {
			n := toVMTreeJSON(o)
			n.Orphan = true
			out = append(out, n)
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(orphans) == 0 {
		fmt.Fprintln(w, "No orphan VMs.")
		return nil
	}
	for _, o := range orphans {
		fmt.Fprintf(w, "%s (parent missing: %s", o.name, o.parent)
		if o.parentSnapshot != "" {
			fmt.Fprintf(w, "@%s", o.parentSnapshot)
		}
		if !o.forkedAt.IsZero() {
			fmt.Fprintf(w, ", forked %s", o.forkedAt.UTC().Format("2006-01-02"))
		}
		fmt.Fprintln(w, ")")
	}
	return nil
}

func vmTreeLabel(node *vmTreeNode) string {
	var tags []string
	if node.parentSnapshot != "" {
		tags = append(tags, "from snapshot "+node.parentSnapshot)
	}
	if !node.forkedAt.IsZero() {
		tags = append(tags, "forked "+node.forkedAt.Format("2006-01-02"))
	}
	if len(tags) == 0 {
		return node.name
	}
	return node.name + " (" + strings.Join(tags, ", ") + ")"
}
