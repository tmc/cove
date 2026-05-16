package vmtree

import (
	"encoding/json"
	"errors"
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

// Options configures vm tree rendering.
type Options struct {
	// JSON emits a structured tree (forest of root nodes with children).
	JSON bool
	// Orphans filters output to VMs whose ParentVM points at a missing
	// VM. The orphans are listed flat (no children walked, no roots
	// shown), which is the natural shape for a "what's broken?" sweep.
	Orphans bool
	// ReachableFromImage, when set, restricts output to a one-hop
	// image-rooted view: the image is the synthetic root and child VMs
	// (those whose ParentImage matches the ref) are direct children.
	// Mutually exclusive with Orphans.
	ReachableFromImage *ReachableImage
}

// ReachableImage describes an image-rooted VM reachability query.
type ReachableImage struct {
	Ref      string
	Exists   func(string) bool
	Children func(string) ([]string, error)
}

// Print writes the VM fork lineage tree with default options
// (ASCII, full forest). Kept as a stable entry point for callers that
// don't need flag handling.
func Print(w io.Writer) error {
	return PrintWithOptions(w, Options{})
}

// PrintWithOptions writes the VM fork lineage tree honoring opts.
func PrintWithOptions(w io.Writer, opts Options) error {
	if opts.ReachableFromImage != nil && opts.Orphans {
		return errors.New("vm tree: --reachable-from and --orphans are mutually exclusive")
	}
	if opts.ReachableFromImage != nil {
		return renderReachableFromImage(w, *opts.ReachableFromImage, opts.JSON)
	}
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

// reachableChildJSON is the on-the-wire shape for one VM under an
// image-rooted reachability view. Kept flat: this is a one-hop query,
// not a recursive forest.
type reachableChildJSON struct {
	Name     string `json:"name"`
	ForkedAt string `json:"forkedAt,omitempty"`
	Orphan   bool   `json:"orphan,omitempty"`
}

// reachableImageJSON is the JSON shape emitted by `cove vm tree
// --reachable-from <ref> --json`.
type reachableImageJSON struct {
	Image    string               `json:"image"`
	Children []reachableChildJSON `json:"children"`
}

// renderReachableFromImage emits the image-rooted reachability view:
// the image as a synthetic root and its direct child VMs (one-hop).
// Errors out if the image does not exist on disk.
func renderReachableFromImage(w io.Writer, image ReachableImage, asJSON bool) error {
	if image.Ref == "" {
		return fmt.Errorf("image ref is empty")
	}
	if image.Exists == nil || !image.Exists(image.Ref) {
		return fmt.Errorf("image %s not found", image.Ref)
	}
	if image.Children == nil {
		return fmt.Errorf("image %s children lookup is unavailable", image.Ref)
	}
	names, err := image.Children(image.Ref)
	if err != nil {
		return err
	}

	// Walk each child's config to surface forkedAt + orphan flag. Orphan
	// here mirrors Phase 4 semantics: a non-empty ParentVM that does not
	// resolve to any VM in the inventory.
	inv, err := vmconfig.List(nil)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(inv))
	for _, vm := range inv {
		known[vm.Name] = struct{}{}
	}

	type childInfo struct {
		name     string
		forkedAt time.Time
		orphan   bool
	}
	children := make([]childInfo, 0, len(names))
	for _, name := range names {
		path := vmconfig.Path(name)
		cfg, lerr := vmconfig.Load(path)
		if lerr != nil {
			// Skip unreadable configs but don't fail the whole render;
			// the name is still surfaced via VMsForkedFromImage.
			children = append(children, childInfo{name: name})
			continue
		}
		c := childInfo{name: name, forkedAt: cfg.ForkedAt}
		if cfg.ParentVM != "" {
			if _, ok := known[cfg.ParentVM]; !ok {
				c.orphan = true
			}
		}
		children = append(children, c)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].name < children[j].name })

	if asJSON {
		out := reachableImageJSON{
			Image:    image.Ref,
			Children: make([]reachableChildJSON, 0, len(children)),
		}
		for _, c := range children {
			entry := reachableChildJSON{Name: c.name, Orphan: c.orphan}
			if !c.forkedAt.IsZero() {
				entry.ForkedAt = c.forkedAt.UTC().Format(time.RFC3339)
			}
			out.Children = append(out.Children, entry)
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(children) == 0 {
		fmt.Fprintf(w, "No VMs forked from %s.\n", image.Ref)
		return nil
	}
	fmt.Fprintf(w, "%s (image)\n", image.Ref)
	for i, c := range children {
		branch := "|-- "
		if i == len(children)-1 {
			branch = "`-- "
		}
		var parts []string
		if !c.forkedAt.IsZero() {
			parts = append(parts, "forked "+c.forkedAt.UTC().Format(time.RFC3339))
		}
		if c.orphan {
			parts = append(parts, "orphan")
		}
		if len(parts) == 0 {
			fmt.Fprintf(w, "%s%s\n", branch, c.name)
		} else {
			fmt.Fprintf(w, "%s%s (%s)\n", branch, c.name, strings.Join(parts, ", "))
		}
	}
	return nil
}
