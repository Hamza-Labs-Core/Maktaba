package fppackage

import "github.com/BurntSushi/toml"

func tomlDecode(src []byte, v any) (toml.MetaData, error) { return toml.Decode(string(src), v) }
