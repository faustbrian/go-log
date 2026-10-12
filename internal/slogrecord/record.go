// Package slogrecord owns cumulative structural admission and group-tree copies
// for package handlers. CloneRaw preserves LogValuer identity; CloneResolved
// uses slog.Value.Resolve before recursively copying groups. Neither deep-copies
// opaque maps/pointers nor limits strings or collaborator work. Callers must
// discard partial records on error and own value lifetime/concurrent mutation.
// No worker, sink, provider or public alternative record model is introduced.
package slogrecord

import (
	"errors"
	"log/slog"
)

// MaxRecordAttributes is the shared structural attribute budget.
const MaxRecordAttributes = 1024

// MaxRecordDepth is the shared structural group-depth budget.
const MaxRecordDepth = 32

// ErrRecordLimit reports rejection by the shared structural budget.
var ErrRecordLimit = errors.New("log: record attribute limit exceeded")

// CloneResolved returns a structurally independent resolved record under fixed
// limits. On a nested failure the returned record may contain an admitted
// prefix; callers must discard it when err is non-nil. Resolved opaque values
// still retain object identity, and slog owns panic/recursion handling in Resolve.
func CloneResolved(record slog.Record) (slog.Record, error) {
	return clone(record, Usage{}, true)
}

// CloneRaw returns a structurally independent record without evaluating
// non-group LogValuer values.
func CloneRaw(record slog.Record) (slog.Record, error) {
	return clone(record, Usage{}, false)
}

// Usage tracks structure retained by a chain of handler derivations.
type Usage struct {
	// Attributes is the retained attribute-node count, including charged groups.
	Attributes int
	// Groups is the retained derivation depth charged when a batch has attrs.
	Groups int
}

// Start returns the initial attribute count and depth for one derived or
// record batch after checking its top-level shape. Empty batches do not charge
// retained groups or visit a node; their returned depth may exceed the visit cap.
// Usage is copied by value; reject invalid retained state before allocating.
// count must come from a non-negative actual slice/record attribute count.
func (usage Usage) Start(count int) (int, int, error) {
	if err := preflight(count, usage); err != nil {
		return 0, 0, err
	}
	attributes := usage.Attributes
	if count > 0 {
		attributes += usage.Groups
	}

	return attributes, usage.Groups + 1, nil
}

// WithGroup returns usage for one additional retained group.
func (usage Usage) WithGroup(name string) (Usage, error) {
	if name == "" {
		return usage, nil
	}
	if usage.Groups >= MaxRecordDepth {
		return Usage{}, ErrRecordLimit
	}
	usage.Groups++

	return usage, nil
}

// CloneResolvedWithUsage clones a record while including retained derivation
// structure in the fixed limits.
func CloneResolvedWithUsage(record slog.Record, usage Usage) (slog.Record, error) {
	return clone(record, usage, true)
}

// CloneRawWithUsage clones a record without resolving non-group values while
// including retained derivation structure in the fixed limits.
func CloneRawWithUsage(record slog.Record, usage Usage) (slog.Record, error) {
	return clone(record, usage, false)
}

func clone(record slog.Record, usage Usage, resolve bool) (slog.Record, error) {
	attributes, depth, err := usage.Start(record.NumAttrs())
	if err != nil {
		return slog.Record{}, err
	}
	cloned := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	budget := budget{attributes: attributes}
	var result error
	record.Attrs(func(attr slog.Attr) bool {
		owned, err := cloneAttr(attr, depth, &budget, resolve)
		if err != nil {
			result = err
			return false
		}
		cloned.AddAttrs(owned)
		return true
	})

	return cloned, result
}

// CloneResolvedAttrs returns independent resolved attributes under fixed limits.
func CloneResolvedAttrs(attrs []slog.Attr) ([]slog.Attr, error) {
	cloned, _, err := cloneAttrs(attrs, Usage{}, true)
	return cloned, err
}

// CloneRawAttrs returns independent attributes without evaluating non-group
// LogValuer values.
func CloneRawAttrs(attrs []slog.Attr) ([]slog.Attr, error) {
	cloned, _, err := cloneAttrs(attrs, Usage{}, false)
	return cloned, err
}

// CloneResolvedAttrsWithUsage clones and accounts for retained derived attrs.
func CloneResolvedAttrsWithUsage(attrs []slog.Attr, usage Usage) ([]slog.Attr, Usage, error) {
	return cloneAttrs(attrs, usage, true)
}

// CloneRawAttrsWithUsage clones and accounts for retained derived attrs
// without resolving non-group values.
func CloneRawAttrsWithUsage(attrs []slog.Attr, usage Usage) ([]slog.Attr, Usage, error) {
	return cloneAttrs(attrs, usage, false)
}

func cloneAttrs(attrs []slog.Attr, usage Usage, resolve bool) ([]slog.Attr, Usage, error) {
	attributes, depth, err := usage.Start(len(attrs))
	if err != nil {
		return nil, Usage{}, err
	}
	if len(attrs) == 0 {
		return []slog.Attr{}, usage, nil
	}
	budget := budget{attributes: attributes}
	cloned := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		owned, err := cloneAttr(attr, depth, &budget, resolve)
		if err != nil {
			return nil, Usage{}, err
		}
		cloned = append(cloned, owned)
	}

	usage.Attributes = budget.attributes
	return cloned, usage, nil
}

func preflight(count int, usage Usage) error {
	if usage.Attributes < 0 || usage.Groups < 0 || usage.Groups > MaxRecordDepth ||
		count > MaxRecordAttributes-usage.Attributes {
		return ErrRecordLimit
	}
	if count > 0 && usage.Groups > MaxRecordAttributes-usage.Attributes-count {
		return ErrRecordLimit
	}

	return nil
}

type budget struct{ attributes int }

func (b *budget) visit(depth int) error {
	if depth > MaxRecordDepth || b.attributes >= MaxRecordAttributes {
		return ErrRecordLimit
	}
	b.attributes++
	return nil
}

func cloneAttr(attr slog.Attr, depth int, budget *budget, resolve bool) (slog.Attr, error) {
	if err := budget.visit(depth); err != nil {
		return slog.Attr{}, err
	}
	value := attr.Value
	if resolve {
		value = value.Resolve()
	}
	if value.Kind() != slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: value}, nil
	}
	children := value.Group()
	// Admit all immediate children before allocating/calling a child's Resolve.
	// Checking incrementally would evaluate an early child in an impossible tree
	// (TestOrdinaryGroupChildrenAdmittedBeforeAnyChildWork).
	if len(children) > MaxRecordAttributes-budget.attributes {
		return slog.Attr{}, ErrRecordLimit
	}
	cloned := make([]slog.Attr, 0, len(children))
	for _, child := range children {
		owned, err := cloneAttr(child, depth+1, budget, resolve)
		if err != nil {
			return slog.Attr{}, err
		}
		cloned = append(cloned, owned)
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(cloned...)}, nil
}
