package plugins

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginpanel"
)

// The API's refusal code is the panel package's (one word for one refusal).
func TestTextForbiddenCode(t *testing.T) {
	if CodeTextForbidden != pluginpanel.CodeTextForbidden {
		t.Fatalf("%s != %s", CodeTextForbidden, pluginpanel.CodeTextForbidden)
	}
}
