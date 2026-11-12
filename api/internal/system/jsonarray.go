package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeJSONArray reads a JSON array from r one element at a time and returns its
// elements. Only one element's text is held at a time: decoding a whole answer at
// once keeps all of it in memory beside what is made of it, and GitHub's list of
// 30 releases is about 7 MB (some 150 assets each), read at every start and every
// check. That one burst left the API some 35 MB larger for good, since a page once
// touched stays counted in the process's resident memory after it is freed.
//
// It reads r to the end of the array and refuses anything after it but white
// space, as json.Unmarshal does.
func DecodeJSONArray[T any](r io.Reader) ([]T, error) {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil {
		// null: no elements, as json.Unmarshal gives for a slice
		return nil, trailing(dec)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("json: an array was expected, found %v", tok)
	}
	out := []T{}
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if _, err := dec.Token(); err != nil { // the closing bracket
		return nil, err
	}
	return out, trailing(dec)
}

// trailing refuses anything left in the stream after the value.
func trailing(dec *json.Decoder) error {
	switch _, err := dec.Token(); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return err
	}
	return errors.New("json: data after the array")
}
