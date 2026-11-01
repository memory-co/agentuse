package harness

type Config struct {
	ConfigPath string
	Harnesses  map[string]HarnessConfig
}
type HarnessConfig struct {
	Command  string
	Args     []string
	Disabled bool
}
