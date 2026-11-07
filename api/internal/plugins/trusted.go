package plugins

import "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"

// The trust refusals (story 54.22 §2; package firstparty has the check).
const (
	CodeTrustedUnsigned = firstparty.CodeTrustedUnsigned
	CodeTrustedVersion  = firstparty.CodeTrustedVersion
)

// FirstParty is one of Maktaba's own plugins as this release built it.
type FirstParty = firstparty.FirstParty

// FirstPartyPlugins answers the plugins built into this server.
func FirstPartyPlugins() ([]*FirstParty, error) { return firstparty.Plugins() }

// FirstPartyByID answers one first-party plugin built into this server.
func FirstPartyByID(id string) *FirstParty { return firstparty.ByID(id) }

// FirstPartyByStage answers the first-party plugin that defines a stage.
func FirstPartyByStage(stage string) *FirstParty { return firstparty.ByStage(stage) }

func strs(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
