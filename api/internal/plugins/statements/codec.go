package statements

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Values between the plugin's runner and Postgres (the Python side is
// maktaba_plugin_sdk/native/codec.py). Plain JSON carries null, booleans, numbers,
// strings and arrays; every other value is {"$t": type, "v": text}, so the plugin's code
// reads exactly the Python types asyncpg answers in-process: uuid, ts (timestamptz), tsn
// (timestamp), date, time, dec (numeric), bytes (bytea, base64), iv (interval, in
// microseconds), float (NaN and the infinities).

type tagged struct {
	T string `json:"$t"`
	V string `json:"v"`
}

func decodeArgs(raw []json.RawMessage) ([]any, error) {
	out := make([]any, len(raw))
	for i, r := range raw {
		v, err := decodeArg(r)
		if err != nil {
			return nil, fmt.Errorf("argument $%d: %w", i+1, err)
		}
		out[i] = v
	}
	return out, nil
}

// decodeArg turns one argument into what lib/pq sends: an array as its Postgres text
// (the parameter's type, which Postgres infers, reads it), a timestamp as time.Time.
func decodeArg(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return argValue(v, false)
}

func argValue(v any, inArray bool) (any, error) {
	switch t := v.(type) {
	case nil, bool, string:
		return t, nil
	case json.Number:
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return i, nil
		}
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case []any:
		if inArray {
			return nil, fmt.Errorf("nested arrays")
		}
		parts := make([]string, len(t))
		for i, x := range t {
			e, err := argValue(x, true)
			if err != nil {
				return nil, err
			}
			parts[i] = arrayElem(e)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case map[string]any:
		typ, _ := t["$t"].(string)
		s, _ := t["v"].(string)
		switch typ {
		case "uuid", "tsn", "date", "time", "dec":
			return s, nil
		case "ts":
			ts, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return nil, err
			}
			return ts, nil
		case "bytes":
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, err
			}
			return b, nil
		case "iv":
			us, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, err
			}
			return strconv.FormatInt(us, 10) + " microseconds", nil
		case "float":
			switch s {
			case "NaN":
				return math.NaN(), nil
			case "Infinity":
				return math.Inf(1), nil
			case "-Infinity":
				return math.Inf(-1), nil
			}
		}
		return nil, fmt.Errorf("unknown value type %q", typ)
	}
	return nil, fmt.Errorf("unsupported value %T", v)
}

func arrayElem(e any) string {
	switch t := e.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "t"
		}
		return "f"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case time.Time:
		return `"` + t.Format(time.RFC3339Nano) + `"`
	case []byte:
		return `"\\x` + fmt.Sprintf("%x", t) + `"`
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(t) + `"`
	}
	return "NULL"
}

// encodeRows reads every row (at most limit when it is above 0).
func encodeRows(rows *sql.Rows, limit int) (*Response, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := &Response{Columns: cols, Rows: [][]any{}}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i], err = encodeValue(types[i].DatabaseTypeName(), v)
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", cols[i], err)
			}
		}
		out.Rows = append(out.Rows, row)
		if limit > 0 && len(out.Rows) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func tag(t, v string) tagged { return tagged{T: t, V: v} }

// encodeValue answers one column value as the Python side decodes it, by its type name.
func encodeValue(typ string, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if strings.HasPrefix(typ, "_") {
		text, ok := asText(v)
		if !ok {
			return nil, fmt.Errorf("array %s as %T", typ, v)
		}
		elems, err := parseArray(text)
		if err != nil {
			return nil, err
		}
		out := make([]any, len(elems))
		for i, e := range elems {
			if e == nil {
				continue
			}
			out[i], err = encodeText(typ[1:], *e)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	switch t := v.(type) {
	case int64:
		return t, nil
	case float64:
		return encodeFloat(t), nil
	case bool:
		return t, nil
	case time.Time:
		switch typ {
		case "DATE":
			return tag("date", t.Format("2006-01-02")), nil
		case "TIMESTAMP":
			return tag("tsn", t.Format("2006-01-02T15:04:05.999999")), nil
		default:
			return tag("ts", t.UTC().Format(time.RFC3339Nano)), nil
		}
	case []byte:
		if typ == "BYTEA" {
			return tag("bytes", base64.StdEncoding.EncodeToString(t)), nil
		}
		return encodeText(typ, string(t))
	case string:
		return encodeText(typ, t)
	}
	return nil, fmt.Errorf("unsupported %T", v)
}

func asText(v any) (string, bool) {
	switch t := v.(type) {
	case []byte:
		return string(t), true
	case string:
		return t, true
	}
	return "", false
}

func encodeFloat(f float64) any {
	switch {
	case math.IsNaN(f):
		return tag("float", "NaN")
	case math.IsInf(f, 1):
		return tag("float", "Infinity")
	case math.IsInf(f, -1):
		return tag("float", "-Infinity")
	}
	return f
}

// encodeText reads a value from its Postgres text by its type name.
func encodeText(typ, s string) (any, error) {
	switch typ {
	case "INT2", "INT4", "INT8", "OID":
		return strconv.ParseInt(s, 10, 64)
	case "FLOAT4", "FLOAT8":
		f, err := strconv.ParseFloat(strings.Replace(s, "Infinity", "Inf", 1), 64)
		if err != nil {
			return nil, err
		}
		return encodeFloat(f), nil
	case "BOOL":
		return s == "t" || s == "true", nil
	case "UUID":
		return tag("uuid", s), nil
	case "NUMERIC":
		return tag("dec", s), nil
	case "DATE":
		return tag("date", s), nil
	case "TIME":
		return tag("time", s), nil
	case "TIMESTAMPTZ":
		t, err := parsePGTime(s)
		if err != nil {
			return nil, err
		}
		return tag("ts", t.UTC().Format(time.RFC3339Nano)), nil
	case "TIMESTAMP":
		return tag("tsn", strings.Replace(s, " ", "T", 1)), nil
	case "INTERVAL":
		us, err := parseInterval(s)
		if err != nil {
			return nil, err
		}
		return tag("iv", strconv.FormatInt(us, 10)), nil
	case "BYTEA":
		if strings.HasPrefix(s, `\x`) {
			b := make([]byte, (len(s)-2)/2)
			for i := range b {
				x, err := strconv.ParseUint(s[2+2*i:4+2*i], 16, 8)
				if err != nil {
					return nil, err
				}
				b[i] = byte(x)
			}
			return tag("bytes", base64.StdEncoding.EncodeToString(b)), nil
		}
	}
	return s, nil
}

func parsePGTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05.999999-07:00:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Parse(time.RFC3339Nano, s)
}

var ivPart = regexp.MustCompile(`(-?\d+) (year|years|mon|mons|day|days)`)

// parseInterval reads Postgres's default interval output ("1 day 02:03:04.5",
// "-00:01:00", "3 mons") into microseconds, a month as 30 days and a year as 365.25
// days, as asyncpg converts one to a timedelta.
func parseInterval(s string) (int64, error) {
	var us int64
	rest := s
	for _, m := range ivPart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		day := int64(86400_000_000)
		switch m[2] {
		case "year", "years":
			us += n * 365 * day
			us += n * day / 4
		case "mon", "mons":
			us += n * 30 * day
		default:
			us += n * day
		}
		rest = strings.Replace(rest, m[0], "", 1)
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return us, nil
	}
	neg := strings.HasPrefix(rest, "-")
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "-"), "+")
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("interval %q", s)
	}
	h, err1 := strconv.ParseInt(parts[0], 10, 64)
	mi, err2 := strconv.ParseInt(parts[1], 10, 64)
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("interval %q", s)
	}
	t := h*3600_000_000 + mi*60_000_000 + int64(math.Round(sec*1e6))
	if neg {
		t = -t
	}
	return us + t, nil
}

// parseArray reads a one-dimensional Postgres array literal ({a,"b c",NULL}).
func parseArray(s string) ([]*string, error) {
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("array %q", s)
	}
	body := s[1 : len(s)-1]
	var out []*string
	if body == "" {
		return []*string{}, nil
	}
	i := 0
	for i <= len(body) {
		if i < len(body) && body[i] == '{' {
			return nil, fmt.Errorf("nested array %q", s)
		}
		if i < len(body) && body[i] == '"' {
			var b strings.Builder
			i++
			for i < len(body) && body[i] != '"' {
				if body[i] == '\\' && i+1 < len(body) {
					i++
				}
				b.WriteByte(body[i])
				i++
			}
			i++ // the closing quote
			v := b.String()
			out = append(out, &v)
		} else {
			j := strings.IndexByte(body[i:], ',')
			if j < 0 {
				j = len(body) - i
			}
			v := body[i : i+j]
			if v == "NULL" {
				out = append(out, nil)
			} else {
				out = append(out, &v)
			}
			i += j
		}
		i++ // the comma
	}
	return out, nil
}
