package graphql

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// JSON is the schema's JSON scalar: any JSON value, in and out.
type JSON struct{ V any }

// ImplementsGraphQLType binds JSON to the JSON scalar.
func (JSON) ImplementsGraphQLType(name string) bool { return name == "JSON" }

// UnmarshalGraphQL takes an argument value (a literal or a variable).
func (j *JSON) UnmarshalGraphQL(input any) error {
	j.V = input
	return nil
}

// UnmarshalJSON reads a REST response value.
func (j *JSON) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &j.V)
}

// MarshalJSON writes the value into the GraphQL response.
func (j JSON) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// ID is the schema's ID scalar. REST ids are UUID strings or integers
// (jobs, segments, subtitles); both read into ID and leave as a string.
type ID string

// ImplementsGraphQLType binds ID to the built-in ID scalar.
func (ID) ImplementsGraphQLType(name string) bool { return name == "ID" }

// UnmarshalGraphQL takes an ID argument (a string or an integer).
func (id *ID) UnmarshalGraphQL(input any) error {
	switch v := input.(type) {
	case string:
		*id = ID(v)
	case int32:
		*id = ID(strconv.FormatInt(int64(v), 10))
	case int:
		*id = ID(strconv.Itoa(v))
	case float64:
		*id = ID(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return fmt.Errorf("wrong type for ID: %T", input)
	}
	return nil
}

// UnmarshalJSON reads a REST id: a JSON string or number.
func (id *ID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*id = ID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*id = ID(n.String())
	return nil
}

// MarshalJSON writes the id as a JSON string.
func (id ID) MarshalJSON() ([]byte, error) { return json.Marshal(string(id)) }

func (id ID) String() string { return string(id) }
