package claudecode

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Change is a single proposed env-var modification.
type Change struct {
	Key    string
	Before string
	After  string
}

// ChangeSet partitions proposed env changes by kind.
type ChangeSet struct {
	Added   []Change
	Updated []Change
	Removed []Change
}

// DiffEnv compares the existing env block with the proposed one. Output is
// sorted by key for deterministic presentation.
func DiffEnv(before, after map[string]string) ChangeSet {
	var changes ChangeSet
	for _, key := range slices.Sorted(maps.Keys(after)) {
		previous, existed := before[key]
		switch {
		case !existed:
			changes.Added = append(changes.Added, Change{Key: key, After: after[key]})
		case previous != after[key]:
			changes.Updated = append(changes.Updated, Change{Key: key, Before: previous, After: after[key]})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(before)) {
		if _, kept := after[key]; !kept {
			changes.Removed = append(changes.Removed, Change{Key: key, Before: before[key]})
		}
	}
	return changes
}

// Empty reports whether applying the set would change nothing.
func (c ChangeSet) Empty() bool {
	return len(c.Added) == 0 && len(c.Updated) == 0 && len(c.Removed) == 0
}

// Destructive reports whether the set overwrites or deletes existing values.
func (c ChangeSet) Destructive() bool {
	return len(c.Updated) > 0 || len(c.Removed) > 0
}

// Format renders the set as an indented "+ / ~ / -" description.
func (c ChangeSet) Format() string {
	var lines []string
	for _, change := range c.Added {
		lines = append(lines, fmt.Sprintf("  + %s = %s", change.Key, change.After))
	}
	for _, change := range c.Updated {
		lines = append(lines, fmt.Sprintf("  ~ %s: %s -> %s", change.Key, change.Before, change.After))
	}
	for _, change := range c.Removed {
		lines = append(lines, fmt.Sprintf("  - %s (removed)", change.Key))
	}
	return strings.Join(lines, "\n")
}
