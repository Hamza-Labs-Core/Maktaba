package pluginsettings

import (
	"errors"
	"strings"
	"testing"
)

var words = map[string]map[string]string{
	"en": {"s.t": "Threshold", "s.h": "Help", "s.l": "Language"},
	"ar": {"s.t": "العتبة", "s.h": "مساعدة", "s.l": "اللغة"},
}

const good = `{"type":"object","properties":{
 "threshold":{"type":"integer","minimum":1,"maximum":100,"default":30,"x-maktaba-title":"s.t","x-maktaba-help":"s.h","x-maktaba-widget":"slider"},
 "lang":{"type":"string","enum":["ar","en"],"default":"ar","x-maktaba-title":"s.l","x-maktaba-widget":"select"},
 "code":{"type":"string","maxLength":8,"pattern":"^[a-z]{2,8}$","default":"ab","x-maktaba-title":"s.t"},
 "on":{"type":"boolean","default":true,"x-maktaba-title":"s.t"},
 "kinds":{"type":"array","items":{"type":"string","enum":["a","b"]},"default":["a"],"x-maktaba-title":"s.t"}}}`

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// 54.11 T01-T03: the subset, secrets refused, values checked on the server.
func TestSchemaAndValues(t *testing.T) {
	s, err := Parse([]byte(good), words)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Properties) != 5 || s.Properties[0].Name != "code" {
		t.Fatalf("%+v", s.Properties)
	}
	for _, bad := range []struct{ schema, code string }{
		{`{"type":"object","properties":{"api_key":{"type":"string","default":"","x-maktaba-title":"s.t"}}}`, CodeSecretForbid},
		{`{"type":"object","properties":{"Secret_Token":{"type":"string","default":"","x-maktaba-title":"s.t"}}}`, CodeSecretForbid},
		{`{"type":"object","properties":{"pin":{"type":"string","format":"password","default":"","x-maktaba-title":"s.t"}}}`, CodeSecretForbid},
		{`{"type":"object","properties":{"n":{"type":"integer","x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"object","default":{},"x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"integer","default":1,"x-maktaba-title":"s.missing"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"integer","default":1,"x-other":1,"x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"string","pattern":"(a)\\1","default":"","x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"string","enum":["https://x.example"],"default":"https://x.example","x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
		{`{"type":"object","properties":{"n":{"type":"integer","minimum":5,"default":1,"x-maktaba-title":"s.t"}}}`, CodeSchemaInvalid},
	} {
		if _, err := Parse([]byte(bad.schema), words); code(err) != bad.code {
			t.Errorf("%s: %v, want %s", bad.schema, err, bad.code)
		}
	}
	got, err := s.Check(map[string]any{"threshold": float64(40)}, nil)
	if err != nil || got["threshold"] != float64(40) || got["lang"] != "ar" {
		t.Fatalf("%v %v", got, err)
	}
	for _, v := range []map[string]any{
		{"threshold": float64(101)}, {"threshold": 4.5}, {"lang": "fr"}, {"code": "ABC"},
		{"code": "http://x"}, {"on": "yes"}, {"kinds": []any{"c"}}, {"nope": 1.0},
	} {
		if _, err := s.Check(v, nil); code(err) != CodeInvalid {
			t.Errorf("%v: %v", v, err)
		}
	}
	vals, reset := s.Carry(map[string]any{"threshold": float64(500), "lang": "en", "gone": 1.0})
	if vals["threshold"] != float64(30) || vals["lang"] != "en" || strings.Join(reset, ",") != "threshold" || vals["gone"] != nil {
		t.Fatalf("carry %v %v", vals, reset)
	}
}
