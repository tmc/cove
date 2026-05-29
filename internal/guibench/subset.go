package guibench

import "fmt"

// SubsetTestSmall is the canonical CI subset name (OSWorld's test_small pattern,
// §8): a small, representative slice of the corpus that a CI run can score
// without paying for the full matrix.
const SubsetTestSmall = "test_small"

// InSubset reports whether the task is a member of the named subset. A task
// joins a subset by listing the name in its Subset field. The empty subset name
// matches every task, so callers can treat "" as "the whole corpus".
func (t *Task) InSubset(name string) bool {
	if name == "" {
		return true
	}
	for _, s := range t.Subset {
		if s == name {
			return true
		}
	}
	return false
}

// SelectSubset returns the tasks belonging to the named subset, preserving
// input order. An empty name returns every task. It is an error to select a
// subset no task belongs to, so a typo in --subset fails loudly instead of
// silently scoring zero tasks.
func SelectSubset(tasks []*Task, name string) ([]*Task, error) {
	if name == "" {
		return tasks, nil
	}
	out := make([]*Task, 0, len(tasks))
	for _, t := range tasks {
		if t.InSubset(name) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("subset %q matches no task", name)
	}
	return out, nil
}
