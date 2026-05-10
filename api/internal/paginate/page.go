package paginate

// Cursorable is implemented by domain types whose row carries enough
// state to mint the next page's cursor. The handler typically defines
// a small adapter around the row struct that returns the (updated_at,
// id) pair.
type Cursorable interface {
	PageCursor() Cursor
}

// Page is the JSON envelope every list endpoint returns:
// `{ items: [...], next: "..." | null }`. Generic so each handler keeps
// its own row type and the JSON marshalling stays free of type-erasure.
type Page[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next"`
}

// Bound trims items to limit and computes `next` from the (limit+1)-th
// row when present. Callers SELECT `limit+1` rows; if the (limit+1)-th
// is present, there's another page; the cursor is encoded from the
// `limit`-th row (the last row that survives the trim).
func Bound[T Cursorable](items []T, limit int) Page[T] {
	if limit < 1 {
		// Defensive: a zero or negative limit would crash on slicing.
		// ParseLimit rejects this, so reaching here means a programmer
		// error in the call site. Returning an empty page is safer
		// than panicking.
		return Page[T]{Items: items, Next: nil}
	}
	if len(items) <= limit {
		return Page[T]{Items: items, Next: nil}
	}
	last := items[limit-1]
	enc := Encode(last.PageCursor())
	return Page[T]{Items: items[:limit], Next: &enc}
}
