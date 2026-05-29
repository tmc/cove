package guibench

import "testing"

func TestInSubset(t *testing.T) {
	tests := []struct {
		name   string
		subset []string
		query  string
		want   bool
	}{
		{"empty query matches all", nil, "", true},
		{"member", []string{"test_small", "smoke"}, "test_small", true},
		{"non-member", []string{"smoke"}, "test_small", false},
		{"no subsets non-empty query", nil, "test_small", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{ID: "t", Subset: tt.subset}
			if got := task.InSubset(tt.query); got != tt.want {
				t.Errorf("InSubset(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestSelectSubset(t *testing.T) {
	tasks := []*Task{
		{ID: "a", Subset: []string{SubsetTestSmall}},
		{ID: "b"},
		{ID: "c", Subset: []string{SubsetTestSmall, "smoke"}},
	}

	all, err := SelectSubset(tasks, "")
	if err != nil {
		t.Fatalf("SelectSubset(\"\"): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("empty subset = %d tasks, want 3 (whole corpus)", len(all))
	}

	small, err := SelectSubset(tasks, SubsetTestSmall)
	if err != nil {
		t.Fatalf("SelectSubset(test_small): %v", err)
	}
	if len(small) != 2 || small[0].ID != "a" || small[1].ID != "c" {
		t.Errorf("test_small = %v, want [a c] in order", taskIDList(small))
	}

	if _, err := SelectSubset(tasks, "nonexistent"); err == nil {
		t.Error("want error selecting a subset no task belongs to")
	}
}

func taskIDList(tasks []*Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}
