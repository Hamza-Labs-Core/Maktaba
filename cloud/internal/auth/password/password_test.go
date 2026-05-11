package password

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		name, pw string
		want     error
	}{
		{"empty", "", ErrTooShort},
		{"short", "abc", ErrTooShort},
		{"common", "password123", ErrLeaked},
		{"ok", "correcthorsebattery", nil},
		{"trailing space", "thispassword ", ErrWhitespace},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Validate(c.pw); got != c.want {
				t.Errorf("Validate(%q) = %v, want %v", c.pw, got, c.want)
			}
		})
	}
}
