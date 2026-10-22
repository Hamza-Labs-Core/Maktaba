package supervisor

import "github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"

// childEnvOK is ChildEnv for a test whose node config can be read: its error panics.
func childEnvOK(role Role, cfg config.Config) []string {
	env, err := ChildEnv(role, cfg)
	if err != nil {
		panic(err)
	}
	return env
}
