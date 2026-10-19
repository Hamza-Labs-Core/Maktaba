package graphql

// Depth and complexity limits, checked before anything executes.
//
// Complexity: every field costs 1; a list field multiplies what it
// selects by its page size — the limit / first argument when given,
// else DefaultListSize. So videos(limit: 200) { tags { name } } costs
// 1 + 200 * (1 + 20 * 1). An operation above MaxComplexity, or deeper than
// MaxDepth, is rejected whole with extensions.code cost-limit-exceeded /
// depth-limit-exceeded. Introspection (__schema, __type) is exempt from
// both, as a client's schema download is deep by nature.

import (
	"fmt"
	"math"
	"strconv"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Defaults for the limits (Handler.MaxDepth / MaxComplexity override).
const (
	DefaultMaxDepth      = 12
	DefaultMaxComplexity = 10000
	// DefaultListSize is the assumed length of a list field without a
	// limit / first argument.
	DefaultListSize = 20
)

var parsedSchema = gqlparser.MustLoadSchema(&ast.Source{Name: "schema.graphql", Input: Schema})

// plan is what the analysis learned about one operation.
type plan struct {
	operation  string // query | mutation | subscription
	rootFields []string
	depth      int
	cost       float64
}

// analyse parses and validates the operation and applies the limits. It
// returns GraphQL-shaped errors on failure.
func (h *Handler) analyse(query, operationName string, vars map[string]any) (*plan, []map[string]any) {
	doc, errs := gqlparser.LoadQueryWithRules(parsedSchema, query, nil)
	if len(errs) > 0 {
		return nil, gqlErrList(errs)
	}
	var op *ast.OperationDefinition
	if operationName == "" && len(doc.Operations) == 1 {
		op = doc.Operations[0]
	} else {
		op = doc.Operations.ForName(operationName)
	}
	if op == nil {
		return nil, []map[string]any{gqlErr("unknown or ambiguous operation "+strconv.Quote(operationName), "bad-request")}
	}
	p := &plan{operation: string(op.Operation)}
	for _, f := range collectFields(op.SelectionSet) {
		p.rootFields = append(p.rootFields, f.Name)
	}
	p.cost, p.depth = measure(op.SelectionSet, vars, 1, 0)

	maxDepth := h.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxCost := h.MaxComplexity
	if maxCost <= 0 {
		maxCost = DefaultMaxComplexity
	}
	if p.depth > maxDepth {
		return nil, []map[string]any{gqlErr(fmt.Sprintf("query depth %d exceeds the limit of %d", p.depth, maxDepth), "depth-limit-exceeded")}
	}
	if p.cost > float64(maxCost) {
		return nil, []map[string]any{gqlErr(fmt.Sprintf("query complexity %.0f exceeds the limit of %d", p.cost, maxCost), "cost-limit-exceeded")}
	}
	return p, nil
}

// collectFields flattens fragments into the fields of a selection set.
func collectFields(set ast.SelectionSet) []*ast.Field {
	var out []*ast.Field
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			out = append(out, s)
		case *ast.InlineFragment:
			out = append(out, collectFields(s.SelectionSet)...)
		case *ast.FragmentSpread:
			if s.Definition != nil {
				out = append(out, collectFields(s.Definition.SelectionSet)...)
			}
		}
	}
	return out
}

// measure returns the cost and depth of a selection set at depth level.
// page is the page size a connection field (videos, jobs: an object with
// a limit argument) hands to its list of items.
func measure(set ast.SelectionSet, vars map[string]any, level int, page float64) (cost float64, depth int) {
	for _, f := range collectFields(set) {
		if len(f.Name) > 1 && f.Name[:2] == "__" {
			continue // __typename, __schema, __type
		}
		isList := f.Definition != nil && f.Definition.Type != nil && f.Definition.Type.Elem != nil
		size, sized := listSize(f, vars)
		mult, childPage := 1.0, 0.0
		switch {
		case isList && sized:
			mult = size
		case isList && page > 0:
			mult = page
		case isList:
			mult = DefaultListSize
		case sized:
			childPage = size // a connection: its items list is this long
		}
		childCost, childDepth := measure(f.SelectionSet, vars, level+1, childPage)
		cost += 1 + mult*childCost
		d := level
		if childDepth > d {
			d = childDepth
		}
		if d > depth {
			depth = d
		}
	}
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		cost = math.MaxFloat64
	}
	return cost, depth
}

// listSize is the page size a field asks for through limit / first.
func listSize(f *ast.Field, vars map[string]any) (float64, bool) {
	for _, name := range []string{"limit", "first"} {
		a := f.Arguments.ForName(name)
		if a == nil {
			continue
		}
		v, err := a.Value.Value(vars)
		if err != nil || v == nil {
			continue
		}
		switch n := v.(type) {
		case int64:
			return math.Max(float64(n), 1), true
		case int:
			return math.Max(float64(n), 1), true
		case float64:
			return math.Max(n, 1), true
		}
	}
	return 0, false
}

func gqlErr(msg, code string) map[string]any {
	return map[string]any{"message": msg, "extensions": map[string]any{"code": code}}
}

func gqlErrList(errs gqlerror.List) []map[string]any {
	out := make([]map[string]any, 0, len(errs))
	for _, e := range errs {
		m := gqlErr(e.Message, "validation-failed")
		if len(e.Locations) > 0 {
			m["locations"] = e.Locations
		}
		out = append(out, m)
	}
	return out
}
