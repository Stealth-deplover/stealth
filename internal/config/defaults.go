package config

// WithDefaults returns a copy of the configuration with every unset or
// non-positive field replaced by the value the HTTP API would otherwise
// assume. Environment loading via Load already validates its own env
// fallbacks; these defaults cover struct-literal configurations such as
// tests and embedded API setups. Derived limits are applied in dependency
// order, so the storage defaults feed the Functions/Sites limits.
func (c Config) WithDefaults() Config {
	c.applyDatabaseDefaults()
	c.applyAuthDefaults()
	c.applyStorageDefaults()
	c.applyExecutionDefaults()
	c.applySiteDefaults()
	return c
}
