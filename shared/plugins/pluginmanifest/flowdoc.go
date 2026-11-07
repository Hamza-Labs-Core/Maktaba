package pluginmanifest

import (
	"sync"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

var (
	flowOnce   sync.Once
	flowRoot   *node
	flowErr    error
	flowRemaps = map[string]string{CodeMissingKey: "flow_invalid", CodeUnknownKey: "flow_unknown_key", CodeInvalid: "flow_invalid"}
)

// ValidateFlowDoc parses a flow.toml and checks it against shared/plugins/flow.schema.json
// (story 54.39 §1), with the same schema reader as the manifest. It answers the document
// with its defaults filled, or the refusals with flow_* codes. The checks across plugins
// are pluginflow's.
func ValidateFlowDoc(src []byte) (map[string]any, Errors) {
	flowOnce.Do(func() { flowRoot, flowErr = parseSchema(plugins.FlowSchema) })
	if flowErr != nil {
		panic(flowErr)
	}
	raw, err := parseTOML(src)
	if err != nil {
		return nil, Errors{{Code: "flow_invalid", Detail: "not TOML"}}
	}
	var errs []Error
	flowRoot.check(raw, "", "", false, &errs) // a flow names no trusted key
	if len(errs) > 0 {
		for i := range errs {
			if c, ok := flowRemaps[errs[i].Code]; ok {
				errs[i].Code = c
			}
			// the detail a flow refusal shows is where: the slash path into flow.toml
			errs[i].Detail = errs[i].Path
		}
		return nil, errs
	}
	return flowRoot.fill(raw).(map[string]any), nil
}
