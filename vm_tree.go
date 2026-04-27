package main

import (
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
	children       []*vmTreeNode
}

// PrintVMTree writes the VM fork lineage tree.
func PrintVMTree(w io.Writer) error {
	vms, err := vmconfig.List(nil)
	if err != nil {
		return err
	}
	if len(vms) == 0 {
		fmt.Fprintln(w, "No VMs found.")
		return nil
	}

	nodes := make(map[string]*vmTreeNode)
	for _, vm := range vms {
		cfg, err := vmconfig.Load(vm.Path)
		if err != nil {
			return fmt.Errorf("load %s config: %w", vm.Name, err)
		}
		nodes[vm.Name] = &vmTreeNode{
			name:           vm.Name,
			parent:         cfg.ParentVM,
			parentSnapshot: cfg.ParentSnapshot,
			forkedAt:       cfg.ForkedAt,
		}
	}

	var roots []*vmTreeNode
	for _, node := range nodes {
		parent := nodes[node.parent]
		if parent == nil {
			roots = append(roots, node)
			continue
		}
		parent.children = append(parent.children, node)
	}
	sortVMTree(roots)

	for i, root := range roots {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printVMTreeNode(w, root, "", true, true)
	}
	return nil
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
